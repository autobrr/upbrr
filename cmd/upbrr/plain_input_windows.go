// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

type cliPlainInput struct {
	ctx  context.Context
	file *os.File
}

// newCLIPlainInput ties a borrowed terminal reader to the invocation context.
// Callers must run the returned cleanup and retain ownership of the terminal file.
func newCLIPlainInput(ctx context.Context, file *os.File, _ context.CancelCauseFunc) (io.Reader, func() error, error) {
	return &cliPlainInput{ctx: ctx, file: file}, func() error { return nil }, nil
}

// ReadConsole is synchronous. Cancel only the OS thread owned by this read;
// leave the caller's console and its cooked line/echo settings intact.
func (input *cliPlainInput) beginRead() (func(), error) {
	if err := input.ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin terminal read: %w", err)
	}
	runtime.LockOSThread()
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("own console read thread: %w", err)
	}
	finished, joined := make(chan struct{}), make(chan struct{})
	cancelIO := windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")
	stop := context.AfterFunc(input.ctx, func() {
		defer close(joined)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			_, _, _ = cancelIO.Call(uintptr(thread))
			select {
			case <-finished:
				return
			case <-ticker.C:
			}
		}
	})
	return func() {
		close(finished)
		if !stop() {
			<-joined
		}
		_ = windows.CloseHandle(thread)
		runtime.UnlockOSThread()
	}, nil
}

func (input *cliPlainInput) Read(data []byte) (int, error) {
	finish, err := input.beginRead()
	if err != nil {
		return 0, err
	}
	defer finish()
	n, err := input.file.Read(data)
	if input.ctx.Err() != nil {
		return n, fmt.Errorf("cancel terminal read: %w", input.ctx.Err())
	}
	if err != nil {
		return n, fmt.Errorf("read terminal input: %w", err)
	}
	return n, nil
}

func (input *cliPlainInput) readPassword(_ io.Writer) (string, error) {
	finish, err := input.beginRead()
	if err != nil {
		return "", err
	}
	defer finish()
	fd, _ := terminalFileDescriptor(input.file)
	raw, err := term.ReadPassword(fd)
	if input.ctx.Err() != nil {
		return "", fmt.Errorf("cancel terminal password: %w", input.ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("read terminal password: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}
