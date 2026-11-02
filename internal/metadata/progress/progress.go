// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package progress reports source-free metadata substeps within their preparation stage.
package progress

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const waitingInterval = 5 * time.Second

type stageKey struct{}

type stage struct {
	ctx             context.Context
	phase           api.PreparationProgressPhase
	mu              sync.Mutex
	active          []*Step
	current         *Step
	closed, partial bool
	stop, stopped   chan struct{}
}

// Step owns one active substep. Concurrent work retains its own counters and
// elapsed time; finishing the foreground step resumes the latest unfinished step.
type Step struct {
	stage    *stage
	update   api.PreparationProgressUpdate
	started  time.Time
	finished bool
}

// StartStage brackets collection with terminal progress and a bounded waiting
// heartbeat. The returned closer must run once after collection, including errors.
// A canceled or expired context reports cancellation even if collection returns nil.
func StartStage(ctx context.Context, phase api.PreparationProgressPhase) (context.Context, func(error)) {
	s := &stage{
		ctx:     ctx,
		phase:   phase,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	s.current = &Step{
		stage:   s,
		update:  api.NewPreparationProgressUpdate(phase, api.PreparationProgressRunning, "Stage started."),
		started: time.Now(),
	}
	s.emit()
	go func() {
		defer close(s.stopped)
		ticker := time.NewTicker(waitingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if !s.closed && !s.current.finished {
					s.emit()
				}
				s.mu.Unlock()
			}
		}
	}()
	return context.WithValue(ctx, stageKey{}, s), func(err error) {
		s.mu.Lock()
		s.closed = true
		close(s.stop)
		s.mu.Unlock()
		<-s.stopped
		status, message := api.PreparationProgressCompleted, "Stage complete."
		if s.partial {
			status, message = api.PreparationProgressPartial, "Stage complete with unavailable optional metadata."
		}
		if err != nil {
			status, message = api.PreparationProgressFailed, "Stage failed."
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status, message = api.PreparationProgressCanceled, "Stage canceled."
		}
		api.EmitPreparationProgress(ctx, api.NewPreparationProgressUpdate(phase, status, message))
	}
}

// Begin starts a named substep without advancing its parent stage. Labels and
// messages must be frontend-safe; never pass paths, endpoints or remote errors.
func Begin(ctx context.Context, label string) *Step {
	s, _ := ctx.Value(stageKey{}).(*stage)
	if s == nil {
		return &Step{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return &Step{}
	}
	p := &Step{
		stage:   s,
		update:  api.NewPreparationProgressUpdate(s.phase, api.PreparationProgressRunning, ""),
		started: time.Now(),
	}
	p.update.Substep = label
	s.active = append(s.active, p)
	s.current = p
	s.emit()
	return p
}

// Update records completed work. Total zero means discovery is incomplete;
// completed counts may include unsuccessful attempts when the message says so.
func (p *Step) Update(completed, total int, message string) {
	if p.stage == nil {
		return
	}
	s := p.stage
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || p.finished {
		return
	}
	p.update.CompletedItems = max(0, completed)
	p.update.TotalItems = max(0, total)
	p.update.Message = message
	if s.current == p {
		s.emit()
	}
}

// Finish reports the actual substep outcome and stops its waiting heartbeat.
// unavailable marks missing optional evidence; cancellation of losing parallel
// requests does not itself establish missing evidence or fail the parent stage.
func (p *Step) Finish(message string, unavailable bool) {
	if p.stage == nil {
		return
	}
	s := p.stage
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || p.finished {
		return
	}
	p.finished = true
	p.update.Message = message
	s.partial = s.partial || unavailable
	index := slices.Index(s.active, p)
	s.active = slices.Delete(s.active, index, index+1)
	if s.current != p {
		return
	}
	s.emit()
	if len(s.active) > 0 {
		s.current = s.active[len(s.active)-1]
		s.emit()
	}
}

// Retained identifies cache processing without claiming a network request and
// preserves unavailable evidence in the stage's eventual partial outcome.
func Retained(ctx context.Context, unavailable bool) {
	p := Begin(ctx, "Read retained metadata evidence")
	message := "Using retained metadata evidence."
	if unavailable {
		message = "Retained metadata evidence unavailable."
	}
	p.Finish(message, unavailable)
}

// emit is called with the stage lock held, ordering heartbeats and results.
func (s *stage) emit() {
	s.current.update.ElapsedSeconds = int64(time.Since(s.current.started) / time.Second)
	api.EmitPreparationProgress(s.ctx, s.current.update)
}
