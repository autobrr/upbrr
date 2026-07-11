// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	dvdMenuCaptureEventPrefix              = "dvdmenu:job:"
	errDVDMenuCaptureRequiresMetadataCache = "DVD menu capture requires metadata preview"
	// dvdMenuCompletedJobRetention bounds how long terminal snapshots remain queryable.
	dvdMenuCompletedJobRetention = 24 * time.Hour
	// maxCompletedDVDMenuJobs bounds retained terminal snapshots across all GUI captures.
	maxCompletedDVDMenuJobs = 200
	dvdMenuSnapshotThrottle = 150 * time.Millisecond
)

// dvdMenuCaptureJob owns one isolated core/logger snapshot and its synchronized
// frontend-visible progress state.
type dvdMenuCaptureJob struct {
	mu             sync.Mutex
	cleanupOnce    sync.Once
	id             string
	sourcePath     string
	uploadOptions  api.UploadOptions
	core           api.Core
	logger         interface{ Close() error }
	overrides      api.ExternalIDOverrides
	nameOverrides  api.ReleaseNameOverrides
	status         string
	phase          string
	message        string
	discovered     int
	visitedStates  int
	visitedButtons int
	captured       int
	warnings       int
	result         api.DVDMenuCaptureResult
	errorMessage   string
	startedAt      time.Time
	finishedAt     time.Time
	lastEmit       time.Time
	cancel         context.CancelFunc
	retentionTimer *time.Timer
}

// StartDVDMenuCapture starts a background capture using the current prepared
// GUI metadata and returns its job ID. Progress is emitted on
// dvdmenu:job:<jobID>; setup errors reject the frontend call.
func (a *App) StartDVDMenuCapture(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (string, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return "", err
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", errors.New("path is required")
	}
	req := newDVDMenuRequest(trimmedPath, overrides, nameOverrides, rt.baseUploadOptions())
	baseCtx := a.runtimeContext()

	var preparedMeta api.PreparedMetadata
	preparedMetaFound := false
	if exporter, ok := rt.core.(guishared.PreparedMetaExporter); ok {
		preparedMeta, preparedMetaFound, err = exporter.ExportGUICachedPreparedMeta(baseCtx, req)
		if err != nil {
			return "", fmt.Errorf("DVD menu capture metadata preview cache: %w", err)
		}
		if !preparedMetaFound {
			return "", errors.New(errDVDMenuCaptureRequiresMetadataCache)
		}
	}

	runCore, runLogger, err := a.buildRunCoreFromSnapshot(rt, runOptions{})
	if err != nil {
		return "", err
	}
	if preparedMetaFound {
		importer, ok := runCore.(guishared.PreparedMetaImporter)
		if !ok {
			_ = runCore.Close()
			_ = runLogger.Close()
			return "", errors.New("DVD menu capture metadata preview cache: run core cannot import prepared metadata")
		}
		if err := importer.ImportPreparedMetadataForGUI(baseCtx, req, preparedMeta); err != nil {
			_ = runCore.Close()
			_ = runLogger.Close()
			return "", fmt.Errorf("DVD menu capture metadata preview cache: %w", err)
		}
	}

	job := &dvdMenuCaptureJob{
		id:            randomJobID(),
		sourcePath:    trimmedPath,
		uploadOptions: req.Options,
		core:          runCore,
		logger:        runLogger,
		overrides:     overrides,
		nameOverrides: nameOverrides,
		status:        "queued",
		phase:         "queued",
		message:       "DVD menu capture queued.",
		startedAt:     time.Now().UTC(),
	}
	jobCtx, cancel := context.WithCancel(baseCtx)
	job.cancel = cancel

	a.dvdMenuMu.Lock()
	if a.dvdMenus == nil {
		a.dvdMenus = make(map[string]*dvdMenuCaptureJob)
	}
	a.dvdMenus[job.id] = job
	a.pruneCompletedDVDMenuJobsLocked(maxCompletedDVDMenuJobs)
	a.dvdMenuWG.Add(1)
	a.dvdMenuMu.Unlock()
	a.emitDVDMenuCaptureSnapshot(baseCtx, job, true)
	go func() {
		defer a.dvdMenuWG.Done()
		defer cancel()
		a.runDVDMenuCaptureJob(jobCtx, baseCtx, job)
	}()
	return job.id, nil
}

// GetDVDMenuCaptureSnapshot returns the current capture job state. It rejects
// unknown or expired job IDs. Terminal snapshots have bounded time and
// capacity retention, so callers should consume their final state promptly.
func (a *App) GetDVDMenuCaptureSnapshot(jobID string) (api.DVDMenuCaptureSnapshot, error) {
	if a == nil {
		return api.DVDMenuCaptureSnapshot{}, errors.New("app not initialized")
	}
	job := a.getDVDMenuCaptureJob(strings.TrimSpace(jobID))
	if job == nil {
		return api.DVDMenuCaptureSnapshot{}, errors.New("DVD menu capture job not found")
	}
	return buildDVDMenuCaptureSnapshot(job), nil
}

// CancelDVDMenuCapture requests cancellation for an existing capture job.
// Completion is asynchronous and is reflected in later snapshots.
func (a *App) CancelDVDMenuCapture(jobID string) error {
	if a == nil {
		return errors.New("app not initialized")
	}
	job := a.getDVDMenuCaptureJob(strings.TrimSpace(jobID))
	if job == nil {
		return errors.New("DVD menu capture job not found")
	}
	job.mu.Lock()
	cancel := job.cancel
	job.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// ListDVDMenuScreenshots returns persisted manual and automatic menu images for
// one host filesystem source path. The call requires prepared GUI metadata.
func (a *App) ListDVDMenuScreenshots(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.ScreenshotImage, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.runtimeContext(), previewTimeout)
	defer cancel()
	req := newDVDMenuRequest(path, overrides, nameOverrides, rt.baseUploadOptions())
	return wrapGUIResult(rt.core.ListDVDMenuScreenshots(ctx, req))
}

// DeleteDVDMenuScreenshot removes one persisted image from the prepared
// release's managed directory and local records. Remote-host assets remain.
func (a *App) DeleteDVDMenuScreenshot(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, imagePath string) error {
	rt, err := a.requireRuntime()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.runtimeContext(), previewTimeout)
	defer cancel()
	req := newDVDMenuRequest(path, overrides, nameOverrides, rt.baseUploadOptions())
	return wrapGUIError(rt.core.DeleteDVDMenuScreenshot(ctx, req, imagePath))
}

func newDVDMenuRequest(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, options api.UploadOptions) api.Request {
	return api.Request{
		Paths:                []string{strings.TrimSpace(path)},
		Mode:                 api.ModeGUI,
		Options:              options,
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
}

// runDVDMenuCaptureJob owns the queued-to-terminal lifecycle, releases its
// per-run core/logger resources exactly once, and schedules bounded retention.
func (a *App) runDVDMenuCaptureJob(ctx context.Context, eventCtx context.Context, job *dvdMenuCaptureJob) {
	if job != nil {
		defer func() { _ = job.closeResources() }()
	}
	if a == nil || job == nil || job.core == nil {
		return
	}
	job.mu.Lock()
	job.status = "running"
	job.phase = "preflight"
	job.message = "Checking DVD menu capture support."
	job.mu.Unlock()
	a.emitDVDMenuCaptureSnapshot(eventCtx, job, true)

	progressCtx := api.WithDVDMenuProgressReporter(ctx, func(update api.DVDMenuProgressUpdate) {
		a.applyDVDMenuCaptureProgress(eventCtx, job, update)
	})
	req := newDVDMenuRequest(job.sourcePath, job.overrides, job.nameOverrides, job.uploadOptions)
	result, err := job.core.CaptureDVDMenus(progressCtx, req)

	job.mu.Lock()
	job.result = result
	job.finishedAt = time.Now().UTC()
	job.cancel = nil
	job.discovered = result.DiscoveredMenus
	job.visitedStates = result.VisitedStates
	job.visitedButtons = result.VisitedButtons
	job.captured = len(result.Images)
	job.warnings = len(result.Warnings)
	if err != nil {
		if ctx.Err() != nil {
			job.status = "canceled"
			job.phase = "canceled"
			job.message = "DVD menu capture canceled."
			job.errorMessage = "DVD menu capture canceled"
		} else {
			job.status = "failed"
			job.phase = "failed"
			job.message = "DVD menu capture failed."
			job.errorMessage = redaction.RedactValue(err.Error(), nil)
		}
	} else {
		job.status = "completed"
		job.phase = "complete"
		job.message = "DVD menu capture finished."
		job.errorMessage = ""
	}
	job.mu.Unlock()
	a.emitDVDMenuCaptureSnapshot(eventCtx, job, true)
	a.scheduleDVDMenuJobCleanup(job)
}

func (a *App) applyDVDMenuCaptureProgress(ctx context.Context, job *dvdMenuCaptureJob, update api.DVDMenuProgressUpdate) {
	job.mu.Lock()
	if strings.TrimSpace(update.Phase) != "" {
		job.phase = strings.TrimSpace(update.Phase)
	}
	if strings.TrimSpace(update.Message) != "" {
		job.message = strings.TrimSpace(update.Message)
	}
	job.discovered = update.DiscoveredMenus
	job.visitedStates = update.VisitedStates
	job.visitedButtons = update.VisitedButtons
	job.captured = update.CapturedCount
	job.warnings = update.WarningCount
	job.mu.Unlock()
	a.emitDVDMenuCaptureSnapshot(ctx, job, false)
}

// emitDVDMenuCaptureSnapshot publishes a throttled Wails event; force bypasses
// throttling for queued and terminal transitions.
func (a *App) emitDVDMenuCaptureSnapshot(ctx context.Context, job *dvdMenuCaptureJob, force bool) {
	if a == nil || ctx == nil || job == nil || ctx.Value("events") == nil {
		return
	}
	job.mu.Lock()
	if !force && time.Since(job.lastEmit) < dvdMenuSnapshotThrottle {
		job.mu.Unlock()
		return
	}
	job.lastEmit = time.Now()
	job.mu.Unlock()
	defer func() { _ = recover() }()
	runtime.EventsEmit(ctx, dvdMenuCaptureEventPrefix+job.id, buildDVDMenuCaptureSnapshot(job))
}

func buildDVDMenuCaptureSnapshot(job *dvdMenuCaptureJob) api.DVDMenuCaptureSnapshot {
	job.mu.Lock()
	defer job.mu.Unlock()
	return api.DVDMenuCaptureSnapshot{
		JobID:           job.id,
		SourcePath:      job.sourcePath,
		Status:          job.status,
		Phase:           job.phase,
		Message:         job.message,
		DiscoveredMenus: job.discovered,
		VisitedStates:   job.visitedStates,
		VisitedButtons:  job.visitedButtons,
		CapturedCount:   job.captured,
		WarningCount:    job.warnings,
		Result:          job.result,
		Error:           job.errorMessage,
		StartedAt:       formatDVDMenuTime(job.startedAt),
		FinishedAt:      formatDVDMenuTime(job.finishedAt),
	}
}

func formatDVDMenuTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func (a *App) getDVDMenuCaptureJob(jobID string) *dvdMenuCaptureJob {
	if strings.TrimSpace(jobID) == "" {
		return nil
	}
	a.dvdMenuMu.Lock()
	defer a.dvdMenuMu.Unlock()
	return a.dvdMenus[jobID]
}

// closeResources closes the per-run core and logger at most once.
func (job *dvdMenuCaptureJob) closeResources() error {
	if job == nil {
		return nil
	}
	var closeErr error
	job.cleanupOnce.Do(func() {
		job.mu.Lock()
		coreSvc := job.core
		logger := job.logger
		job.core = nil
		job.logger = nil
		job.mu.Unlock()
		if coreSvc != nil {
			closeErr = errors.Join(closeErr, closeTrackerUploadResource("core", coreSvc))
		}
		if logger != nil {
			closeErr = errors.Join(closeErr, closeTrackerUploadResource("logger", logger))
		}
	})
	return closeErr
}

// scheduleDVDMenuJobCleanup retains a registered terminal job for the query
// window and arranges its eventual removal. Capacity pruning may remove it
// immediately when newer terminal jobs already fill the retained set.
func (a *App) scheduleDVDMenuJobCleanup(job *dvdMenuCaptureJob) {
	if a == nil || job == nil {
		return
	}
	a.dvdMenuMu.Lock()
	if a.dvdMenus[job.id] != job {
		a.dvdMenuMu.Unlock()
		return
	}
	a.pruneCompletedDVDMenuJobsLocked(maxCompletedDVDMenuJobs)
	if a.dvdMenus[job.id] != job {
		a.dvdMenuMu.Unlock()
		return
	}
	timer := time.AfterFunc(dvdMenuCompletedJobRetention, func() {
		a.dvdMenuMu.Lock()
		defer a.dvdMenuMu.Unlock()
		if current := a.dvdMenus[job.id]; current == job && isDVDMenuJobTerminal(job) {
			a.deleteDVDMenuJobLocked(job)
		}
	})
	job.mu.Lock()
	job.retentionTimer = timer
	job.mu.Unlock()
	a.dvdMenuMu.Unlock()
}

// pruneCompletedDVDMenuJobsLocked evicts the oldest terminal jobs until at
// most maxCompleted remain. The caller must hold a.dvdMenuMu.
func (a *App) pruneCompletedDVDMenuJobsLocked(maxCompleted int) {
	if maxCompleted <= 0 {
		return
	}
	completed := make([]*dvdMenuCaptureJob, 0, len(a.dvdMenus))
	for _, job := range a.dvdMenus {
		if isDVDMenuJobTerminal(job) {
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
		a.deleteDVDMenuJobLocked(job)
	}
}

// deleteDVDMenuJobLocked removes job and stops its retention timer. The caller
// must hold a.dvdMenuMu.
func (a *App) deleteDVDMenuJobLocked(job *dvdMenuCaptureJob) {
	delete(a.dvdMenus, job.id)
	job.mu.Lock()
	timer := job.retentionTimer
	job.retentionTimer = nil
	job.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

// isDVDMenuJobTerminal reports whether job has reached a queryable final state.
func isDVDMenuJobTerminal(job *dvdMenuCaptureJob) bool {
	if job == nil {
		return false
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.status == "completed" || job.status == "failed" || job.status == "canceled"
}

// stopAllDVDMenuJobs cancels active captures, waits for workers, releases
// per-job resources, and clears retained snapshots and cleanup timers.
func (a *App) stopAllDVDMenuJobs() {
	if a == nil {
		return
	}
	a.dvdMenuMu.Lock()
	jobs := make([]*dvdMenuCaptureJob, 0, len(a.dvdMenus))
	for _, job := range a.dvdMenus {
		jobs = append(jobs, job)
	}
	a.dvdMenuMu.Unlock()
	for _, job := range jobs {
		job.mu.Lock()
		cancel := job.cancel
		job.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	a.dvdMenuWG.Wait()
	for _, job := range jobs {
		_ = job.closeResources()
	}
	a.dvdMenuMu.Lock()
	for _, job := range a.dvdMenus {
		a.deleteDVDMenuJobLocked(job)
	}
	a.dvdMenuMu.Unlock()
}
