// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

type cliPlainInput struct {
	cancelreader.CancelReader
	file   *os.File
	cancel context.CancelCauseFunc
}

// newCLIPlainInput ties a borrowed terminal reader to the invocation context.
// Callers must run the returned cleanup and retain ownership of the terminal file.
func newCLIPlainInput(ctx context.Context, file *os.File, cancel context.CancelCauseFunc) (io.Reader, func() error, error) {
	reader, err := cancelreader.NewReader(file)
	if err != nil {
		return nil, nil, fmt.Errorf("own terminal reader: %w", err)
	}
	joined := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { reader.Cancel(); close(joined) })
	input := &cliPlainInput{
		CancelReader: reader,
		file:         file,
		cancel:       cancel,
	}
	return input, func() error {
		if !stop() {
			<-joined
		}
		if err := reader.Close(); err != nil {
			return fmt.Errorf("close terminal reader: %w", err)
		}
		return nil
	}, nil
}

func (input *cliPlainInput) readPassword(output io.Writer) (string, error) {
	fd, _ := terminalFileDescriptor(input.file)
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("mask terminal password: %w", err)
	}
	defer func() { _ = term.Restore(fd, state) }()
	terminal := term.NewTerminal(cliPasswordIO{
		Reader: input.CancelReader,
		Writer: output,
		cancel: input.cancel,
	}, "")
	value, err := terminal.ReadPassword("")
	if err != nil {
		return "", fmt.Errorf("read terminal password: %w", err)
	}
	return strings.TrimSpace(value), nil
}
