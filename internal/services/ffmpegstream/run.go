// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package ffmpegstream delivers outputs from one FFmpeg input traversal to
// independent, bounded consumers over private, child-authenticated pipes.
package ffmpegstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// Output supplies backend-owned output arguments, excluding the destination.
// Consume must read to EOF. An unsuccessful consumer is drained so siblings
// can finish; cancellation closes every pipe and waits for the child.
type Output struct {
	Arguments []string
	Consume   func(io.Reader) error
}

// Result distinguishes consumer failures from a failed shared process.
type Result struct {
	Errors       []error
	ProcessError error
}

// Run attaches all private outputs before starting a caller-configured command.
// cmd.Args already contains input and global options. Diagnostics must be set
// by the caller; no source paths or subprocess output are logged here.
func Run(ctx context.Context, cmd *exec.Cmd, outputs []Output) (Result, error) {
	result := Result{Errors: make([]error, len(outputs))}
	if cmd == nil || len(outputs) == 0 || len(outputs) > 64 {
		return result, errors.New("invalid FFmpeg stream batch")
	}
	for _, output := range outputs {
		if output.Consume == nil || len(output.Arguments) == 0 {
			return result, errors.New("FFmpeg output requires arguments and a consumer")
		}
	}
	pipes, err := NewPipes(len(outputs))
	if err != nil {
		return result, err
	}
	defer ClosePipes(pipes)
	for i, output := range outputs {
		cmd.Args = append(cmd.Args, output.Arguments...)
		cmd.Args = append(cmd.Args, pipes[i].Destination())
	}
	AttachPipes(cmd, pipes)
	cmd.Stdout = io.Discard
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("start shared FFmpeg process: %w", err)
	}
	stopProcess := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
	defer stopProcess()
	for i := range pipes {
		pipes[i].Started()
	}
	acceptCtx, cancelAccept := context.WithCancel(ctx)
	defer cancelAccept()
	type completion struct {
		index int
		err   error
	}
	done := make(chan completion, len(outputs))
	for i, output := range outputs {
		go func() {
			connection, err := pipes[i].Reader(acceptCtx, cmd.Process.Pid)
			if err == nil {
				stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
				err = output.Consume(connection)
				// A consumer's limit or format failure must not discard usable
				// metadata, audio or pictures from another output.
				_, drainErr := io.Copy(io.Discard, connection)
				err = errors.Join(err, drainErr, connection.Close())
				stop()
			}
			done <- completion{i, err}
		}()
	}
	process := make(chan error, 1)
	go func() { process <- cmd.Wait() }()
	remaining, waited := len(outputs), false
	for remaining > 0 || !waited {
		select {
		case completed := <-done:
			remaining--
			result.Errors[completed.index] = completed.err
		case result.ProcessError = <-process:
			waited = true
			cancelAccept()
		}
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("shared FFmpeg process canceled: %w", ctx.Err())
	}
	return result, nil
}

// ClosePipes releases listeners and any pipe ends not transferred to consumers.
func ClosePipes(pipes []Pipe) {
	for i := range pipes {
		pipes[i].Close()
	}
}
