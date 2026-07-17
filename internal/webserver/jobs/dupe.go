// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// dupeJob contains mutable state for one owner-scoped duplicate check.
type dupeJob struct {
	mu                         sync.Mutex
	id, correlationID          string
	release                    api.ReleaseRef
	spec                       DupeSpec
	trackers                   []string
	states                     map[string]DupeCheckTrackerState
	completedCount, totalCount int
	explicitTotal              bool
	summary                    api.DupeCheckSummary
	status                     string
	failure, outcomeFailure    *api.OperationFailure
	outcomeStatus              string
	startedAt, finishedAt      time.Time
	outcomeAt                  time.Time
}

func newDupeJob(id string, spec DupeSpec, now time.Time) *dupeJob {
	trackers := spec.Snapshot.Input.Trackers
	states := make(map[string]DupeCheckTrackerState, len(trackers))
	for _, tracker := range trackers {
		states[tracker] = DupeCheckTrackerState{
			Tracker: tracker,
			Status:  StatusQueued,
			Message: StatusQueued,
		}
	}
	return &dupeJob{
		id:            id,
		correlationID: spec.CorrelationID,
		release:       spec.Snapshot.Input.Release,
		spec:          spec,
		trackers:      append([]string(nil), trackers...),
		states:        states,
		totalCount:    len(states),
		summary:       api.DupeCheckSummary{SourcePath: spec.Snapshot.Input.Release.SourcePath},
		status:        StatusQueued,
		startedAt:     now,
	}
}

// runDupe executes the runner, merges progress and summary results, and closes resources before terminal emission.
func (e *Engine) runDupe(ctx context.Context, job *dupeJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			e.failDupe(job, "dupe worker panicked: "+sanitizeMessage(fmt.Sprint(recovered)))
		}
	}()
	job.mu.Lock()
	job.status = StatusRunning
	job.mu.Unlock()
	e.emitDupeJob(job)
	progressCtx := api.WithDupeProgressReporter(ctx, func(update api.DupeProgressUpdate) { e.applyDupeProgress(job, update) })
	summary, err := job.spec.Runner.CheckDupes(progressCtx, job.input())
	now := e.deps.now().UTC()
	job.mu.Lock()
	job.outcomeAt = now
	job.summary = cloneSummary(summary)
	for _, result := range summary.Results {
		job.mergeResultLocked(result, now)
	}
	if err != nil {
		if ctx.Err() != nil {
			job.outcomeStatus = StatusCanceled
			job.outcomeFailure = nil
			job.cancelActiveLocked()
		} else {
			job.outcomeStatus = StatusFailed
			job.outcomeFailure = failureForError(
				err,
				api.OperationKindDuplicateCheck,
				"Duplicate check failed.",
				api.OperationRecoveryRetry,
			)
			job.failActiveLocked()
		}
		job.mu.Unlock()
		return
	}
	if hasFailedDupeState(job.states) {
		job.outcomeStatus = StatusCompletedWithErrors
		job.outcomeFailure = genericFailure(
			api.OperationKindDuplicateCheck,
			"One or more tracker duplicate checks failed.",
			api.OperationRecoveryRetry,
		)
	} else {
		job.outcomeStatus = StatusCompleted
		job.outcomeFailure = nil
	}
	job.mu.Unlock()
}

// failDupe converts a worker failure into a sanitized failed snapshot and terminates active tracker states.
func (e *Engine) failDupe(job *dupeJob, message string) {
	e.warn("duplicate-check Job failed: " + sanitizeMessage(message))
	job.mu.Lock()
	if job.outcomeAt.IsZero() {
		job.outcomeAt = e.deps.now().UTC()
	}
	job.outcomeStatus = StatusFailed
	job.outcomeFailure = genericFailure(
		api.OperationKindDuplicateCheck,
		"Duplicate check failed.",
		api.OperationRecoveryRetry,
	)
	job.failActiveLocked()
	job.mu.Unlock()
}

// finalizeDupe publishes the staged outcome only after per-Job resources close.
func (e *Engine) finalizeDupe(job *dupeJob) {
	job.mu.Lock()
	if job.outcomeStatus == "" {
		job.outcomeStatus = StatusFailed
		job.outcomeFailure = genericFailure(
			api.OperationKindDuplicateCheck,
			"Duplicate check failed.",
			api.OperationRecoveryRetry,
		)
	}
	job.status = job.outcomeStatus
	job.failure = cloneFailure(job.outcomeFailure)
	job.finishedAt = e.deps.now().UTC()
	job.mu.Unlock()
}

// applyDupeProgress merges dynamically discovered trackers without double-counting terminal transitions.
func (e *Engine) applyDupeProgress(job *dupeJob, update api.DupeProgressUpdate) {
	tracker := strings.ToUpper(strings.TrimSpace(update.Tracker))
	if tracker == "" {
		return
	}
	now := e.deps.now().UTC()
	job.mu.Lock()
	state, exists := job.states[tracker]
	if update.Total > 0 {
		job.explicitTotal = true
	}
	if !exists {
		state.Tracker = tracker
		job.trackers = append(job.trackers, tracker)
		if !job.explicitTotal {
			job.totalCount++
		}
	}
	previousStatus := state.Status
	if status := strings.TrimSpace(update.Status); status != "" {
		state.Status = status
	}
	if message := strings.TrimSpace(update.Message); message != "" {
		state.Message = sanitizeMessage(message)
	}
	if state.Status == StatusRunning && state.StartedAt == "" {
		state.StartedAt = formatTime(now)
	}
	if strings.TrimSpace(update.Result.Tracker) != "" {
		result := cloneResult(update.Result)
		result.Tracker = tracker
		state.Result = result
		upsertDupeSummaryResult(&job.summary, result)
	}
	if isDupeTerminal(state.Status) {
		if state.FinishedAt == "" {
			state.FinishedAt = formatTime(now)
		}
		if !isDupeTerminal(previousStatus) {
			job.completedCount++
		}
	}
	if update.Total > job.totalCount {
		job.totalCount = update.Total
	}
	job.states[tracker] = state
	job.mu.Unlock()
	e.emitDupeJob(job)
}

// mergeResultLocked upserts one canonical tracker result.
// The caller must hold j.mu.
func (j *dupeJob) mergeResultLocked(raw api.DupeCheckResult, now time.Time) {
	tracker := strings.ToUpper(strings.TrimSpace(raw.Tracker))
	if tracker == "" {
		return
	}
	result := cloneResult(raw)
	result.Tracker = tracker
	state, exists := j.states[tracker]
	if !exists {
		state.Tracker = tracker
		j.trackers = append(j.trackers, tracker)
		if !j.explicitTotal {
			j.totalCount++
		}
	}
	if !isDupeTerminal(state.Status) {
		j.completedCount++
	}
	state.Status = resultStatus(result)
	state.Message = sanitizeMessage(resultMessage(result))
	state.Result = result
	if state.StartedAt == "" {
		state.StartedAt = formatTime(j.startedAt)
	}
	state.FinishedAt = formatTime(now)
	j.states[tracker] = state
}

func (j *dupeJob) input() api.DuplicateCheckInput {
	input := cloneDuplicateExecutionSnapshot(j.spec.Snapshot).Input
	input.Trackers = append([]string(nil), j.trackers...)
	return input
}

// snapshot copies all mutable duplicate-check state for lock-free sink delivery.
func (j *dupeJob) snapshot() DupeCheckSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	trackers := make([]DupeCheckTrackerState, 0, len(j.trackers))
	seen := make(map[string]struct{}, len(j.trackers))
	for _, tracker := range j.trackers {
		normalized := strings.ToUpper(strings.TrimSpace(tracker))
		if normalized == "" {
			continue
		}
		trackers = append(trackers, cloneDupeState(j.states[normalized]))
		seen[normalized] = struct{}{}
	}
	for tracker, state := range j.states {
		if _, ok := seen[tracker]; !ok {
			trackers = append(trackers, cloneDupeState(state))
		}
	}
	return DupeCheckSnapshot{
		JobID:             j.id,
		CorrelationID:     j.correlationID,
		Release:           j.release,
		RuntimeGeneration: j.spec.Snapshot.RuntimeGeneration,
		RequestedTrackers: append([]string(nil), j.spec.Snapshot.Input.Trackers...),
		Status:            j.status,
		Trackers:          trackers,
		CompletedCount:    j.completedCount,
		TotalCount:        j.totalCount,
		Summary:           cloneSummary(j.summary),
		Failure:           cloneFailure(j.failure),
		StartedAt:         formatTime(j.startedAt),
		FinishedAt:        formatTime(j.finishedAt),
	}
}

func (j *dupeJob) cancelActiveLocked() {
	for tracker, state := range j.states {
		if !isDupeTerminal(state.Status) {
			j.completedCount++
			state.Status = StatusCanceled
			state.Message = StatusCanceled
			state.FinishedAt = formatTime(j.outcomeAt)
			j.states[tracker] = state
		}
	}
}
func (j *dupeJob) failActiveLocked() {
	message := "Duplicate check failed."
	if j.outcomeFailure != nil && strings.TrimSpace(j.outcomeFailure.Message) != "" {
		message = j.outcomeFailure.Message
	}
	for tracker, state := range j.states {
		if !isDupeTerminal(state.Status) {
			j.completedCount++
			state.Status = StatusFailed
			state.Message = message
			state.FinishedAt = formatTime(j.outcomeAt)
			j.states[tracker] = state
		}
	}
}

func upsertDupeSummaryResult(summary *api.DupeCheckSummary, result api.DupeCheckResult) {
	tracker := strings.ToUpper(strings.TrimSpace(result.Tracker))
	if tracker == "" {
		return
	}
	for idx := range summary.Results {
		if strings.ToUpper(strings.TrimSpace(summary.Results[idx].Tracker)) == tracker {
			summary.Results[idx] = cloneResult(result)
			return
		}
	}
	summary.Results = append(summary.Results, cloneResult(result))
}
func hasFailedDupeState(states map[string]DupeCheckTrackerState) bool {
	for _, state := range states {
		if strings.EqualFold(state.Status, StatusFailed) {
			return true
		}
	}
	return false
}
func isDupeTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusCompleted, "skipped", StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}
func isJobTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusCompleted, StatusCompletedWithErrors, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}
func resultStatus(result api.DupeCheckResult) string {
	if status := strings.TrimSpace(result.Status); status != "" {
		return status
	}
	if result.Skipped {
		return "skipped"
	}
	if strings.TrimSpace(result.Error) != "" {
		return StatusFailed
	}
	return StatusCompleted
}
func resultMessage(result api.DupeCheckResult) string {
	if result.Error != "" {
		return result.Error
	}
	if result.SkipReason != "" {
		return result.SkipReason
	}
	if result.HasDupes {
		return fmt.Sprintf("%d dupes found", len(result.Filtered))
	}
	return "no dupes found"
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
func cloneSummary(summary api.DupeCheckSummary) api.DupeCheckSummary {
	cloned := summary
	cloned.Results = make([]api.DupeCheckResult, len(summary.Results))
	for idx := range summary.Results {
		cloned.Results[idx] = cloneResult(summary.Results[idx])
	}
	cloned.Notes = append([]string(nil), summary.Notes...)
	cloned.Eligibility = cloneTrackerEligibility(summary.Eligibility)
	return cloned
}
func cloneResult(result api.DupeCheckResult) api.DupeCheckResult {
	result.Raw = append([]api.DupeEntry(nil), result.Raw...)
	result.Filtered = append([]api.DupeEntry(nil), result.Filtered...)
	result.Notes = append([]string(nil), result.Notes...)
	result.SkipRules = append([]string(nil), result.SkipRules...)
	if result.Error != "" {
		result.Error = sanitizeMessage(result.Error)
	}
	return result
}
func cloneDupeState(state DupeCheckTrackerState) DupeCheckTrackerState {
	state.Result = cloneResult(state.Result)
	return state
}
