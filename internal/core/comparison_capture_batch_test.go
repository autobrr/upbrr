// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"reflect"
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
	for _, tc := range []struct {
		name       string
		renderErr  error
		publishErr error
		want       []float64
		fatal      bool
	}{
		{name: "completed captures", want: []float64{0, 1}},
		{
			name:      "render failure",
			renderErr: failure,
			want:      []float64{0},
			fatal:     true,
		},
		{
			name:      "mixed render failure",
			renderErr: errors.Join(context.Canceled, failure),
			want:      []float64{0},
			fatal:     true,
		},
		{
			name:       "interrupted reinspection",
			publishErr: &comparisonPublicationInterruptedError{context.Canceled},
			want:       []float64{0},
		},
		{
			name:       "witness conflict",
			publishErr: comparisonWitnessConflict(),
			want:       []float64{0},
			fatal:      true,
		},
		{
			name:       "unclassified publication failure",
			publishErr: context.Canceled,
			want:       []float64{0},
			fatal:      true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
						return screenshots.ComparisonFrame{}, tc.renderErr
					}
					return screenshots.ComparisonFrame{}, nil
				}}
				finished := make(chan error, 1)
				var published []float64
				go func() {
					finished <- captureComparisonBatch(ctx, renderer, jobs, 2, func(comparisonCaptureJob, api.WorkflowProgressUpdate) {}, func(job comparisonCaptureJob, _ screenshots.ComparisonFrame, notify bool) error {
						if notify {
							t.Error("drained capture emitted progress")
						}
						if job.request.TimestampSeconds == 1 && tc.publishErr != nil {
							return tc.publishErr
						}
						published = append(published, job.request.TimestampSeconds)
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
				if err == nil || comparisonCancellationOnly(err) == tc.fatal || !reflect.DeepEqual(published, tc.want) ||
					tc.fatal && !errors.Is(err, tc.renderErr) && !errors.Is(err, tc.publishErr) {
					t.Fatalf("published %v, expected %v: %v", published, tc.want, err)
				}
			})
		})
	}
}
