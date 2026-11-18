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
	err := captureComparisonBatch(t.Context(), renderer, jobs, 2, func(comparisonCaptureJob, api.WorkflowProgressUpdate) {}, func(comparisonCaptureJob, screenshots.ComparisonFrame, bool) error {
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
	for _, terminal := range []error{nil, failure, errors.Join(context.Canceled, failure)} {
		t.Run("drain", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				started := make(chan struct{}, 2)
				release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
				renderer := comparisonBatchRenderer{capture: func(_ context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
					index := int(request.TimestampSeconds)
					started <- struct{}{}
					<-release[index]
					if index == 1 {
						return screenshots.ComparisonFrame{}, terminal
					}
					return screenshots.ComparisonFrame{}, nil
				}}
				finished := make(chan error, 1)
				published := 0
				go func() {
					finished <- captureComparisonBatch(ctx, renderer, jobs, 2, func(comparisonCaptureJob, api.WorkflowProgressUpdate) {}, func(comparisonCaptureJob, screenshots.ComparisonFrame, bool) error {
						published++
						return nil
					})
				}()
				<-started
				<-started
				cancel()
				synctest.Wait()
				close(release[0])
				synctest.Wait()
				close(release[1])
				err := <-finished
				if terminal == nil {
					if !comparisonCancellationOnly(err) || published != 2 {
						t.Fatalf("cancelled batch published %d successes: %v", published, err)
					}
				} else if !errors.Is(err, failure) || comparisonCancellationOnly(err) || published != 1 {
					t.Fatalf("fatal batch published %d successes: %v", published, err)
				}
			})
		})
	}
}
