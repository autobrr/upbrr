// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const durableOperationProgressInterval = 100 * time.Millisecond

// durableOperationProgressReporter batches high-frequency intermediate
// snapshots while forcing terminal item updates and close-time state to disk.
type durableOperationProgressReporter struct {
	module      *Module
	ctx         context.Context
	ownerID     string
	workflowID  api.WorkflowID
	operationID api.WorkflowOperationID

	mu        sync.Mutex
	flushMu   sync.Mutex
	pending   []api.WorkflowProgressUpdate
	lastFlush time.Time
	timer     *time.Timer
	closed    bool
}

func newDurableOperationProgressReporter(
	ctx context.Context,
	module *Module,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) *durableOperationProgressReporter {
	return &durableOperationProgressReporter{
		module:      module,
		ctx:         ctx,
		ownerID:     ownerID,
		workflowID:  workflowID,
		operationID: operationID,
	}
}

func (r *durableOperationProgressReporter) Report(update api.WorkflowProgressUpdate) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.pending = append(r.pending, update)
	now := time.Now()
	if r.lastFlush.IsZero() || now.Sub(r.lastFlush) >= durableOperationProgressInterval || isTerminalProgressStatus(update.Status) {
		r.flushLocked(now, false)
		return
	}
	if r.timer == nil {
		delay := durableOperationProgressInterval - now.Sub(r.lastFlush)
		r.timer = time.AfterFunc(delay, r.flush)
	}
	r.mu.Unlock()
}

// Close persists every pending update and waits for an in-flight timer flush.
func (r *durableOperationProgressReporter) Close() {
	r.mu.Lock()
	r.flushLocked(time.Now(), true)
}

func (r *durableOperationProgressReporter) flush() {
	r.mu.Lock()
	if r.closed || len(r.pending) == 0 {
		r.timer = nil
		r.mu.Unlock()
		return
	}
	r.flushLocked(time.Now(), false)
}

// flushLocked acquires flushMu before releasing mu. Close therefore cannot
// overtake a timer batch and publish the operation terminal state too early.
func (r *durableOperationProgressReporter) flushLocked(now time.Time, closeReporter bool) {
	if closeReporter {
		r.closed = true
	}
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	updates := append([]api.WorkflowProgressUpdate(nil), r.pending...)
	r.pending = nil
	if len(updates) > 0 {
		r.lastFlush = now
	}
	r.flushMu.Lock()
	r.mu.Unlock()
	if len(updates) > 0 {
		r.persist(updates)
	}
	r.flushMu.Unlock()
}

// Progress is observational. A reporter persistence panic must not abort or
// partially publish the owning workflow command.
func (r *durableOperationProgressReporter) persist(updates []api.WorkflowProgressUpdate) {
	defer func() {
		_ = recover()
	}()
	_, _ = r.module.mutateOperation(r.ctx, r.ownerID, r.workflowID, r.operationID, func(status *api.WorkflowOperationStatus) {
		for _, update := range updates {
			applyWorkflowProgress(status, update)
		}
	})
}

func isTerminalProgressStatus(status api.StageStatus) bool {
	switch status {
	case api.StageStatusBlocked,
		api.StageStatusStale,
		api.StageStatusFailed,
		api.StageStatusPartial,
		api.StageStatusSkipped,
		api.StageStatusCompleted,
		api.StageStatusExecuted,
		api.StageStatusInterrupted,
		api.StageStatusCanceled,
		api.StageStatusUnavailable:
		return true
	case api.StageStatusPending,
		api.StageStatusQueued,
		api.StageStatusReady,
		api.StageStatusRunning:
		return false
	}
	return false
}
