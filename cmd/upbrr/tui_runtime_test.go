// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliSynchronizedOutput struct {
	mu   sync.Mutex
	text strings.Builder
}

func (w *cliSynchronizedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.text.Write(data)
	return len(data), nil
}
func (w *cliSynchronizedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.String()
}

func TestTerminalRuntimeRestoresBeforeDurableSummaryAndKeepsDriverOnce(t *testing.T) {
	var output cliSynchronizedOutput
	var calls atomic.Int32
	err := runCLIPresentation(t.Context(), cliIO{
		in:     strings.NewReader(""),
		out:    &output,
		errOut: io.Discard,
	}, cliTerminalCapabilities{
		Width:  120,
		Height: 40,
		Input:  true,
		Output: true,
	}, true, false, true, nil, []string{"example.mkv"}, false, false,
		func(_ context.Context, _, _ []string, streams cliIO) error {
			calls.Add(1)
			streams.presenter.Publish(cliView{
				Source: "example.mkv",
				Stage:  "Completed",
				Lanes: []cliLaneView{{
					ID:     "ALPHA",
					State:  "Submission: completed / Client: failed",
					Reason: "client_failure",
				}},
			})
			return nil
		})
	if err != nil || calls.Load() != 1 {
		t.Fatalf("runtime result = %v calls=%d", err, calls.Load())
	}
	text := output.String()
	enter, exit := strings.Index(text, "\x1b[?1049h"), strings.LastIndex(text, "\x1b[?1049l")
	summary := strings.LastIndex(text, "ALPHA: Submission: completed / Client: failed")
	if enter < 0 || exit <= enter || summary <= exit {
		t.Fatalf("alternate screen/summary ordering failed: enter=%d exit=%d summary=%d", enter, exit, summary)
	}
	if strings.Count(text[exit:], "ALPHA: ") != 1 {
		t.Fatal("durable summary repeated")
	}
}

func TestPresentationCancellationJoinsPromptAndDriver(t *testing.T) {
	var output cliSynchronizedOutput
	ctx, cancel := context.WithCancel(t.Context())
	joined := make(chan struct{})
	var callers sync.WaitGroup
	callers.Go(func() {
		err := runCLIPresentation(ctx, cliIO{
			in:       strings.NewReader(""),
			out:      &output,
			errOut:   io.Discard,
			keepOpen: true,
		}, cliTerminalCapabilities{Width: 100, Height: 30}, true, false, true, nil, nil, false, false,
			func(runCtx context.Context, _, _ []string, streams cliIO) error {
				cancel()
				_, err := streams.presenter.Ask(runCtx, cliPrompt{
					ID:       1,
					Kind:     cliPromptConfirm,
					Question: "Do not approve",
				})
				close(joined)
				return fmt.Errorf("synthetic prompt: %w", err)
			})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("parent cancellation = %v", err)
		}
	})
	callers.Wait()
	select {
	case <-joined:
	default:
		t.Fatal("runtime abandoned driver")
	}
	if !strings.Contains(output.String(), "\x1b[?1049l") {
		t.Fatal("cancel did not restore alternate screen")
	}
}

type cliStartupGateWriter struct {
	cliSynchronizedOutput
	fail atomic.Bool
}

func (w *cliStartupGateWriter) Write(data []byte) (int, error) {
	if w.fail.Load() {
		return 0, errors.New("synthetic startup timeout")
	}
	return w.cliSynchronizedOutput.Write(data)
}

type cliStartupGateInput struct {
	io.Reader
	io.Writer
	fd               uintptr
	entered, release chan struct{}
	once             sync.Once
}

func (r *cliStartupGateInput) Fd() uintptr {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.fd
}
func (*cliStartupGateInput) Close() error { return nil }

func TestStartupCancellationCompletesBeforeReadinessWithoutDispatch(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(alreadyCanceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			file, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			input := &cliStartupGateInput{
				Reader:  strings.NewReader(""),
				Writer:  io.Discard,
				fd:      file.Fd(),
				entered: make(chan struct{}),
				release: make(chan struct{}),
			}
			output := &cliStartupGateWriter{}
			var calls atomic.Int32
			done := make(chan error, 1)
			go func() {
				done <- runCLIPresentation(ctx, cliIO{
					in:     input,
					out:    output,
					errOut: io.Discard,
				},
					cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, nil, false, false,
					func(context.Context, []string, []string, cliIO) error { calls.Add(1); return nil })
			}()
			select {
			case <-input.entered:
			case <-time.After(5 * time.Second):
				close(input.release)
				t.Fatal("terminal initialization did not reach its gate")
			}
			cancel()
			close(input.release)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
					t.Fatalf("startup cancellation = %v, dispatches=%d", err, calls.Load())
				}
			case <-time.After(5 * time.Second):
				// An output failure forces the test runtime to join even if the
				// completion regression returns. No process-exit seam is used.
				output.fail.Store(true)
				<-done
				t.Fatal("canceled startup left the terminal program waiting")
			}
			if !strings.Contains(output.String(), "\x1b[?1049l") {
				t.Fatal("startup cancellation did not restore the alternate screen")
			}
		})
	}
}

type cliFailingFrames struct{}

func (cliFailingFrames) Write([]byte) (int, error) { return 0, errors.New("synthetic output failure") }

func TestUIStartupFailureNeverDispatchesForcedWork(t *testing.T) {
	var calls atomic.Int32
	err := runCLIPresentation(t.Context(), cliIO{
		in:     strings.NewReader(""),
		out:    cliFailingFrames{},
		errOut: io.Discard,
	}, cliTerminalCapabilities{Width: 120, Height: 40}, true, true, true, []string{"--ui=tui"}, nil, false, false,
		func(context.Context, []string, []string, cliIO) error { calls.Add(1); return nil })
	if err == nil || calls.Load() != 0 {
		t.Fatalf("failed startup dispatched work: err=%v calls=%d", err, calls.Load())
	}
}

func TestAutomaticStartupFallbackPreservesInvocationInterrupt(t *testing.T) {
	invocation, cancel := context.WithCancelCause(t.Context())
	cancel(errCLIUserInterrupt)
	if t.Context().Err() != nil || cliStartupFallbackAllowed(t.Context(), invocation, false) {
		t.Fatal("invocation-owned interrupt permitted fallback with an uncanceled parent")
	}
	for _, forced := range []bool{false, true} {
		var calls atomic.Int32
		err := runCLIPresentation(t.Context(), cliIO{
			in:     strings.NewReader(""),
			out:    cliFailingFrames{},
			errOut: io.Discard,
		}, cliTerminalCapabilities{Width: 120, Height: 40}, true, true, forced, nil, nil, false, false,
			func(context.Context, []string, []string, cliIO) error { calls.Add(1); return nil })
		if forced {
			if err == nil || calls.Load() != 0 {
				t.Fatal("forced startup failure dispatched work")
			}
		} else if err != nil || calls.Load() != 1 {
			t.Fatalf("ordinary automatic startup failure did not fall back once: %v calls=%d", err, calls.Load())
		}
	}
	parent, cancelParent := context.WithCancel(t.Context())
	cancelParent()
	if cliStartupFallbackAllowed(parent, parent, false) {
		t.Fatal("parent cancellation permitted startup fallback")
	}
}

type cliRetryLifecycle struct{ calls, closed int }

func (f *cliRetryLifecycle) ShutdownWorkflowCoordinator(context.Context) error {
	f.calls++
	if f.calls == 1 {
		return errors.New("synthetic timeout")
	}
	return nil
}
func (f *cliRetryLifecycle) Close() error { f.closed++; return nil }
func TestWorkflowCleanupRetryClosesOnlyAfterConfirmedTermination(t *testing.T) {
	fake := &cliRetryLifecycle{}
	if err := closeCLIWorkflowCore(t.Context(), fake); err != nil || fake.calls != 2 || fake.closed != 1 {
		t.Fatalf("cleanup=%v calls=%d closes=%d", err, fake.calls, fake.closed)
	}
}

func TestOperationEpochRejectsOldProgressAndCopiesAuthority(t *testing.T) {
	b := newCLITUIBridge()
	lanes := []cliLaneView{{ID: "ALPHA", State: "Unchecked"}}
	b.Publish(cliView{
		Item:       1,
		WorkflowID: "workflow",
		Revision:   3,
		Lanes:      lanes,
	})
	lanes[0].State = "injected"
	b.epoch = 2
	b.Progress(cliTelemetry{
		Item:      1,
		Epoch:     1,
		Phase:     "obsolete",
		Completed: 100,
		Total:     100,
	})
	if b.progress != nil || b.latest.Lanes[0].State != "Unchecked" {
		t.Fatal("stale update or mutable ownership crossed bridge")
	}
	b.Progress(cliTelemetry{
		Item:      1,
		Epoch:     2,
		Phase:     "current",
		Completed: 2,
		Total:     3,
	})
	b.Publish(cliView{
		Item:       1,
		WorkflowID: "workflow",
		Revision:   2,
		Lanes:      []cliLaneView{{ID: "STALE"}},
	})
	if b.progress == nil || b.progress.Completed != 2 || b.latest.Revision != 3 {
		t.Fatal("current authority lost")
	}
	writer := &cliPresentationWriter{
		Writer:    io.Discard,
		presenter: b,
		ctx:       t.Context(),
		terminal:  true,
	}
	session := &cliWorkflowSession{streams: cliIO{out: writer, presenter: b}}
	ctx := session.presentationProgressContext(context.WithValue(t.Context(), cliItemContextKey{}, uint64(1)))
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		ItemOnly:  true,
		ItemID:    "audio-1",
		Phase:     "audio_analysis_decode",
		Completed: 50,
		Total:     50,
	})
	if b.progress != nil || len(b.telemetry) != 1 || b.telemetry[0].Lane != "audio-1" || b.telemetry[0].Completed != 50 {
		t.Fatal("item telemetry was lost or advanced the operation aggregate")
	}
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{Completed: 2, Total: 3})
	if b.progress == nil || b.progress.Completed != 2 {
		t.Fatal("operation progress did not reach the bridge")
	}
}

func TestQueueSnapshotsRetainResultsAndExcludedTrackerRows(t *testing.T) {
	bridge := newCLITUIBridge()
	writer := &cliPresentationWriter{
		Writer:    io.Discard,
		presenter: bridge,
		ctx:       t.Context(),
		terminal:  true,
	}
	streams := cliIO{out: writer, presenter: bridge}
	ctx := withCLIItemPresentation(t.Context(), streams, "Example.One.mkv", 2)
	session := &cliWorkflowSession{
		streams:       streams,
		uploadRequest: api.Request{Trackers: []string{"ALPHA", "BETA"}},
		current:       releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{ID: "workflow", Revision: 1}, Selection: &api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA"}}},
	}
	session.publishPresentation(ctx)
	if len(bridge.latest.Lanes) != 2 || bridge.latest.Lanes[1].State != "Skipped" {
		t.Fatal("excluded tracker disappeared or acquired valid status")
	}
	publishCLIItemResult(streams, errors.New("synthetic failure"))
	withCLIItemPresentation(ctx, streams, "Example.Two.mkv", 2)
	if bridge.latest.Item != 2 || bridge.latest.ItemsTotal != 2 || len(bridge.results) != 1 || !strings.Contains(bridge.results[0], "failed or interrupted") || bridge.latest.Result != "" {
		t.Fatal("queue item authority or retained results lost")
	}
}
