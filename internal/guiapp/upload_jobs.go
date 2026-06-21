// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	trackerUploadEventPrefix   = "upload:job:"
	trackerUploadProgressEvent = "upload:progress"
)

// TrackerUploadTrackerState reports frontend-visible state for one tracker in
// an upload job. UploadedCount includes accepted uploads returned before a later
// tracker error or cancellation.
type TrackerUploadTrackerState struct {
	Tracker         string  `json:"tracker"`
	Status          string  `json:"status"`
	Task            string  `json:"task"`
	TaskStatus      string  `json:"taskStatus"`
	Message         string  `json:"message"`
	CompletedPieces int     `json:"completedPieces"`
	TotalPieces     int     `json:"totalPieces"`
	Percent         int     `json:"percent"`
	HashRateMiB     float64 `json:"hashRateMiB"`
	UploadedCount   int     `json:"uploadedCount"`
	StartedAt       string  `json:"startedAt"`
	FinishedAt      string  `json:"finishedAt"`
}

// TrackerUploadSnapshot reports frontend-visible state for a tracker upload
// job. UploadedCount is the sum of per-tracker accepted uploads, including
// partial counts returned with non-nil errors.
type TrackerUploadSnapshot struct {
	JobID                  string                      `json:"jobID"`
	SourcePath             string                      `json:"sourcePath"`
	Status                 string                      `json:"status"`
	CurrentTask            string                      `json:"currentTask"`
	CurrentTaskStatus      string                      `json:"currentTaskStatus"`
	CurrentMessage         string                      `json:"currentMessage"`
	CurrentCompletedPieces int                         `json:"currentCompletedPieces"`
	CurrentTotalPieces     int                         `json:"currentTotalPieces"`
	CurrentPercent         int                         `json:"currentPercent"`
	CurrentHashRateMiB     float64                     `json:"currentHashRateMiB"`
	Trackers               []TrackerUploadTrackerState `json:"trackers"`
	FailedTrackers         []string                    `json:"failedTrackers"`
	UploadedCount          int                         `json:"uploadedCount"`
	Error                  string                      `json:"error"`
	StartedAt              string                      `json:"startedAt"`
	FinishedAt             string                      `json:"finishedAt"`
}

type trackerUploadJob struct {
	mu                   sync.Mutex
	cleanupOnce          sync.Once
	id                   string
	sourcePath           string
	runOptions           runOptions
	core                 api.Core
	logger               interface{ Close() error }
	overrides            api.ExternalIDOverrides
	nameOverrides        api.ReleaseNameOverrides
	questionnaireAnswers map[string]map[string]string
	descriptionGroups    []api.DescriptionBuilderGroup
	trackers             []string
	ignoreDupesFor       []string
	states               map[string]TrackerUploadTrackerState
	failedTrackers       []string
	uploadedCount        int
	status               string
	currentTask          string
	currentTaskStatus    string
	currentMessage       string
	currentCompleted     int
	currentTotal         int
	currentPercent       int
	currentHashRateMiB   float64
	errorMessage         string
	startedAt            time.Time
	finishedAt           time.Time
	cancel               context.CancelFunc
}

func (j *trackerUploadJob) closeResources() {
	if j == nil {
		return
	}

	j.cleanupOnce.Do(func() {
		j.mu.Lock()
		coreSvc := j.core
		logger := j.logger
		j.core = nil
		j.logger = nil
		j.mu.Unlock()

		if coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger != nil {
			_ = logger.Close()
		}
	})
}

// StartTrackerUpload starts a Wails upload job for selected trackers and
// returns its job ID. Snapshots preserve partial upload counts returned with
// later tracker errors or cancellation.
func (a *App) StartTrackerUpload(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string, questionnaireAnswers map[string]map[string]string, descriptionGroups []api.DescriptionBuilderGroup, debug bool, noSeed bool, runLogLevel string) (string, error) {
	if err := a.requireCore(); err != nil {
		return "", err
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", errors.New("path is required")
	}
	resolvedTrackers := normalizeTrackerList(trackers)
	if len(resolvedTrackers) == 0 {
		return "", errors.New("at least one tracker must be selected")
	}
	runOpts, err := a.buildRunOptions(debug, noSeed, runLogLevel)
	if err != nil {
		return "", err
	}
	baseCtx := a.runtimeContext()

	runCore, runLogger, err := a.buildRunCore(runOpts)
	if err != nil {
		return "", err
	}

	seedReq := api.Request{
		Paths:                []string{trimmedPath},
		Mode:                 api.ModeGUI,
		DescriptionGroups:    api.CloneDescriptionBuilderGroups(descriptionGroups),
		Trackers:             resolvedTrackers,
		IgnoreDupesFor:       normalizeTrackerList(ignoreDupesFor),
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	if err := guishared.SeedRunCorePreparedMeta(baseCtx, a.currentCore(), runCore, seedReq); err != nil {
		_ = runCore.Close()
		_ = runLogger.Close()
		return "", fmt.Errorf("gui: %w", err)
	}

	jobID := randomUploadJobID()
	job := &trackerUploadJob{
		id:                   jobID,
		sourcePath:           trimmedPath,
		runOptions:           runOpts,
		core:                 runCore,
		logger:               runLogger,
		overrides:            overrides,
		nameOverrides:        nameOverrides,
		questionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
		descriptionGroups:    api.CloneDescriptionBuilderGroups(descriptionGroups),
		trackers:             resolvedTrackers,
		ignoreDupesFor:       normalizeTrackerList(ignoreDupesFor),
		states:               make(map[string]TrackerUploadTrackerState, len(resolvedTrackers)),
		status:               "queued",
		startedAt:            time.Now().UTC(),
	}
	for _, tracker := range resolvedTrackers {
		job.states[tracker] = TrackerUploadTrackerState{Tracker: tracker, Status: "queued", Message: "queued"}
	}

	jobCtx, cancel := context.WithCancel(baseCtx)
	job.cancel = cancel

	a.uploadMu.Lock()
	a.uploads[jobID] = job
	a.uploadMu.Unlock()

	a.emitTrackerUploadSnapshot(baseCtx, job)
	go func() {
		defer cancel()
		a.runTrackerUploadJob(jobCtx, baseCtx, job)
	}()

	return jobID, nil
}

// CancelTrackerUpload requests cancellation for a Wails tracker upload job.
func (a *App) CancelTrackerUpload(jobID string) error {
	if a == nil {
		return errors.New("app not initialized")
	}
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return errors.New("job id is required")
	}

	job := a.getTrackerUploadJob(trimmedID)
	if job == nil {
		return errors.New("upload job not found")
	}

	job.mu.Lock()
	cancel := job.cancel
	job.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	job.closeResources()
	return nil
}

// RetryFailedTrackerUpload starts a new Wails upload job for trackers that
// failed in the original job.
func (a *App) RetryFailedTrackerUpload(jobID string) (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return "", errors.New("job id is required")
	}

	job := a.getTrackerUploadJob(trimmedID)
	if job == nil {
		return "", errors.New("upload job not found")
	}

	job.mu.Lock()
	failedTrackers := append([]string(nil), job.failedTrackers...)
	sourcePath := job.sourcePath
	overrides := job.overrides
	nameOverrides := job.nameOverrides
	questionnaireAnswers := cloneQuestionnaireAnswers(job.questionnaireAnswers)
	descriptionGroups := api.CloneDescriptionBuilderGroups(job.descriptionGroups)
	ignoreDupesFor := append([]string(nil), job.ignoreDupesFor...)
	runOptions := job.runOptions
	job.mu.Unlock()

	if len(failedTrackers) == 0 {
		return "", errors.New("no failed trackers to retry")
	}

	return a.StartTrackerUpload(sourcePath, overrides, nameOverrides, failedTrackers, ignoreDupesFor, questionnaireAnswers, descriptionGroups, runOptions.Debug, runOptions.NoSeed, runOptions.RunLogLevel)
}

// GetTrackerUploadSnapshot returns the current Wails tracker upload job state.
func (a *App) GetTrackerUploadSnapshot(jobID string) (TrackerUploadSnapshot, error) {
	if a == nil {
		return TrackerUploadSnapshot{}, errors.New("app not initialized")
	}
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return TrackerUploadSnapshot{}, errors.New("job id is required")
	}

	job := a.getTrackerUploadJob(trimmedID)
	if job == nil {
		return TrackerUploadSnapshot{}, errors.New("upload job not found")
	}

	return buildTrackerUploadSnapshot(job), nil
}

// runTrackerUploadJob records UploadedCount before error handling so partial
// successes returned with non-nil errors remain visible in snapshots.
func (a *App) runTrackerUploadJob(ctx context.Context, eventCtx context.Context, job *trackerUploadJob) {
	if a == nil || job == nil {
		return
	}

	job.mu.Lock()
	job.status = "running"
	job.mu.Unlock()
	a.emitTrackerUploadSnapshot(eventCtx, job)

	for _, tracker := range job.trackers {
		if ctx.Err() != nil {
			break
		}

		job.mu.Lock()
		state := job.states[tracker]
		state.Status = "running"
		state.Message = "uploading"
		state.StartedAt = time.Now().UTC().Format(time.RFC3339)
		job.states[tracker] = state
		job.mu.Unlock()
		a.emitTrackerUploadSnapshot(eventCtx, job)

		progressCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
			a.applyTrackerUploadProgress(eventCtx, job, update)
		})
		result, err := a.runSingleTrackerUpload(progressCtx, job, tracker)
		if result.UploadedCount > 0 {
			job.mu.Lock()
			state = job.states[tracker]
			state.UploadedCount += result.UploadedCount
			job.states[tracker] = state
			job.uploadedCount += result.UploadedCount
			job.mu.Unlock()
		}
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			job.mu.Lock()
			state = job.states[tracker]
			state.Status = "failed"
			state.Message = err.Error()
			state.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			job.states[tracker] = state
			job.failedTrackers = append(job.failedTrackers, tracker)
			job.errorMessage = err.Error()
			job.mu.Unlock()
			a.emitTrackerUploadSnapshot(eventCtx, job)
			continue
		}

		job.mu.Lock()
		state = job.states[tracker]
		state.Status = "success"
		state.Message = "uploaded"
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		job.states[tracker] = state
		job.mu.Unlock()
		a.emitTrackerUploadSnapshot(eventCtx, job)
	}

	job.mu.Lock()
	job.finishedAt = time.Now().UTC()
	switch {
	case ctx.Err() != nil:
		job.status = "canceled"
		job.errorMessage = "upload canceled"
		for _, tracker := range job.trackers {
			state := job.states[tracker]
			if state.Status == "queued" || state.Status == "running" {
				state.Status = "canceled"
				state.Message = "canceled"
				state.FinishedAt = job.finishedAt.Format(time.RFC3339)
				job.states[tracker] = state
			}
		}
	case len(job.failedTrackers) > 0:
		job.status = "completed_with_errors"
	default:
		job.status = "completed"
		job.errorMessage = ""
	}
	job.cancel = nil
	job.mu.Unlock()
	job.closeResources()
	a.emitTrackerUploadSnapshot(eventCtx, job)
}

func (a *App) applyTrackerUploadProgress(eventCtx context.Context, job *trackerUploadJob, update api.UploadProgressUpdate) {
	if a == nil || job == nil {
		return
	}
	tracker := strings.TrimSpace(update.Tracker)
	if tracker == "" && len(job.trackers) == 1 {
		tracker = job.trackers[0]
	}

	job.mu.Lock()
	job.currentTask = strings.TrimSpace(update.Task)
	job.currentTaskStatus = strings.TrimSpace(update.Status)
	job.currentMessage = strings.TrimSpace(update.Message)
	job.currentCompleted = update.CompletedPieces
	job.currentTotal = update.TotalPieces
	job.currentPercent = update.Percent
	job.currentHashRateMiB = update.HashRateMiB
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
		if state.Status == "queued" && strings.EqualFold(job.currentTaskStatus, "running") {
			state.Status = "running"
		}
		if state.StartedAt == "" && state.Status == "running" {
			state.StartedAt = time.Now().UTC().Format(time.RFC3339)
		}
		job.states[tracker] = state
	}
	job.mu.Unlock()

	a.emitTrackerUploadSnapshot(eventCtx, job)
}

func (a *App) runSingleTrackerUpload(ctx context.Context, job *trackerUploadJob, tracker string) (api.Result, error) {
	if a == nil || job == nil || job.core == nil {
		return api.Result{}, errors.New("app not initialized")
	}

	req := api.Request{
		Paths:                       []string{job.sourcePath},
		Mode:                        api.ModeGUI,
		DescriptionGroups:           api.CloneDescriptionBuilderGroups(job.descriptionGroups),
		Trackers:                    []string{tracker},
		IgnoreDupesFor:              append([]string(nil), job.ignoreDupesFor...),
		IgnoreTrackerRuleFailures:   false,
		Options:                     buildRunUploadOptions(a.currentConfig(), job.runOptions),
		ExternalIDOverrides:         job.overrides,
		ReleaseNameOverrides:        job.nameOverrides,
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(job.questionnaireAnswers),
	}

	return wrapGUIResult(job.core.RunUploadPrepared(ctx, req))
}

func (a *App) emitTrackerUploadSnapshot(ctx context.Context, job *trackerUploadJob) {
	if a == nil || ctx == nil || job == nil {
		return
	}
	snapshot := buildTrackerUploadSnapshot(job)
	runtime.EventsEmit(ctx, trackerUploadEventPrefix+job.id, snapshot)
}

func buildTrackerUploadSnapshot(job *trackerUploadJob) TrackerUploadSnapshot {
	job.mu.Lock()
	defer job.mu.Unlock()

	trackers := make([]TrackerUploadTrackerState, 0, len(job.trackers))
	for _, tracker := range job.trackers {
		state, ok := job.states[tracker]
		if !ok {
			state = TrackerUploadTrackerState{Tracker: tracker, Status: "queued", Message: "queued"}
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

	return TrackerUploadSnapshot{
		JobID:                  job.id,
		SourcePath:             job.sourcePath,
		Status:                 job.status,
		CurrentTask:            job.currentTask,
		CurrentTaskStatus:      job.currentTaskStatus,
		CurrentMessage:         job.currentMessage,
		CurrentCompletedPieces: job.currentCompleted,
		CurrentTotalPieces:     job.currentTotal,
		CurrentPercent:         job.currentPercent,
		CurrentHashRateMiB:     job.currentHashRateMiB,
		Trackers:               trackers,
		FailedTrackers:         append([]string(nil), job.failedTrackers...),
		UploadedCount:          job.uploadedCount,
		Error:                  job.errorMessage,
		StartedAt:              startedAt,
		FinishedAt:             finishedAt,
	}
}

func randomUploadJobID() string {
	value, err := rand.Int(rand.Reader, new(big.Int).SetUint64(^uint64(0)))
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), value.Uint64())
}

func (a *App) getTrackerUploadJob(jobID string) *trackerUploadJob {
	a.uploadMu.Lock()
	defer a.uploadMu.Unlock()
	return a.uploads[jobID]
}

func (a *App) stopAllUploadJobs() {
	if a == nil {
		return
	}
	a.uploadMu.Lock()
	jobs := make([]*trackerUploadJob, 0, len(a.uploads))
	for _, job := range a.uploads {
		jobs = append(jobs, job)
	}
	a.uploadMu.Unlock()

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
		job.closeResources()
	}
}

func normalizeTrackerList(trackers []string) []string {
	seen := make(map[string]struct{})
	resolved := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		trimmed := strings.TrimSpace(tracker)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		resolved = append(resolved, trimmed)
	}
	return resolved
}

func cloneQuestionnaireAnswers(input map[string]map[string]string) map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]map[string]string, len(input))
	for tracker, values := range input {
		if len(values) == 0 {
			cloned[tracker] = map[string]string{}
			continue
		}
		inner := make(map[string]string, len(values))
		maps.Copy(inner, values)
		cloned[tracker] = inner
	}
	return cloned
}
