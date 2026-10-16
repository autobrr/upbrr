// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build windows

package ffmpegstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// Pipe owns one child-authenticated output stream. Do not copy it after use begins.
type Pipe struct {
	path       string
	handle     windows.Handle
	event      windows.Handle
	overlapped *windows.Overlapped
	pending    bool
	connected  bool
}

// NewPipes creates and arms count private listeners before the child starts.
// The caller must release them with ClosePipes.
func NewPipes(count int) ([]Pipe, error) {
	outputs := make([]Pipe, 0, count)
	for range count {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			ClosePipes(outputs)
			return nil, fmt.Errorf("name PCM pipe: %w", err)
		}
		path := `\\.\pipe\upbrr-audio-` + hex.EncodeToString(nonce[:])
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			ClosePipes(outputs)
			return nil, fmt.Errorf("encode PCM pipe name: %w", err)
		}
		handle, err := windows.CreateNamedPipe(name,
			windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
			1, 0, 256<<10, 0, nil)
		if err != nil {
			ClosePipes(outputs)
			return nil, fmt.Errorf("create PCM pipe: %w", err)
		}
		outputs = append(outputs, Pipe{path: path, handle: handle})
		// Arm acceptance before the child starts, so a short output cannot close first.
		if err := outputs[len(outputs)-1].listen(); err != nil {
			ClosePipes(outputs)
			return nil, err
		}
	}
	return outputs, nil
}

// AttachPipes is a no-op on Windows, where the child opens named destinations.
func AttachPipes(*exec.Cmd, []Pipe) {}

func (p *Pipe) Destination() string { return p.path }

// Started is a no-op because Windows listeners are armed by NewPipes.
func (p *Pipe) Started() {}

// Reader accepts bytes only from ffmpegPID and transfers the connected handle
// to the returned reader. ctx cancels acceptance; callers own read cancellation.
// The random pipe name is not an authentication boundary.
func (p *Pipe) Reader(ctx context.Context, ffmpegPID int) (io.ReadCloser, error) {
	for {
		if err := p.connect(ctx); err != nil {
			return nil, err
		}
		var peerPID uint32
		if err := windows.GetNamedPipeClientProcessId(p.handle, &peerPID); err != nil {
			if errors.Is(err, windows.ERROR_NOT_FOUND) {
				if resetErr := windows.DisconnectNamedPipe(p.handle); resetErr != nil {
					return nil, fmt.Errorf("reset lost PCM pipe writer: %w", resetErr)
				}
				p.connected = false
				continue
			}
			return nil, fmt.Errorf("identify PCM pipe writer: %w", err)
		}
		if int64(peerPID) == int64(ffmpegPID) {
			file := os.NewFile(uintptr(p.handle), p.path)
			p.handle = 0 // The consumer now owns the connected handle.
			return file, nil
		}
		if err := windows.DisconnectNamedPipe(p.handle); err != nil {
			return nil, fmt.Errorf("reject unexpected PCM pipe writer: %w", err)
		}
		p.connected = false
	}
}

func (p *Pipe) listen() error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return fmt.Errorf("create PCM pipe event: %w", err)
	}
	p.event = event
	p.overlapped = &windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(p.handle, p.overlapped)
	if err == nil || errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		p.connected = true
		p.closeEvent()
		return nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		p.closeEvent()
		return fmt.Errorf("connect PCM pipe: %w", err)
	}
	p.pending = true
	return nil
}

func (p *Pipe) connect(ctx context.Context) error {
	for {
		if p.connected {
			return nil
		}
		if !p.pending {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("wait for PCM pipe writer: %w", err)
			}
			if err := p.listen(); err != nil {
				return err
			}
			continue
		}
		state, waitErr := windows.WaitForSingleObject(p.event, 100)
		if waitErr != nil {
			p.cancelPending()
			return fmt.Errorf("wait for PCM pipe writer: %w", waitErr)
		}
		if state == uint32(windows.WAIT_TIMEOUT) {
			if err := ctx.Err(); err != nil {
				p.cancelPending()
				return fmt.Errorf("wait for PCM pipe writer: %w", err)
			}
			continue
		}
		if state != windows.WAIT_OBJECT_0 {
			p.cancelPending()
			return fmt.Errorf("wait for PCM pipe writer: unexpected state %d", state)
		}
		p.pending = false // A signaled event means the operation ended, including error completion.
		var transferred uint32
		err := windows.GetOverlappedResult(p.handle, p.overlapped, &transferred, false)
		p.closeEvent()
		if err != nil {
			return fmt.Errorf("connect PCM pipe: %w", err)
		}
		p.connected = true
		return nil
	}
}

func (p *Pipe) cancelPending() {
	if p.pending {
		cancelErr := windows.CancelIoEx(p.handle, p.overlapped)
		var transferred uint32
		// ERROR_NOT_FOUND can mean the operation finished; check before waiting on its event.
		if !errors.Is(cancelErr, windows.ERROR_NOT_FOUND) ||
			errors.Is(windows.GetOverlappedResult(p.handle, p.overlapped, &transferred, false), windows.ERROR_IO_INCOMPLETE) {
			_ = windows.GetOverlappedResult(p.handle, p.overlapped, &transferred, true)
		}
		p.pending = false
	}
	p.closeEvent()
}

func (p *Pipe) closeEvent() {
	if p.event != 0 {
		_ = windows.CloseHandle(p.event)
		p.event = 0
	}
	p.overlapped = nil
}

func (p *Pipe) Close() {
	p.cancelPending()
	if p.handle != 0 {
		_ = windows.CloseHandle(p.handle)
		p.handle = 0
	}
}
