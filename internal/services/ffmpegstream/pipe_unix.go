// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !windows

package ffmpegstream

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Pipe owns one child's output stream. Use NewPipes to initialize it.
type Pipe struct {
	read  *os.File
	write *os.File
	index int
}

// NewPipes creates count output pipes; the caller must release them with ClosePipes.
func NewPipes(count int) ([]Pipe, error) {
	outputs := make([]Pipe, 0, count)
	for index := range count {
		read, write, err := os.Pipe()
		if err != nil {
			ClosePipes(outputs)
			return nil, fmt.Errorf("create PCM pipe: %w", err)
		}
		outputs = append(outputs, Pipe{
			read:  read,
			write: write,
			index: index,
		})
	}
	return outputs, nil
}

// AttachPipes installs write ends before Start. cmd must have no existing ExtraFiles.
func AttachPipes(cmd *exec.Cmd, outputs []Pipe) {
	// ExtraFiles gives the child private write ends at descriptors 3, 4, and so on.
	for index := range outputs {
		cmd.ExtraFiles = append(cmd.ExtraFiles, outputs[index].write)
	}
}

func (p *Pipe) Destination() string { return fmt.Sprintf("pipe:%d", p.index+3) }

// Started closes the parent's write end after the child inherits it.
func (p *Pipe) Started() { _ = p.write.Close() }

// Reader returns the inherited pipe's read end; callers own read cancellation.
func (p *Pipe) Reader(context.Context, int) (io.ReadCloser, error) { return p.read, nil }

func (p *Pipe) Close() {
	_ = p.read.Close()
	_ = p.write.Close()
}
