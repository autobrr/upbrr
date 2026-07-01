// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/autobrr/upbrr/pkg/api"
)

const dupeCheckEventPrefix = "dupe:job:"

type DupeCheckTrackerState struct {
	Tracker    string              `json:"tracker"`
	Status     string              `json:"status"`
	Message    string              `json:"message"`
	Result     api.DupeCheckResult `json:"result"`
	StartedAt  string              `json:"startedAt"`
	FinishedAt string              `json:"finishedAt"`
}

type DupeCheckSnapshot struct {
	JobID          string                  `json:"jobID"`
	SourcePath     string                  `json:"sourcePath"`
	Status         string                  `json:"status"`
	Trackers       []DupeCheckTrackerState `json:"trackers"`
	CompletedCount int                     `json:"completedCount"`
	TotalCount     int                     `json:"totalCount"`
	Summary        api.DupeCheckSummary    `json:"summary"`
	Error          string                  `json:"error"`
	StartedAt      string                  `json:"startedAt"`
	FinishedAt     string                  `json:"finishedAt"`
}

type dupeCheckJob struct {
	mu                sync.Mutex
	id                string
	sourcePath        string
	overrides         api.ExternalIDOverrides
	nameOverrides     api.ReleaseNameOverrides
	metadataOverrides api.MetadataOverrides
	trackers          []string
	states            map[string]DupeCheckTrackerState
	completedCount    int
	totalCount        int
	summary           api.DupeCheckSummary
	status            string
	errorMessage      string
	startedAt         time.Time
	finishedAt        time.Time
	cancel            context.CancelFunc
}

func (a *App) StartDupeCheck(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, metadataOverrides api.MetadataOverrides, trackers []string) (string, error) {
	if a == nil || a.currentCore() == nil {
		return "", errors.New("app not initialized")
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", errors.New("path is required")
	}
	resolvedTrackers := normalizeTrackerList(trackers)
	if len(resolvedTrackers) == 0 {
		return "", errors.New("at least one tracker must be selected")
	}

	jobID := randomJobID()
	states := make(map[string]DupeCheckTrackerState, len(resolvedTrackers))
	for _, tracker := range resolvedTrackers {
		normalized := strings.ToUpper(strings.TrimSpace(tracker))
		if normalized == "" {
			continue
		}
		states[normalized] = DupeCheckTrackerState{Tracker: normalized, Status: "queued", Message: "queued"}
	}

	job := &dupeCheckJob{
		id:                jobID,
		sourcePath:        trimmedPath,
		overrides:         overrides,
		nameOverrides:     nameOverrides,
		metadataOverrides: metadataOverrides,
		trackers:          resolvedTrackers,
		states:            states,
		totalCount:        len(states),
		summary:           api.DupeCheckSummary{SourcePath: trimmedPath},
		status:            "queued",
		startedAt:         time.Now().UTC(),
	}

	baseCtx := a.runtimeContext()
	jobCtx, cancel := context.WithCancel(baseCtx)
	job.cancel = cancel

	a.dupeMu.Lock()
	a.dupes[jobID] = job
	a.dupeMu.Unlock()

	a.emitDupeCheckSnapshot(baseCtx, job)
	go func() {
		defer cancel()
		a.runDupeCheckJob(jobCtx, baseCtx, job)
	}()

	return jobID, nil
}

func (a *App) GetDupeCheckSnapshot(jobID string) (DupeCheckSnapshot, error) {
	if a == nil {
		return DupeCheckSnapshot{}, errors.New("app not initialized")
	}
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return DupeCheckSnapshot{}, errors.New("job id is required")
	}

	job := a.getDupeCheckJob(trimmedID)
	if job == nil {
		return DupeCheckSnapshot{}, errors.New("dupe job not found")
	}

	return buildDupeCheckSnapshot(job), nil
}

func (a *App) CancelDupeCheck(jobID string) error {
	if a == nil {
		return errors.New("app not initialized")
	}
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return errors.New("job id is required")
	}

	job := a.getDupeCheckJob(trimmedID)
	if job == nil {
		return errors.New("dupe job not found")
	}

	job.mu.Lock()
	cancel := job.cancel
	job.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (a *App) runDupeCheckJob(ctx context.Context, eventCtx context.Context, job *dupeCheckJob) {
	if a == nil || a.currentCore() == nil || job == nil {
		return
	}

	job.mu.Lock()
	job.status = "running"
	job.mu.Unlock()
	a.emitDupeCheckSnapshot(eventCtx, job)

	progressCtx := api.WithDupeProgressReporter(ctx, func(update api.DupeProgressUpdate) {
		a.applyDupeProgress(eventCtx, job, update)
	})

	req := api.Request{
		Paths:    []string{job.sourcePath},
		Mode:     api.ModeGUI,
		Trackers: job.trackers,
		Options:  a.baseUploadOptions(),

		ExternalIDOverrides:  job.overrides,
		ReleaseNameOverrides: job.nameOverrides,
		MetadataOverrides:    job.metadataOverrides,
	}

	summary, err := a.currentCore().CheckDupes(progressCtx, req)

	job.mu.Lock()
	job.finishedAt = time.Now().UTC()
	job.cancel = nil
	if err != nil {
		if ctx.Err() != nil {
			job.status = "canceled"
			job.errorMessage = "dupe check canceled"
			for tracker, state := range job.states {
				if !isDupeTerminalStatus(state.Status) {
					state.Status = "canceled"
					state.Message = "canceled"
					state.FinishedAt = job.finishedAt.Format(time.RFC3339)
					job.states[tracker] = state
				}
			}
		} else {
			job.status = "failed"
			job.errorMessage = err.Error()
		}
		job.mu.Unlock()
		a.emitDupeCheckSnapshot(eventCtx, job)
		return
	}

	job.summary = summary
	for _, result := range summary.Results {
		for _, tracker := range dupeResultTrackerNames(result) {
			state := job.states[tracker]
			if state.Tracker == "" {
				state.Tracker = tracker
				job.trackers = append(job.trackers, tracker)
				job.totalCount++
			}
			if !isDupeTerminalStatus(state.Status) {
				job.completedCount++
			}
			state.Status = resultStatus(result)
			state.Message = resultMessage(result)
			state.Result = result
			state.Result.Tracker = tracker
			if state.StartedAt == "" {
				state.StartedAt = job.startedAt.Format(time.RFC3339)
			}
			state.FinishedAt = job.finishedAt.Format(time.RFC3339)
			job.states[tracker] = state
		}
	}

	if hasFailedDupeState(job.states) {
		job.status = "completed_with_errors"
		if strings.TrimSpace(job.errorMessage) == "" {
			job.errorMessage = "one or more tracker dupe checks failed"
		}
	} else {
		job.status = "completed"
		job.errorMessage = ""
	}
	job.mu.Unlock()
	a.emitDupeCheckSnapshot(eventCtx, job)
}

func randomJobID() string {
	value, err := rand.Int(rand.Reader, new(big.Int).SetUint64(^uint64(0)))
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), value.Uint64())
}

func (a *App) applyDupeProgress(ctx context.Context, job *dupeCheckJob, update api.DupeProgressUpdate) {
	if a == nil || job == nil {
		return
	}
	tracker := strings.ToUpper(strings.TrimSpace(update.Tracker))
	if tracker == "" {
		return
	}

	job.mu.Lock()
	state := job.states[tracker]
	if state.Tracker == "" {
		state.Tracker = tracker
		job.trackers = append(job.trackers, tracker)
		job.totalCount++
	}
	previousStatus := state.Status
	state.Status = strings.TrimSpace(update.Status)
	if state.Status == "" {
		state.Status = previousStatus
	}
	if strings.TrimSpace(update.Message) != "" {
		state.Message = strings.TrimSpace(update.Message)
	}
	if state.Status == "running" && state.StartedAt == "" {
		state.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if update.Result.Tracker != "" {
		state.Result = update.Result
		upsertDupeSummaryResult(&job.summary, update.Result)
	}
	if isDupeTerminalStatus(state.Status) {
		if state.FinishedAt == "" {
			state.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if !isDupeTerminalStatus(previousStatus) {
			job.completedCount++
		}
	}
	if update.Total > 0 {
		job.totalCount = update.Total
	}
	job.states[tracker] = state
	job.mu.Unlock()

	a.emitDupeCheckSnapshot(ctx, job)
}

func upsertDupeSummaryResult(summary *api.DupeCheckSummary, result api.DupeCheckResult) {
	if summary == nil {
		return
	}
	tracker := strings.ToUpper(strings.TrimSpace(result.Tracker))
	if tracker == "" {
		return
	}
	for idx := range summary.Results {
		existing := strings.ToUpper(strings.TrimSpace(summary.Results[idx].Tracker))
		if existing != tracker {
			continue
		}
		summary.Results[idx] = result
		return
	}
	summary.Results = append(summary.Results, result)
}

// dupeResultTrackerNames expands grouped dupe summary labels into the
// per-tracker state keys used by frontend snapshots.
func dupeResultTrackerNames(result api.DupeCheckResult) []string {
	trackers := strings.Split(result.Tracker, ",")
	names := make([]string, 0, len(trackers))
	seen := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func hasFailedDupeState(states map[string]DupeCheckTrackerState) bool {
	for _, state := range states {
		if strings.EqualFold(state.Status, "failed") {
			return true
		}
	}
	return false
}

func isDupeTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "skipped", "failed", "canceled":
		return true
	default:
		return false
	}
}

func resultStatus(result api.DupeCheckResult) string {
	if strings.TrimSpace(result.Status) != "" {
		return result.Status
	}
	if result.Skipped {
		return "skipped"
	}
	if strings.TrimSpace(result.Error) != "" {
		return "failed"
	}
	return "completed"
}

func resultMessage(result api.DupeCheckResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return strings.TrimSpace(result.Error)
	}
	if strings.TrimSpace(result.SkipReason) != "" {
		return strings.TrimSpace(result.SkipReason)
	}
	if result.HasDupes {
		return fmt.Sprintf("%d dupes found", len(result.Filtered))
	}
	return "no dupes found"
}

func (a *App) emitDupeCheckSnapshot(ctx context.Context, job *dupeCheckJob) {
	if a == nil || ctx == nil || job == nil {
		return
	}
	snapshot := buildDupeCheckSnapshot(job)
	runtime.EventsEmit(ctx, dupeCheckEventPrefix+job.id, snapshot)
}

func buildDupeCheckSnapshot(job *dupeCheckJob) DupeCheckSnapshot {
	job.mu.Lock()
	defer job.mu.Unlock()

	trackers := make([]DupeCheckTrackerState, 0, len(job.trackers))
	seen := make(map[string]struct{}, len(job.trackers))
	for _, tracker := range job.trackers {
		normalized := strings.ToUpper(strings.TrimSpace(tracker))
		if normalized == "" {
			continue
		}
		state, ok := job.states[normalized]
		if !ok {
			state = DupeCheckTrackerState{Tracker: normalized, Status: "queued", Message: "queued"}
		}
		trackers = append(trackers, state)
		seen[normalized] = struct{}{}
	}
	for tracker, state := range job.states {
		if _, ok := seen[tracker]; ok {
			continue
		}
		trackers = append(trackers, state)
	}

	startedAt := ""
	if !job.startedAt.IsZero() {
		startedAt = job.startedAt.Format(time.RFC3339)
	}
	finishedAt := ""
	if !job.finishedAt.IsZero() {
		finishedAt = job.finishedAt.Format(time.RFC3339)
	}

	return DupeCheckSnapshot{
		JobID:          job.id,
		SourcePath:     job.sourcePath,
		Status:         job.status,
		Trackers:       trackers,
		CompletedCount: job.completedCount,
		TotalCount:     job.totalCount,
		Summary:        job.summary,
		Error:          job.errorMessage,
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
	}
}

func (a *App) getDupeCheckJob(jobID string) *dupeCheckJob {
	a.dupeMu.Lock()
	defer a.dupeMu.Unlock()
	return a.dupes[jobID]
}

func (a *App) stopAllDupeJobs() {
	if a == nil {
		return
	}
	a.dupeMu.Lock()
	jobs := make([]*dupeCheckJob, 0, len(a.dupes))
	for _, job := range a.dupes {
		jobs = append(jobs, job)
	}
	a.dupeMu.Unlock()

	for _, job := range jobs {
		if job == nil {
			continue
		}
		job.mu.Lock()
		cancel := job.cancel
		job.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
}
