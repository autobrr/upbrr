// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonBatchRenderer struct {
	comparisonFrameRenderer
	capture func(context.Context, screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error)
}

func (r comparisonBatchRenderer) ComparisonCapture(ctx context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
	return r.capture(ctx, request)
}

func TestComparisonCaptureBatchPreservesFailureAndJoinsSiblings(t *testing.T) {
	started, joined := make(chan struct{}), make(chan struct{})
	failure := errors.New("synthetic source integrity failure")
	var calls atomic.Int32
	renderer := comparisonBatchRenderer{capture: func(ctx context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
		calls.Add(1)
		if request.TimestampSeconds == 0 {
			<-started
			return screenshots.ComparisonFrame{}, failure
		}
		close(started)
		<-ctx.Done()
		defer close(joined)
		return screenshots.ComparisonFrame{}, ctx.Err()
	}}
	jobs := make([]comparisonCaptureJob, 3)
	for i := range jobs {
		jobs[i].request.TimestampSeconds = float64(i)
	}
	err := captureComparisonBatch(t.Context(), renderer, jobs, 2, func(comparisonCaptureJob, api.WorkflowProgressUpdate) {}, func(comparisonCaptureJob, screenshots.ComparisonFrame) error {
		t.Fatal("a failed render was published")
		return nil
	})
	if !errors.Is(err, failure) || calls.Load() != 2 {
		t.Fatalf("failure=%v calls=%d", err, calls.Load())
	}
	select {
	case <-joined:
	default:
		t.Fatal("capture returned before its sibling exited")
	}
	t.Run("cancellation cannot hide a drained integrity failure", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			renderer := comparisonBatchRenderer{capture: func(context.Context, screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
				close(started)
				<-release
				return screenshots.ComparisonFrame{}, failure
			}}
			finished := make(chan error, 1)
			go func() {
				finished <- captureComparisonBatch(ctx, renderer, jobs[:1], 1, func(comparisonCaptureJob, api.WorkflowProgressUpdate) {}, func(comparisonCaptureJob, screenshots.ComparisonFrame) error {
					t.Error("an integrity failure became a published capture")
					return nil
				})
			}()
			<-started
			cancel()
			// The coordinator has consumed cancellation and is waiting for the
			// renderer to exit. Its terminal error must remain fatal afterward.
			synctest.Wait()
			close(release)
			if err := <-finished; !errors.Is(err, failure) || errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation hid the worker's integrity failure: %v", err)
			}
		})
	})
}
