// Copyright (c) 2026, Audionut and the autobrr contributors.
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
	"os/exec"
	"slices"
	"sync"
	"time"
)

type comparisonSparseSink struct {
	url                         string
	server                      *http.Server
	done                        chan struct{}
	outputDone                  chan struct{}
	mu                          sync.Mutex
	handlers                    sync.WaitGroup
	closing, accepted, complete bool
}

func openComparisonSparseSink(ctx context.Context, stream *comparisonRasterStream, cancel context.CancelCauseFunc) (*comparisonSparseSink, error) {
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open private comparison output: %w", err)
	}
	endpoint := "/" + rand.Text()
	sink := &comparisonSparseSink{
		url:        "http://" + listener.Addr().String() + endpoint,
		done:       make(chan struct{}),
		outputDone: make(chan struct{}),
	}
	sink.server = &http.Server{
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sink.mu.Lock()
			if sink.closing || r.URL.Path != endpoint || r.URL.RawQuery != "" || r.Method != http.MethodPost {
				sink.mu.Unlock()
				http.NotFound(w, r)
				return
			}
			if sink.accepted {
				sink.mu.Unlock()
				cancel(errors.New("comparison analysis received duplicate sparse output"))
				http.Error(w, "Output already received", http.StatusConflict)
				return
			}
			sink.accepted = true
			sink.handlers.Add(1)
			sink.mu.Unlock()
			defer sink.handlers.Done()
			defer close(sink.outputDone)
			defer r.Body.Close()
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				cancel(errors.New("comparison analysis received an unexpected output format"))
				http.Error(w, "Unexpected format", http.StatusUnsupportedMediaType)
				return
			}
			limit := int64(1024*stream.height*3/2) * int64(stream.maxSparse)
			reader := http.MaxBytesReader(w, r.Body, limit)
			if _, err := io.Copy(stream.sparse, reader); err != nil {
				cancel(&comparisonOutputReadError{cause: err})
				return
			}
			sink.mu.Lock()
			sink.complete = true
			sink.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() {
		defer close(sink.done)
		if err := sink.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel(fmt.Errorf("serve private comparison output: %w", err))
		}
	}()
	return sink, nil
}

func (s *comparisonSparseSink) stop() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	_ = s.server.Close()
}

func (s *comparisonSparseSink) close() {
	s.stop()
	<-s.done
	s.handlers.Wait()
}

// The finite runner owns both pipes, the private listener, its active body and
// the single accumulator. Cancellation preserves its first cause and joins all
// of them before any observations are returned.
func (r finiteComparisonRunner) runAnalysis(
	ctx context.Context,
	name string,
	args []string,
	dir string,
	stream *comparisonRasterStream,
) (CommandResult, error) {
	workCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stream.start(workCtx, cancel)
	sink, err := openComparisonSparseSink(workCtx, stream, cancel)
	if err != nil {
		cancel(err)
		close(stream.facts)
		<-stream.done
		return CommandResult{}, err
	}
	// Join the cancellation callback as well as the listener and handlers.
	stopped := make(chan struct{})
	stop := context.AfterFunc(workCtx, func() { defer close(stopped); sink.stop() })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	command := slices.Clone(args)
	outputs := 0
	for i, arg := range command {
		if arg == comparisonSparseOutput {
			command[i] = sink.url
			outputs++
		}
	}
	if outputs != 1 {
		cancel(errors.New("comparison analysis output command is incomplete"))
	}
	inputURL, _ := ctx.Value(comparisonInputURLKey{}).(string)
	diagnostic := &comparisonDiagnosticStream{
		stream:         stream,
		retain:         r.retain,
		inputURL:       inputURL,
		packets:        packetTraceFor(args),
		outputURL:      sink.url,
		hardwareDecode: slices.Contains(args, "-hwaccel"),
	}
	diagnostic.firstNative, _ = ctx.Value(comparisonFirstNativeKey{}).(func())
	diagnostic.cancel = func() { cancel(diagnostic.err) }
	// #nosec G204 G702 -- Resolved media decoder, structured arguments, no shell.
	cmd := exec.CommandContext(workCtx, name, append([]string{"-nostdin", "-nostats"}, command...)...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	stdout, outErr := cmd.StdoutPipe()
	stderr, errErr := cmd.StderrPipe()
	if err := errors.Join(outErr, errErr); err != nil {
		cancel(err)
	}
	var runErr error
	started := false
	if workCtx.Err() == nil {
		runErr = cmd.Start()
		started = runErr == nil
		if started {
			recordComparisonProcessStart(ctx)
		}
	}
	if runErr != nil {
		cancel(comparisonProcessFailure(runErr, 1))
	}
	var readers sync.WaitGroup
	if started {
		pipesClosed := make(chan struct{})
		closePipes := context.AfterFunc(workCtx, func() { defer close(pipesClosed); _ = stdout.Close(); _ = stderr.Close() })
		readers.Go(func() {
			if _, err := io.Copy(stream.motion, stdout); err != nil {
				cancel(err)
			}
		})
		readers.Go(func() {
			var writer io.Writer = diagnostic
			if progress, ok := ctx.Value(comparisonSampleProgressKey{}).(*comparisonSampleProgress); ok {
				writer = io.MultiWriter(diagnostic, progress)
			}
			_, err := io.Copy(writer, stderr)
			if err == nil && workCtx.Err() == nil {
				_, err = diagnostic.result()
			}
			if err != nil {
				cancel(err)
			}
			close(stream.motionRecords)
			close(stream.sparseRecords)
		})
		readers.Wait()
		runErr = cmd.Wait()
		if !closePipes() {
			<-pipesClosed
		}
	} else {
		if stdout != nil {
			_ = stdout.Close()
		}
		if stderr != nil {
			_ = stderr.Close()
		}
	}
	sink.mu.Lock()
	accepted := sink.accepted
	sink.mu.Unlock()
	if accepted {
		<-sink.outputDone
	}
	sink.close()
	close(stream.facts)
	<-stream.done
	if cause := stream.failure(context.Cause(workCtx), diagnostic.err); cause != nil {
		return CommandResult{}, fmt.Errorf("comparison analysis process interrupted: %w", cause)
	}
	if diagnostic.err != nil {
		return CommandResult{}, diagnostic.err
	}
	data, parseErr := diagnostic.result()
	if parseErr != nil {
		return CommandResult{}, parseErr
	}
	result := CommandResult{bounds: diagnostic.bounds, Stderr: data}
	if runErr != nil {
		result.ExitCode = 1
		return result, comparisonProcessFailure(runErr, 1)
	}
	if !sink.accepted || !sink.complete {
		return CommandResult{}, errors.New("comparison analysis sparse output is missing")
	}
	if err := stream.validate(); err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

type comparisonOutputReadError struct{ cause error }

func (e *comparisonOutputReadError) Error() string {
	return "comparison analysis sparse output interrupted"
}
func (e *comparisonOutputReadError) Unwrap() error { return e.cause }
