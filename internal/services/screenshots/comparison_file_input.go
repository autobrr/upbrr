// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type comparisonInputURLKey struct{}

// comparisonFileInput admits every read, including probes, seeks and preroll,
// before serving it to the decoder. ServeContent owns HTTP range semantics;
// no encoded payload is staged, and only this attempt's approved file is served.
type comparisonFileInput struct {
	budget   *ComparisonDecodeBudget
	url      string
	server   *http.Server
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	handlers sync.WaitGroup
	closing  bool
	err      error
}

func (s *Service) openComparisonFileInput(ctx context.Context, path string, budget *ComparisonDecodeBudget) (context.Context, *comparisonFileInput, error) {
	if budget == nil {
		budget = &ComparisonDecodeBudget{RemainingEncodedBytes: 8 << 30}
	}
	input := &comparisonFileInput{url: path, budget: budget}
	if _, native := s.runner.(commandRunner); !native {
		return ctx, input, nil
	}
	stat, err := os.Stat(path)
	if err != nil {
		return ctx, nil, fmt.Errorf("inspect comparison file input: %w", err)
	}
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return ctx, nil, fmt.Errorf("open bounded comparison input: %w", err)
	}
	endpoint := "/" + rand.Text()
	input.url = "http://" + listener.Addr().String() + endpoint
	workCtx, cancel := context.WithCancel(ctx)
	input.cancel = cancel
	input.done = make(chan struct{})
	input.server = &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			input.mu.Lock()
			if input.closing || r.URL.Path != endpoint || r.Method != http.MethodGet && r.Method != http.MethodHead {
				input.mu.Unlock()
				http.NotFound(w, r)
				return
			}
			input.handlers.Add(1)
			input.mu.Unlock()
			defer input.handlers.Done()
			file, err := os.Open(path)
			if err != nil {
				input.fail(fmt.Errorf("open comparison file input: %w", err))
				http.Error(w, "Input unavailable", http.StatusGone)
				return
			}
			defer file.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, "input", stat.ModTime(), &comparisonBudgetReader{
				file:  file,
				input: input,
			})
		}),
	}
	go func() {
		defer close(input.done)
		if err := input.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			input.fail(fmt.Errorf("serve comparison file input: %w", err))
		}
	}()
	return context.WithValue(workCtx, comparisonInputURLKey{}, input.url), input, nil
}

func (input *comparisonFileInput) fail(err error) {
	input.mu.Lock()
	if input.err == nil {
		input.err = err
	}
	input.mu.Unlock()
	input.cancel()
}

func (input *comparisonFileInput) failure() error {
	input.mu.Lock()
	defer input.mu.Unlock()
	return input.err
}

func (input *comparisonFileInput) close() {
	if input.server == nil {
		return
	}
	input.mu.Lock()
	input.closing = true
	input.mu.Unlock()
	input.cancel()
	_ = input.server.Close()
	<-input.done
	input.handlers.Wait()
}

type comparisonBudgetReader struct {
	file  *os.File
	input *comparisonFileInput
}

func (r *comparisonBudgetReader) Read(p []byte) (int, error) {
	// Admission reserves the complete read buffer before any source IO. A short
	// final read can conservatively charge unused bytes; read counts never
	// understate the approved decoder input or assume a constant bitrate.
	r.input.mu.Lock()
	err := r.input.budget.admit(int64(len(p)))
	r.input.mu.Unlock()
	if err != nil {
		r.input.fail(err)
		return 0, err
	}
	n, err := r.file.Read(p)
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		wrapped := fmt.Errorf("read comparison file input: %w", err)
		r.input.fail(wrapped)
		return n, wrapped
	}
	return n, nil
}

func (r *comparisonBudgetReader) Seek(offset int64, whence int) (int64, error) {
	position, err := r.file.Seek(offset, whence)
	if err != nil {
		return position, fmt.Errorf("seek comparison file input: %w", err)
	}
	return position, nil
}
