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

	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonInputURLKey struct{}

// comparisonFileInput admits every read, including probes, seeks and preroll,
// before serving it to the decoder. ServeContent owns HTTP range semantics;
// no encoded payload is staged, and only this attempt's approved file is served.
type comparisonFileInput struct {
	file     *os.File
	path     string
	stat     os.FileInfo
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

func (s *Service) openComparisonFileInput(
	ctx context.Context, path string, budget *ComparisonDecodeBudget, expected os.FileInfo,
) (context.Context, *comparisonFileInput, error) {
	if budget == nil {
		budget = &ComparisonDecodeBudget{RemainingEncodedBytes: 8 << 30}
	}
	input := &comparisonFileInput{url: path, budget: budget}
	if _, native := s.runner.(commandRunner); !native {
		return ctx, input, nil
	}
	file, stat, err := pathing.OpenRegularFile(path)
	if errors.Is(err, pathing.ErrNotRegular) {
		return ctx, nil, comparisonFileInputChanged()
	}
	if err != nil {
		return ctx, nil, fmt.Errorf("open comparison file input: %w", err)
	}
	if expected != nil && !pathing.SameFileVersion(expected, stat) {
		_ = file.Close()
		return ctx, nil, comparisonFileInputChanged()
	}
	input.file, input.path, input.stat = file, path, stat
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		_ = file.Close()
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
			if err := input.verify(); err != nil {
				input.fail(err)
				http.Error(w, "Input unavailable", http.StatusGone)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, "input", stat.ModTime(), &comparisonBudgetReader{
				file:  io.NewSectionReader(file, 0, stat.Size()),
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
	if err := input.verify(); err != nil {
		input.fail(err)
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	return input.err
}

func (input *comparisonFileInput) verify() error {
	if input.file == nil {
		return nil
	}
	atPath, err := pathing.RegularFileInfo(input.path)
	if err != nil || !pathing.SameFileVersion(input.stat, atPath) {
		return comparisonFileInputChanged()
	}
	return nil
}

func comparisonFileInputChanged() error {
	return api.NewOperationError(api.OperationFailure{
		Code:             api.OperationFailureInvalidSource,
		Operation:        api.OperationKindMedia,
		Recovery:         api.OperationRecoveryEditInput,
		ComparisonReason: "source_changed",
		Message:          "The video file changed while it was being read. Select it again before comparing.",
	}, api.ErrActiveInputChanged)
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
	_ = input.file.Close()
}

type comparisonBudgetReader struct {
	file  *io.SectionReader
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
