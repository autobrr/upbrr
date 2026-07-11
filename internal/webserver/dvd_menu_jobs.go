// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	errDVDMenuCaptureRequiresMetadataCache = "DVD menu capture requires metadata preview"
	maxCompletedDVDMenuJobs                = 200
	dvdMenuSnapshotThrottle                = 150 * time.Millisecond
)

// dvdMenuCaptureJob owns one isolated core/logger snapshot and synchronized
// session-visible progress state.
type dvdMenuCaptureJob struct {
	mu             sync.Mutex
	cleanupOnce    sync.Once
	sessionID      string
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
}

// StartDVDMenuCapture starts a session-owned background DVD menu capture job
// using cached prepared metadata. The returned job continues after the request
// context ends and emits dvdmenu:job:<jobID> only to the owning session.
func (b *Backend) StartDVDMenuCapture(ctx context.Context, sessionID string, path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (string, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		return "", errors.New("request context is required")
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", errors.New("path is required")
	}
	req := newWebDVDMenuRequest(trimmedPath, overrides, nameOverrides, rt.baseUploadOptions())

	var preparedMeta api.PreparedMetadata
	preparedMetaFound := false
	if exporter, ok := rt.core.(guishared.PreparedMetaExporter); ok {
		preparedMeta, preparedMetaFound, err = exporter.ExportGUICachedPreparedMeta(ctx, req)
		if err != nil {
			return "", fmt.Errorf("DVD menu capture metadata preview cache: %w", err)
		}
		if !preparedMetaFound {
			return "", errors.New(errDVDMenuCaptureRequiresMetadataCache)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("DVD menu capture metadata preview cache: %w", err)
	}
	//nolint:contextcheck // Per-run core construction has no context-aware API; request context is checked before and after setup.
	runCore, runLogger, err := b.buildRunCoreFromSnapshot(rt, runOptions{})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		_ = runCore.Close()
		_ = runLogger.Close()
		return "", fmt.Errorf("DVD menu capture metadata preview cache: %w", err)
	}
	if preparedMetaFound {
		importer, ok := runCore.(guishared.PreparedMetaImporter)
		if !ok {
			_ = runCore.Close()
			_ = runLogger.Close()
			return "", errors.New("DVD menu capture metadata preview cache: run core cannot import prepared metadata")
		}
		if err := importer.ImportPreparedMetadataForGUI(ctx, req, preparedMeta); err != nil {
			_ = runCore.Close()
			_ = runLogger.Close()
			return "", fmt.Errorf("DVD menu capture metadata preview cache: %w", err)
		}
	}

	job := &dvdMenuCaptureJob{
		sessionID:     strings.TrimSpace(sessionID),
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
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job.cancel = cancel

	b.dvdMenuMu.Lock()
	if b.dvdMenus == nil {
		b.dvdMenus = make(map[string]*dvdMenuCaptureJob)
	}
	b.dvdMenus[job.id] = job
	b.pruneCompletedDVDMenuJobsLocked(maxCompletedDVDMenuJobs)
	b.dvdMenuWG.Add(1)
	b.dvdMenuMu.Unlock()
	b.emitDVDMenuCaptureSnapshot(job, true)
	go func() {
		defer cancel()
		b.runDVDMenuCaptureJob(jobCtx, job)
	}()
	return job.id, nil
}

// GetDVDMenuCaptureSnapshot returns a capture job only to its owning session.
// Unknown, expired, and foreign-session job IDs return the same not-found error.
func (b *Backend) GetDVDMenuCaptureSnapshot(sessionID string, jobID string) (api.DVDMenuCaptureSnapshot, error) {
	job := b.getDVDMenuCaptureJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
	if job == nil {
		return api.DVDMenuCaptureSnapshot{}, errors.New("DVD menu capture job not found")
	}
	return buildWebDVDMenuCaptureSnapshot(job), nil
}

// CancelDVDMenuCapture requests cancellation only for an owned capture job.
// Completion is asynchronous and is reflected in later snapshots.
func (b *Backend) CancelDVDMenuCapture(sessionID string, jobID string) error {
	job := b.getDVDMenuCaptureJobForSession(strings.TrimSpace(sessionID), strings.TrimSpace(jobID))
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
func (b *Backend) ListDVDMenuScreenshots(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.ScreenshotImage, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := newWebDVDMenuRequest(path, overrides, nameOverrides, rt.baseUploadOptions())
	return wrapWebResult(rt.core.ListDVDMenuScreenshots(ctx, req))
}

// DeleteDVDMenuScreenshot removes one persisted image from the prepared
// release's managed directory and local records. Remote-host assets remain.
func (b *Backend) DeleteDVDMenuScreenshot(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, imagePath string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := newWebDVDMenuRequest(path, overrides, nameOverrides, rt.baseUploadOptions())
	return wrapWebError(rt.core.DeleteDVDMenuScreenshot(ctx, req, imagePath))
}

func newWebDVDMenuRequest(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, options api.UploadOptions) api.Request {
	return api.Request{
		Paths:                []string{strings.TrimSpace(path)},
		Mode:                 api.ModeGUI,
		Options:              options,
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
}

// runDVDMenuCaptureJob owns the queued-to-terminal lifecycle, releases its
// per-run resources, and schedules completed-job retention cleanup.
func (b *Backend) runDVDMenuCaptureJob(ctx context.Context, job *dvdMenuCaptureJob) {
	defer b.dvdMenuWG.Done()
	defer job.closeResources()
	if job == nil || job.core == nil {
		return
	}
	job.mu.Lock()
	job.status = "running"
	job.phase = "preflight"
	job.message = "Checking DVD menu capture support."
	job.mu.Unlock()
	b.emitDVDMenuCaptureSnapshot(job, true)

	progressCtx := api.WithDVDMenuProgressReporter(ctx, func(update api.DVDMenuProgressUpdate) {
		b.applyDVDMenuCaptureProgress(job, update)
	})
	req := newWebDVDMenuRequest(job.sourcePath, job.overrides, job.nameOverrides, job.uploadOptions)
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
	b.emitDVDMenuCaptureSnapshot(job, true)
	b.scheduleDVDMenuJobCleanup(job)
}

func (b *Backend) applyDVDMenuCaptureProgress(job *dvdMenuCaptureJob, update api.DVDMenuProgressUpdate) {
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
	b.emitDVDMenuCaptureSnapshot(job, false)
}

// emitDVDMenuCaptureSnapshot publishes a throttled session event; force
// bypasses throttling for queued and terminal transitions.
func (b *Backend) emitDVDMenuCaptureSnapshot(job *dvdMenuCaptureJob, force bool) {
	if b == nil || b.hub == nil || job == nil {
		return
	}
	job.mu.Lock()
	if !force && time.Since(job.lastEmit) < dvdMenuSnapshotThrottle {
		job.mu.Unlock()
		return
	}
	job.lastEmit = time.Now()
	job.mu.Unlock()
	b.hub.Emit(job.sessionID, "dvdmenu:job:"+job.id, buildWebDVDMenuCaptureSnapshot(job))
}

func buildWebDVDMenuCaptureSnapshot(job *dvdMenuCaptureJob) api.DVDMenuCaptureSnapshot {
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
		StartedAt:       formatTime(job.startedAt),
		FinishedAt:      formatTime(job.finishedAt),
	}
}

func (b *Backend) getDVDMenuCaptureJob(jobID string) *dvdMenuCaptureJob {
	if strings.TrimSpace(jobID) == "" {
		return nil
	}
	b.dvdMenuMu.Lock()
	defer b.dvdMenuMu.Unlock()
	return b.dvdMenus[jobID]
}

func (b *Backend) getDVDMenuCaptureJobForSession(sessionID string, jobID string) *dvdMenuCaptureJob {
	job := b.getDVDMenuCaptureJob(jobID)
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

// closeResources closes the per-run core and logger at most once.
func (job *dvdMenuCaptureJob) closeResources() {
	if job == nil {
		return
	}
	job.cleanupOnce.Do(func() {
		job.mu.Lock()
		coreSvc := job.core
		logger := job.logger
		job.core = nil
		job.logger = nil
		job.mu.Unlock()
		if coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger != nil {
			_ = logger.Close()
		}
	})
}

func (b *Backend) scheduleDVDMenuJobCleanup(job *dvdMenuCaptureJob) {
	b.dvdMenuMu.Lock()
	b.pruneCompletedDVDMenuJobsLocked(maxCompletedDVDMenuJobs)
	b.dvdMenuMu.Unlock()
	time.AfterFunc(completedJobRetention, func() {
		b.dvdMenuMu.Lock()
		defer b.dvdMenuMu.Unlock()
		if current := b.dvdMenus[job.id]; current == job && isDVDMenuJobTerminal(job) {
			delete(b.dvdMenus, job.id)
		}
	})
}

func (b *Backend) pruneCompletedDVDMenuJobsLocked(maxCompleted int) {
	if maxCompleted <= 0 {
		return
	}
	completed := make([]*dvdMenuCaptureJob, 0, len(b.dvdMenus))
	for _, job := range b.dvdMenus {
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
		delete(b.dvdMenus, job.id)
	}
}

func isDVDMenuJobTerminal(job *dvdMenuCaptureJob) bool {
	if job == nil {
		return false
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.status == "completed" || job.status == "failed" || job.status == "canceled"
}

func (b *Backend) stopAllDVDMenuJobs() {
	if b == nil {
		return
	}
	b.dvdMenuMu.Lock()
	jobs := make([]*dvdMenuCaptureJob, 0, len(b.dvdMenus))
	for _, job := range b.dvdMenus {
		jobs = append(jobs, job)
	}
	b.dvdMenuMu.Unlock()
	for _, job := range jobs {
		job.mu.Lock()
		cancel := job.cancel
		job.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	b.dvdMenuWG.Wait()
	for _, job := range jobs {
		job.closeResources()
	}
}
