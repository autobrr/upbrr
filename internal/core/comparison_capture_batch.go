// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"sync"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonCaptureJob struct {
	row, cell int
	request   screenshots.ComparisonCaptureRequest
	rendering api.WorkflowFingerprint
}

type comparisonCaptureEvent struct {
	job    comparisonCaptureJob
	frame  screenshots.ComparisonFrame
	update *api.WorkflowProgressUpdate
	done   chan struct{}
	err    error
}

// One input owns one worker batch. Only this coordinator invokes publication or
// mutable progress, and every worker is joined before returning to the caller.
func captureComparisonBatch(ctx context.Context, renderer comparisonFrameRenderer, jobs []comparisonCaptureJob, concurrency int,
	progress func(comparisonCaptureJob, api.WorkflowProgressUpdate), publish func(comparisonCaptureJob, screenshots.ComparisonFrame, bool) error,
) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan comparisonCaptureJob)
	workClosed := false
	events := make(chan comparisonCaptureEvent, 1)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Go(func() {
			for job := range work {
				if workCtx.Err() != nil {
					return
				}
				jobCtx := api.WithWorkflowProgressReporter(workCtx, func(update api.WorkflowProgressUpdate) {
					select {
					case events <- comparisonCaptureEvent{job: job, update: &update}:
					case <-workCtx.Done():
					}
				})
				api.EmitWorkflowProgress(jobCtx, api.WorkflowProgressUpdate{Completed: 0, Total: 1000})
				frame, err := renderer.ComparisonCapture(jobCtx, job.request)
				// Always hand the terminal result back. The coordinator drains
				// even after cancellation; a worker cannot outlive its snapshot.
				done := make(chan struct{})
				events <- comparisonCaptureEvent{
					job:   job,
					frame: frame,
					err:   err,
					done:  done,
				}
				<-done
			}
		})
	}
	go func() {
		workers.Wait()
		close(events)
	}()
	var firstErr error
	next := 0
	var dispatch chan comparisonCaptureJob
	parentDone := ctx.Done()
	for events != nil {
		if firstErr == nil && next < len(jobs) && workCtx.Err() == nil {
			dispatch = work
		} else {
			dispatch = nil
			if !workClosed {
				close(work)
				workClosed = true
			}
		}
		var job comparisonCaptureJob
		if dispatch != nil {
			job = jobs[next]
		}
		select {
		case dispatch <- job:
			next++
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.update != nil {
				if firstErr == nil && workCtx.Err() == nil {
					progress(event.job, *event.update)
				}
				continue
			}
			if firstErr == nil || comparisonCancellationOnly(firstErr) {
				switch {
				case event.err != nil:
					if firstErr == nil || !comparisonCancellationOnly(event.err) {
						firstErr = event.err
					}
				default:
					if err := publish(event.job, event.frame, workCtx.Err() == nil); err != nil {
						firstErr = &comparisonPublicationError{err}
					}
				}
			}
			close(event.done)
			if firstErr != nil {
				cancel()
			}
		case <-parentDone:
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			cancel()
			// Disable the permanently ready parent cancellation case while
			// draining, so terminal workers retain bounded backpressure.
			parentDone = nil
		}
	}
	if firstErr == nil && workCtx.Err() != nil {
		firstErr = workCtx.Err()
	}
	if firstErr != nil {
		return fmt.Errorf("capture comparison input batch: %w", firstErr)
	}
	return nil
}

// Publication failure always invalidates the attempted snapshot.
type comparisonPublicationError struct{ error }

func (e *comparisonPublicationError) Unwrap() error { return e.error }

func comparisonCancellationOnly(err error) bool {
	if _, fatal := errors.AsType[*comparisonPublicationError](err); fatal {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		for _, cause := range causes {
			if !comparisonCancellationOnly(cause) {
				return false
			}
		}
		return len(causes) > 0
	}
	if cause := errors.Unwrap(err); cause != nil {
		return comparisonCancellationOnly(cause)
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func validateComparisonCapture(job comparisonCaptureJob, frame screenshots.ComparisonFrame) error {
	if len(frame.ImageBytes) == 0 || len(frame.ImageBytes) > 64<<20 {
		return errors.New("comparison capture exceeds the image bounds")
	}
	geometry, err := png.DecodeConfig(bytes.NewReader(frame.ImageBytes))
	if err != nil || geometry.Width != frame.Width || geometry.Height != frame.Height {
		return errors.New("comparison capture has invalid image geometry")
	}
	if !job.request.ValidOutputSize(frame) {
		return errors.New("comparison capture changed the requested image geometry")
	}
	return nil
}
