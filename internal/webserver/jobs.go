// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	completedJobRetention      = 24 * time.Hour
	maxCompletedDupeJobs       = 200
	maxCompletedUploadJobs     = 200
	seedPreparedMetaTimeout    = 30 * time.Second
	trackerUploadProgressEvent = "upload:progress"
)

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
	mu             sync.Mutex
	sessionID      string
	id             string
	sourcePath     string
	overrides      api.ExternalIDOverrides
	nameOverrides  api.ReleaseNameOverrides
	trackers       []string
	states         map[string]DupeCheckTrackerState
	completedCount int
	totalCount     int
	summary        api.DupeCheckSummary
	status         string
	errorMessage   string
	startedAt      time.Time
	finishedAt     time.Time
	cancel         context.CancelFunc
}

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
	sessionID            string
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
	lastSnapshotEmit     time.Time
	snapshotThrottle     time.Duration
	errorMessage         string
	startedAt            time.Time
	finishedAt           time.Time
	cancel               context.CancelFunc
}

const trackerUploadSnapshotThrottle = 200 * time.Millisecond

const trackerUploadHashRateEmitDeltaMiB = 1.0

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
		inner := make(map[string]string, len(values))
		maps.Copy(inner, values)
		cloned[tracker] = inner
	}
	return cloned
}

func normalizePatterns(patterns []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func randomJobID() string {
	value, err := rand.Int(rand.Reader, new(big.Int).SetUint64(^uint64(0)))
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), value.Uint64())
}

func (b *Backend) StartDupeCheck(sessionID string, path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string) (string, error) {
	if err := b.requireCore(); err != nil {
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

	jobID := randomJobID()
	states := make(map[string]DupeCheckTrackerState, len(resolvedTrackers))
	for _, tracker := range resolvedTrackers {
		normalized := strings.ToUpper(strings.TrimSpace(tracker))
		states[normalized] = DupeCheckTrackerState{Tracker: normalized, Status: "queued", Message: "queued"}
	}
	job := &dupeCheckJob{
		sessionID:     sessionID,
		id:            jobID,
		sourcePath:    trimmedPath,
		overrides:     overrides,
		nameOverrides: nameOverrides,
		trackers:      resolvedTrackers,
		states:        states,
		totalCount:    len(states),
		summary:       api.DupeCheckSummary{SourcePath: trimmedPath},
		status:        "queued",
		startedAt:     time.Now().UTC(),
	}

	jobCtx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel

	b.dupeMu.Lock()
	b.dupes[jobID] = job
	b.pruneCompletedDupeJobsLocked(maxCompletedDupeJobs)
	b.dupeMu.Unlock()
	b.emitDupeCheckSnapshot(job)

	b.dupeWG.Add(1)
	go func() {
		defer cancel()
		b.runDupeCheckJob(jobCtx, job)
	}()
	return jobID, nil
}

// GetDupeCheckSnapshot returns a dupe job snapshot only to the session that
// started the job.
func (b *Backend) GetDupeCheckSnapshot(sessionID string, jobID string) (DupeCheckSnapshot, error) {
	job := b.getDupeCheckJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
	if job == nil {
		return DupeCheckSnapshot{}, errors.New("dupe job not found")
	}
	return buildDupeCheckSnapshot(job), nil
}

// CancelDupeCheck requests cancellation only for a dupe job owned by sessionID.
func (b *Backend) CancelDupeCheck(sessionID string, jobID string) error {
	job := b.getDupeCheckJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
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

func (b *Backend) runDupeCheckJob(ctx context.Context, job *dupeCheckJob) {
	defer b.dupeWG.Done()

	job.mu.Lock()
	job.status = "running"
	job.mu.Unlock()
	b.emitDupeCheckSnapshot(job)

	progressCtx := api.WithDupeProgressReporter(ctx, func(update api.DupeProgressUpdate) {
		b.applyDupeProgress(job, update)
	})

	req := api.Request{
		Paths:                []string{job.sourcePath},
		Mode:                 api.ModeGUI,
		Trackers:             job.trackers,
		Options:              b.baseUploadOptions(),
		ExternalIDOverrides:  job.overrides,
		ReleaseNameOverrides: job.nameOverrides,
	}

	summary, err := b.currentCore().CheckDupes(progressCtx, req)
	job.mu.Lock()
	job.finishedAt = time.Now().UTC()
	job.cancel = nil
	if err != nil {
		if ctx.Err() != nil {
			job.status = "canceled"
			job.errorMessage = "dupe check canceled"
		} else {
			job.status = "failed"
			job.errorMessage = err.Error()
		}
		job.mu.Unlock()
		b.emitDupeCheckSnapshot(job)
		b.scheduleDupeJobCleanup(job)
		return
	}
	job.summary = summary
	for _, result := range summary.Results {
		tracker := strings.ToUpper(strings.TrimSpace(result.Tracker))
		state := job.states[tracker]
		if !isDupeTerminalStatus(state.Status) {
			job.completedCount++
		}
		state.Tracker = tracker
		state.Status = resultStatus(result)
		state.Message = resultMessage(result)
		state.Result = result
		if state.StartedAt == "" {
			state.StartedAt = job.startedAt.Format(time.RFC3339)
		}
		state.FinishedAt = job.finishedAt.Format(time.RFC3339)
		job.states[tracker] = state
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
	b.emitDupeCheckSnapshot(job)
	b.scheduleDupeJobCleanup(job)
}

func (b *Backend) applyDupeProgress(job *dupeCheckJob, update api.DupeProgressUpdate) {
	tracker := strings.ToUpper(strings.TrimSpace(update.Tracker))
	if tracker == "" {
		return
	}
	job.mu.Lock()
	state := job.states[tracker]
	previousStatus := state.Status
	state.Tracker = tracker
	if strings.TrimSpace(update.Status) != "" {
		state.Status = strings.TrimSpace(update.Status)
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
	if isDupeTerminalStatus(state.Status) && !isDupeTerminalStatus(previousStatus) {
		job.completedCount++
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if update.Total > 0 {
		job.totalCount = update.Total
	}
	job.states[tracker] = state
	job.mu.Unlock()
	b.emitDupeCheckSnapshot(job)
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

// StartTrackerUpload starts an upload job owned by sessionID for selected
// trackers and returns its job ID. Snapshots preserve partial upload counts
// returned with later tracker errors or cancellation.
func (b *Backend) StartTrackerUpload(sessionID string, path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string, questionnaireAnswers map[string]map[string]string, descriptionGroups []api.DescriptionBuilderGroup, debug bool, noSeed bool, runLogLevel string) (string, error) {
	if err := b.requireCore(); err != nil {
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
	runOpts, err := b.buildRunOptions(debug, noSeed, runLogLevel)
	if err != nil {
		return "", err
	}
	runCore, runLogger, err := b.buildRunCore(runOpts)
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
	seedCtx, cancel := context.WithTimeout(context.Background(), seedPreparedMetaTimeout)
	defer cancel()
	if err := guishared.SeedRunCorePreparedMeta(seedCtx, b.currentCore(), runCore, seedReq); err != nil {
		_ = runCore.Close()
		_ = runLogger.Close()
		return "", fmt.Errorf("web: %w", err)
	}

	jobID := randomJobID()
	job := &trackerUploadJob{
		sessionID:            sessionID,
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
		snapshotThrottle:     trackerUploadSnapshotThrottle,
	}
	for _, tracker := range resolvedTrackers {
		job.states[tracker] = TrackerUploadTrackerState{Tracker: tracker, Status: "queued", Message: "queued"}
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel

	b.uploadMu.Lock()
	b.uploads[jobID] = job
	b.pruneCompletedUploadJobsLocked(maxCompletedUploadJobs)
	b.uploadMu.Unlock()
	b.emitTrackerUploadSnapshot(job)

	b.uploadWG.Add(1)
	go func() {
		defer cancel()
		b.runTrackerUploadJob(jobCtx, job)
	}()
	return jobID, nil
}

// CancelTrackerUpload requests cancellation only for an upload job owned by
// sessionID.
func (b *Backend) CancelTrackerUpload(sessionID string, jobID string) error {
	job := b.getTrackerUploadJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
	if job == nil {
		return errors.New("upload job not found")
	}
	job.mu.Lock()
	cancel := job.cancel
	job.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// RetryFailedTrackerUpload starts a retry job for failed trackers only when
// sessionID owns the original upload job.
func (b *Backend) RetryFailedTrackerUpload(sessionID string, jobID string) (string, error) {
	job := b.getTrackerUploadJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
	if job == nil {
		return "", errors.New("upload job not found")
	}
	job.mu.Lock()
	failedTrackers := append([]string(nil), job.failedTrackers...)
	sourcePath := job.sourcePath
	retrySessionID := job.sessionID
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
	return b.StartTrackerUpload(retrySessionID, sourcePath, overrides, nameOverrides, failedTrackers, ignoreDupesFor, questionnaireAnswers, descriptionGroups, runOptions.Debug, runOptions.NoSeed, runOptions.RunLogLevel)
}

// GetTrackerUploadSnapshot returns an upload job snapshot only to the session
// that started the job.
func (b *Backend) GetTrackerUploadSnapshot(sessionID string, jobID string) (TrackerUploadSnapshot, error) {
	job := b.getTrackerUploadJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
	if job == nil {
		return TrackerUploadSnapshot{}, errors.New("upload job not found")
	}
	return buildTrackerUploadSnapshot(job), nil
}

// runTrackerUploadJob records UploadedCount before error handling so partial
// successes returned with non-nil errors remain visible in snapshots.
func (b *Backend) runTrackerUploadJob(ctx context.Context, job *trackerUploadJob) {
	defer b.uploadWG.Done()

	job.mu.Lock()
	job.status = "running"
	job.mu.Unlock()
	b.emitTrackerUploadSnapshot(job)

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
		b.emitTrackerUploadSnapshot(job)

		progressCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
			b.applyTrackerUploadProgress(job, update)
		})
		result, err := b.runSingleTrackerUpload(progressCtx, job, tracker)
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
			b.emitTrackerUploadSnapshot(job)
			continue
		}

		job.mu.Lock()
		state = job.states[tracker]
		state.Status = "success"
		state.Message = "uploaded"
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		job.states[tracker] = state
		job.mu.Unlock()
		b.emitTrackerUploadSnapshot(job)
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
	b.emitTrackerUploadSnapshot(job)
	b.scheduleTrackerUploadJobCleanup(job)
}

func (b *Backend) applyTrackerUploadProgress(job *trackerUploadJob, update api.UploadProgressUpdate) {
	if b == nil || job == nil {
		return
	}
	tracker := strings.TrimSpace(update.Tracker)
	if tracker == "" && len(job.trackers) == 1 {
		tracker = job.trackers[0]
	}

	job.mu.Lock()
	now := time.Now()
	previousStatus := job.currentTaskStatus
	previousPercent := job.currentPercent
	previousHashRate := job.currentHashRateMiB
	job.currentTask = strings.TrimSpace(update.Task)
	job.currentTaskStatus = strings.TrimSpace(update.Status)
	job.currentMessage = strings.TrimSpace(update.Message)
	job.currentCompleted = update.CompletedPieces
	job.currentTotal = update.TotalPieces
	job.currentPercent = update.Percent
	job.currentHashRateMiB = update.HashRateMiB
	throttle := job.snapshotThrottle
	if throttle <= 0 {
		throttle = trackerUploadSnapshotThrottle
	}
	shouldEmit := job.lastSnapshotEmit.IsZero() ||
		now.Sub(job.lastSnapshotEmit) >= throttle ||
		previousPercent != job.currentPercent ||
		previousStatus != job.currentTaskStatus ||
		absFloat64(previousHashRate-job.currentHashRateMiB) >= trackerUploadHashRateEmitDeltaMiB
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
	if shouldEmit {
		job.lastSnapshotEmit = now
	}
	job.mu.Unlock()

	if shouldEmit {
		b.emitTrackerUploadSnapshot(job)
	}
}

func absFloat64(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func (b *Backend) runSingleTrackerUpload(ctx context.Context, job *trackerUploadJob, tracker string) (api.Result, error) {
	req := api.Request{
		Paths:                       []string{job.sourcePath},
		Mode:                        api.ModeGUI,
		DescriptionGroups:           api.CloneDescriptionBuilderGroups(job.descriptionGroups),
		Trackers:                    []string{tracker},
		IgnoreDupesFor:              append([]string(nil), job.ignoreDupesFor...),
		IgnoreTrackerRuleFailures:   false,
		Options:                     buildRunUploadOptions(b.currentConfig(), job.runOptions),
		ExternalIDOverrides:         job.overrides,
		ReleaseNameOverrides:        job.nameOverrides,
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(job.questionnaireAnswers),
	}
	return wrapWebResult(job.core.RunUploadPrepared(ctx, req))
}

func upsertDupeSummaryResult(summary *api.DupeCheckSummary, result api.DupeCheckResult) {
	if summary == nil {
		return
	}
	tracker := strings.ToUpper(strings.TrimSpace(result.Tracker))
	for idx := range summary.Results {
		if strings.ToUpper(strings.TrimSpace(summary.Results[idx].Tracker)) == tracker {
			summary.Results[idx] = result
			return
		}
	}
	summary.Results = append(summary.Results, result)
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
	case "completed", "completed_with_errors", "skipped", "failed", "canceled":
		return true
	default:
		return false
	}
}

func isDupeJobTerminal(job *dupeCheckJob) bool {
	if job == nil {
		return false
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	return isDupeTerminalStatus(job.status) && !job.finishedAt.IsZero()
}

func isTrackerUploadJobTerminal(job *trackerUploadJob) bool {
	if job == nil {
		return false
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(job.status)) {
	case "completed", "completed_with_errors", "failed", "canceled":
		return !job.finishedAt.IsZero()
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

func (b *Backend) emitDupeCheckSnapshot(job *dupeCheckJob) {
	b.hub.Emit(job.sessionID, "dupe:job:"+job.id, buildDupeCheckSnapshot(job))
}

func buildDupeCheckSnapshot(job *dupeCheckJob) DupeCheckSnapshot {
	job.mu.Lock()
	defer job.mu.Unlock()
	trackers := make([]DupeCheckTrackerState, 0, len(job.trackers))
	for _, tracker := range job.trackers {
		normalized := strings.ToUpper(strings.TrimSpace(tracker))
		trackers = append(trackers, job.states[normalized])
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
		StartedAt:      job.startedAt.Format(time.RFC3339),
		FinishedAt:     formatTime(job.finishedAt),
	}
}

func (b *Backend) getDupeCheckJob(jobID string) *dupeCheckJob {
	b.dupeMu.Lock()
	defer b.dupeMu.Unlock()
	return b.dupes[jobID]
}

func (b *Backend) getDupeCheckJobForSession(sessionID string, jobID string) *dupeCheckJob {
	job := b.getDupeCheckJob(jobID)
	if job == nil {
		return nil
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if strings.TrimSpace(job.sessionID) != sessionID {
		return nil
	}
	return job
}

func (b *Backend) scheduleDupeJobCleanup(job *dupeCheckJob) {
	if job == nil {
		return
	}

	b.dupeMu.Lock()
	b.pruneCompletedDupeJobsLocked(maxCompletedDupeJobs)
	b.dupeMu.Unlock()

	time.AfterFunc(completedJobRetention, func() {
		b.dupeMu.Lock()
		defer b.dupeMu.Unlock()

		if current := b.dupes[job.id]; current == job && isDupeJobTerminal(job) {
			delete(b.dupes, job.id)
		}
	})
}

func (b *Backend) pruneCompletedDupeJobsLocked(maxCompleted int) {
	if maxCompleted <= 0 {
		return
	}

	completed := make([]*dupeCheckJob, 0, len(b.dupes))
	for _, job := range b.dupes {
		if isDupeJobTerminal(job) {
			completed = append(completed, job)
		}
	}
	if len(completed) <= maxCompleted {
		return
	}

	sort.Slice(completed, func(i, j int) bool {
		return completed[i].finishedAt.Before(completed[j].finishedAt)
	})

	for _, job := range completed[:len(completed)-maxCompleted] {
		delete(b.dupes, job.id)
	}
}

func (b *Backend) stopAllDupeJobs() {
	b.dupeMu.Lock()
	jobs := make([]*dupeCheckJob, 0, len(b.dupes))
	for _, job := range b.dupes {
		jobs = append(jobs, job)
	}
	b.dupeMu.Unlock()
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
	b.dupeWG.Wait()
}

func (b *Backend) emitTrackerUploadSnapshot(job *trackerUploadJob) {
	b.hub.Emit(job.sessionID, "upload:job:"+job.id, buildTrackerUploadSnapshot(job))
}

func buildTrackerUploadSnapshot(job *trackerUploadJob) TrackerUploadSnapshot {
	job.mu.Lock()
	defer job.mu.Unlock()
	trackers := make([]TrackerUploadTrackerState, 0, len(job.trackers))
	for _, tracker := range job.trackers {
		trackers = append(trackers, job.states[tracker])
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
		StartedAt:              job.startedAt.Format(time.RFC3339),
		FinishedAt:             formatTime(job.finishedAt),
	}
}

func (b *Backend) getTrackerUploadJob(jobID string) *trackerUploadJob {
	b.uploadMu.Lock()
	defer b.uploadMu.Unlock()
	return b.uploads[jobID]
}

func (b *Backend) getTrackerUploadJobForSession(sessionID string, jobID string) *trackerUploadJob {
	job := b.getTrackerUploadJob(jobID)
	if job == nil {
		return nil
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if strings.TrimSpace(job.sessionID) != sessionID {
		return nil
	}
	return job
}

func (b *Backend) scheduleTrackerUploadJobCleanup(job *trackerUploadJob) {
	if job == nil {
		return
	}

	b.uploadMu.Lock()
	b.pruneCompletedUploadJobsLocked(maxCompletedUploadJobs)
	b.uploadMu.Unlock()

	time.AfterFunc(completedJobRetention, func() {
		b.uploadMu.Lock()
		defer b.uploadMu.Unlock()

		if current := b.uploads[job.id]; current == job && isTrackerUploadJobTerminal(job) {
			delete(b.uploads, job.id)
		}
	})
}

func (b *Backend) pruneCompletedUploadJobsLocked(maxCompleted int) {
	if maxCompleted <= 0 {
		return
	}

	completed := make([]*trackerUploadJob, 0, len(b.uploads))
	for _, job := range b.uploads {
		if isTrackerUploadJobTerminal(job) {
			completed = append(completed, job)
		}
	}
	if len(completed) <= maxCompleted {
		return
	}

	sort.Slice(completed, func(i, j int) bool {
		return completed[i].finishedAt.Before(completed[j].finishedAt)
	})

	for _, job := range completed[:len(completed)-maxCompleted] {
		delete(b.uploads, job.id)
	}
}

func (b *Backend) stopAllUploadJobs() {
	b.uploadMu.Lock()
	jobs := make([]*trackerUploadJob, 0, len(b.uploads))
	for _, job := range b.uploads {
		jobs = append(jobs, job)
	}
	b.uploadMu.Unlock()
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
	b.uploadWG.Wait()
}

func (b *Backend) stopAllLogStreams() {
	b.streamMu.Lock()
	streams := make([]*backendLogStream, 0, len(b.streams))
	for id, stream := range b.streams {
		delete(b.streams, id)
		streams = append(streams, stream)
		select {
		case <-stream.stop:
		default:
			close(stream.stop)
		}
	}
	b.streamMu.Unlock()
	for _, stream := range streams {
		if stream == nil {
			continue
		}
		<-stream.done
	}
	b.streamWG.Wait()
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}
