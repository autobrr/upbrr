// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// uploadJob contains mutable state for one owner-scoped multi-tracker upload.
type uploadJob struct {
	mu                                                     sync.Mutex
	id, correlationID, retryOf                             string
	release                                                api.ReleaseRef
	spec                                                   UploadSpec
	trackers                                               []string
	states                                                 map[string]TrackerUploadTrackerState
	failedTrackers                                         []string
	uploadedCount                                          int
	status, currentTask, currentTaskStatus, currentMessage string
	failure, outcomeFailure                                *api.OperationFailure
	outcomeStatus                                          string
	currentCompleted, currentTotal, currentPercent         int
	currentHashRateMiB                                     float64
	startedAt, finishedAt, outcomeAt, lastSnapshotEmit     time.Time
	progress                                               UploadProgressPolicy
}

func newUploadJob(id string, spec UploadSpec, now time.Time, progress UploadProgressPolicy) *uploadJob {
	trackers := spec.Snapshot.Input.Trackers
	states := make(map[string]TrackerUploadTrackerState, len(trackers))
	for _, tracker := range trackers {
		states[tracker] = TrackerUploadTrackerState{
			Tracker: tracker,
			Status:  StatusQueued,
			Message: StatusQueued,
		}
	}
	return &uploadJob{
		id:            id,
		correlationID: spec.CorrelationID,
		retryOf:       spec.RetryOf,
		release:       spec.Snapshot.Input.Release,
		spec:          spec,
		trackers:      append([]string(nil), trackers...),
		states:        states,
		status:        StatusQueued,
		startedAt:     now,
		progress:      progress,
	}
}

// runUpload executes one operation-wide Core call and derives Job state from typed per-tracker progress.
func (e *Engine) runUpload(ctx context.Context, job *uploadJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			e.failUpload(job, "upload worker panicked: "+sanitizeMessage(fmt.Sprint(recovered)))
		}
	}()
	job.mu.Lock()
	job.status = StatusRunning
	job.mu.Unlock()
	e.emitUploadJob(job)
	progressCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) { e.applyUploadProgress(job, update) })
	result, runErr := job.spec.Runner.RunUpload(progressCtx, job.plan())
	job.mu.Lock()
	job.uploadedCount = max(0, result.UploadedCount)
	job.outcomeAt = e.deps.now().UTC()
	terminalStatus := StatusCompleted
	var terminalFailure *api.OperationFailure
	switch {
	case ctx.Err() != nil:
		terminalStatus = StatusCanceled
		job.cancelActiveLocked()
	case runErr != nil && api.IsTrackerLocalUploadError(runErr):
		terminalStatus = StatusCompletedWithErrors
		terminalFailure = failureForError(
			runErr,
			api.OperationKindUploadExecute,
			"One or more tracker uploads failed.",
			api.OperationRecoveryRetry,
		)
		job.failNamedTrackersLocked(api.TrackerLocalUploadFailureNames(runErr), terminalFailure.Message)
		job.completeActiveTrackersLocked()
	case runErr != nil:
		terminalStatus = StatusFailed
		terminalFailure = failureForError(
			runErr,
			api.OperationKindUploadExecute,
			"Upload failed.",
			api.OperationRecoveryRetry,
		)
		job.failActiveTrackersLocked(terminalFailure.Message)
	case len(job.failedTrackers) > 0:
		terminalStatus = StatusCompletedWithErrors
		terminalFailure = genericFailure(
			api.OperationKindUploadExecute,
			"One or more tracker uploads failed.",
			api.OperationRecoveryRetry,
		)
	default:
		job.completeActiveTrackersLocked()
	}
	job.outcomeStatus = terminalStatus
	job.outcomeFailure = terminalFailure
	job.mu.Unlock()
}

// failUpload converts a worker or cleanup failure into a sanitized failed snapshot without overwriting terminal tracker states.
func (e *Engine) failUpload(job *uploadJob, message string) {
	e.warn("tracker-upload Job failed: " + sanitizeMessage(message))
	now := e.deps.now().UTC()
	job.mu.Lock()
	if job.outcomeAt.IsZero() {
		job.outcomeAt = now
	}
	job.outcomeStatus = StatusFailed
	job.outcomeFailure = genericFailure(
		api.OperationKindUploadExecute,
		"Upload failed.",
		api.OperationRecoveryRetry,
	)
	for _, tracker := range job.trackers {
		state := job.states[tracker]
		if state.Status == StatusQueued || state.Status == StatusRunning {
			state.Status = StatusFailed
			state.Message = job.outcomeFailure.Message
			state.FinishedAt = formatTime(job.outcomeAt)
			job.states[tracker] = state
		}
	}
	job.mu.Unlock()
}

// finalizeUpload publishes the staged outcome only after per-Job resources close.
func (e *Engine) finalizeUpload(job *uploadJob) {
	job.mu.Lock()
	if job.outcomeStatus == "" {
		job.outcomeStatus = StatusFailed
		job.outcomeFailure = genericFailure(
			api.OperationKindUploadExecute,
			"Upload failed.",
			api.OperationRecoveryRetry,
		)
	}
	job.status = job.outcomeStatus
	job.failure = cloneFailure(job.outcomeFailure)
	job.finishedAt = e.deps.now().UTC()
	job.mu.Unlock()
}

// applyUploadProgress updates job and tracker state for every event and applies only emission throttling.
func (e *Engine) applyUploadProgress(job *uploadJob, update api.UploadProgressUpdate) {
	tracker := strings.TrimSpace(update.Tracker)
	job.mu.Lock()
	if tracker == "" && len(job.trackers) == 1 {
		tracker = job.trackers[0]
	}
	now := e.deps.now().UTC()
	previousStatus, previousPercent, previousRate := job.currentTaskStatus, job.currentPercent, job.currentHashRateMiB
	job.currentTask = strings.TrimSpace(update.Task)
	job.currentTaskStatus = strings.TrimSpace(update.Status)
	job.currentMessage = strings.TrimSpace(update.Message)
	if job.currentMessage != "" {
		job.currentMessage = sanitizeMessage(job.currentMessage)
	}
	job.currentCompleted = update.CompletedPieces
	job.currentTotal = update.TotalPieces
	job.currentPercent = update.Percent
	job.currentHashRateMiB = update.HashRateMiB
	shouldEmit := job.progress.MinInterval <= 0 ||
		job.lastSnapshotEmit.IsZero() ||
		now.Sub(job.lastSnapshotEmit) >= job.progress.MinInterval ||
		previousPercent != job.currentPercent ||
		previousStatus != job.currentTaskStatus ||
		abs(previousRate-job.currentHashRateMiB) >= job.progress.HashRateDeltaMiB
	if tracker != "" {
		state := job.states[tracker]
		state.Tracker = tracker
		state.Task = job.currentTask
		state.TaskStatus = job.currentTaskStatus
		state.CompletedPieces = update.CompletedPieces
		state.TotalPieces = update.TotalPieces
		state.Percent = update.Percent
		state.HashRateMiB = update.HashRateMiB
		if job.currentMessage != "" {
			state.Message = job.currentMessage
		}
		if state.Status == StatusQueued && strings.EqualFold(state.TaskStatus, StatusRunning) {
			state.Status = StatusRunning
		}
		if state.StartedAt == "" && state.Status == StatusRunning {
			state.StartedAt = formatTime(now)
		}
		terminalTask := strings.EqualFold(state.Task, "tracker_upload") || strings.EqualFold(state.Task, "tracker_preparation")
		if terminalTask {
			switch strings.ToLower(state.TaskStatus) {
			case "completed":
				if strings.EqualFold(state.Task, "tracker_upload") {
					state.Status = "success"
					state.UploadedCount = max(1, state.UploadedCount)
					state.FinishedAt = formatTime(now)
				}
			case StatusFailed:
				state.Status = StatusFailed
				state.FinishedAt = formatTime(now)
				job.addFailedTrackerLocked(tracker)
			case StatusCanceled:
				state.Status = StatusCanceled
				state.FinishedAt = formatTime(now)
			}
		}
		job.states[tracker] = state
	}
	if shouldEmit {
		job.lastSnapshotEmit = now
	}
	job.mu.Unlock()
	if shouldEmit {
		e.emitUploadJob(job)
	}
}

func (j *uploadJob) plan() api.UploadExecutionPlan {
	snapshot := cloneUploadExecutionSnapshot(j.spec.Snapshot)
	snapshot.Input.Trackers = append([]string(nil), j.trackers...)
	return api.UploadExecutionPlan{Input: snapshot.Input, Outcome: snapshot.Outcome}
}

func (j *uploadJob) addFailedTrackerLocked(tracker string) {
	for _, existing := range j.failedTrackers {
		if strings.EqualFold(existing, tracker) {
			return
		}
	}
	j.failedTrackers = append(j.failedTrackers, tracker)
}

func (j *uploadJob) failActiveTrackersLocked(message string) {
	for _, tracker := range j.trackers {
		state := j.states[tracker]
		if state.Status == StatusQueued || state.Status == StatusRunning {
			state.Status = StatusFailed
			state.Message = message
			state.FinishedAt = formatTime(j.outcomeAt)
			j.states[tracker] = state
			j.addFailedTrackerLocked(tracker)
		}
	}
}

func (j *uploadJob) failNamedTrackersLocked(trackers []string, message string) {
	for _, tracker := range trackers {
		state, ok := j.states[tracker]
		if !ok || state.Status == "success" || state.Status == StatusCanceled {
			continue
		}
		state.Status = StatusFailed
		state.Message = message
		state.FinishedAt = formatTime(j.outcomeAt)
		j.states[tracker] = state
		j.addFailedTrackerLocked(tracker)
	}
}

func (j *uploadJob) completeActiveTrackersLocked() {
	for _, tracker := range j.trackers {
		state := j.states[tracker]
		if state.Status == StatusQueued || state.Status == StatusRunning {
			state.Status = "success"
			state.Message = "uploaded"
			state.FinishedAt = formatTime(j.outcomeAt)
			j.states[tracker] = state
		}
	}
}

// snapshot copies all mutable upload state for lock-free sink delivery.
func (j *uploadJob) snapshot() TrackerUploadSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	trackers := make([]TrackerUploadTrackerState, 0, len(j.trackers))
	for _, tracker := range j.trackers {
		trackers = append(trackers, j.states[tracker])
	}
	return TrackerUploadSnapshot{
		JobID:                  j.id,
		CorrelationID:          j.correlationID,
		RetryOf:                j.retryOf,
		Release:                j.release,
		RuntimeGeneration:      j.spec.Snapshot.RuntimeGeneration,
		Status:                 j.status,
		CurrentTask:            j.currentTask,
		CurrentTaskStatus:      j.currentTaskStatus,
		CurrentMessage:         j.currentMessage,
		CurrentCompletedPieces: j.currentCompleted,
		CurrentTotalPieces:     j.currentTotal,
		CurrentPercent:         j.currentPercent,
		CurrentHashRateMiB:     j.currentHashRateMiB,
		Trackers:               trackers,
		FailedTrackers:         append([]string{}, j.failedTrackers...),
		UploadedCount:          j.uploadedCount,
		Failure:                cloneFailure(j.failure),
		StartedAt:              formatTime(j.startedAt),
		FinishedAt:             formatTime(j.finishedAt),
	}
}

// retry copies immutable job input and restricts the tracker list to failed trackers.
func (j *uploadJob) retry() (UploadRetry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.failedTrackers) == 0 {
		return UploadRetry{}, errors.New("no failed trackers to retry")
	}
	return UploadRetry{
		RetryOf: j.id,
		Snapshot: func() UploadExecutionSnapshot {
			snapshot := cloneUploadExecutionSnapshot(j.spec.Snapshot)
			snapshot.Input.Trackers = append([]string(nil), j.failedTrackers...)
			return snapshot
		}(),
	}, nil
}

func (j *uploadJob) cancelActiveLocked() {
	for _, tracker := range j.trackers {
		state := j.states[tracker]
		if state.Status == StatusQueued || state.Status == StatusRunning {
			state.Status = StatusCanceled
			state.Message = StatusCanceled
			state.FinishedAt = formatTime(j.outcomeAt)
			j.states[tracker] = state
		}
	}
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
