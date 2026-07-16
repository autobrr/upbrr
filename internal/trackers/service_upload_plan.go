// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

const defaultMaxConcurrentTrackerPreparations = 4

// TrackerFailure is one normalized tracker-local preparation, record, or submission failure.
type TrackerFailure struct {
	// Tracker is the normalized tracker attributed to the failure.
	Tracker string
	// Code is the stable preparation, record, or submission failure class.
	Code string
	// Message contains sanitized operator-facing detail.
	Message string
	cause   error
}

// PartialUploadError reports tracker-local failures without discarding successful outcomes.
type PartialUploadError struct{ failures []TrackerFailure }

// Error returns a deterministic sanitized summary of tracker-local failures.
func (e *PartialUploadError) Error() string {
	if e == nil || len(e.failures) == 0 {
		return "trackers: partial upload failure"
	}
	parts := make([]string, 0, len(e.failures))
	for _, failure := range e.failures {
		parts = append(parts, failure.Tracker+": "+failure.Message)
	}
	return "trackers: " + strings.Join(parts, "; ")
}

// Failures returns a defensive copy in resolved tracker order.
func (e *PartialUploadError) Failures() []TrackerFailure {
	if e == nil {
		return nil
	}
	return append([]TrackerFailure(nil), e.failures...)
}

// TrackerLocalUploadFailures implements api.TrackerLocalUploadError without exposing private plan state.
func (e *PartialUploadError) TrackerLocalUploadFailures() []string {
	if e == nil {
		return nil
	}
	trackers := make([]string, 0, len(e.failures))
	for _, failure := range e.failures {
		trackers = append(trackers, failure.Tracker)
	}
	return trackers
}

// Unwrap returns diagnostic causes for tracker failures that retained one.
func (e *PartialUploadError) Unwrap() []error {
	if e == nil {
		return nil
	}
	causes := make([]error, 0, len(e.failures))
	for _, failure := range e.failures {
		if failure.cause != nil {
			causes = append(causes, failure.cause)
		}
	}
	return causes
}

// IsPartialUploadError reports whether err represents only tracker-local failures.
func IsPartialUploadError(err error) bool {
	var partial *PartialUploadError
	return errors.As(err, &partial)
}

type trackerPlanSlot struct {
	tracker                  string
	plan                     TrackerPlan
	failure                  *TrackerFailure
	summary                  api.UploadSummary
	canceledDuringSubmission bool
}

// Upload prepares every selected tracker, reaches a full barrier, then submits
// all ready plans concurrently. Tracker-local failures do not stop unrelated work.
func (s *Service) Upload(ctx context.Context, meta api.UploadSubject) (api.UploadSummary, error) {
	if err := ctx.Err(); err != nil {
		return api.UploadSummary{}, fmt.Errorf("context canceled: %w", err)
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return api.UploadSummary{}, errors.New("trackers: prepared release source is missing")
	}
	resolved := resolveTrackers(s.cfg, meta.Trackers, meta.TrackersRemove)
	if len(resolved) > 0 && s.registry == nil {
		return api.UploadSummary{}, errors.New("trackers: registry not configured")
	}
	resolved = filterKnownTrackersWithRegistry(resolved, s.logger, s.registry)
	resolved = filterTrackersByRuleFailures(resolved, meta.TrackerRuleFailures, meta.IgnoreTrackerRuleFailures, s.logger)
	resolved = filterTrackersByBlocks(resolved, meta.BlockedTrackers, s.logger)
	if len(resolved) == 0 {
		s.logger.Infof("trackers: no trackers configured, skipping upload")
		return api.UploadSummary{}, nil
	}
	if baseTorrent, err := ResolveUploadTorrentPath(meta, s.cfg.MainSettings.DBPath); err == nil {
		meta.TorrentPath = baseTorrent
	} else if !isUploadTorrentNotFound(err) {
		return api.UploadSummary{}, fmt.Errorf("trackers: shared upload torrent: %w", err)
	}

	preflight := s.preflightDescriptionImageHosts(ctx, meta, resolved)
	banned := s.prepareBannedState(ctx, meta, resolved)
	slots := s.prepareUploadPlans(ctx, meta, resolved, preflight, banned)
	if err := ctx.Err(); err != nil {
		s.releaseTrackerPlans(slots)
		for idx := range slots {
			emitTrackerPlanProgress(ctx, meta.SourcePath, slots[idx].tracker, "tracker_upload", "canceled", "Upload canceled before submission")
		}
		return summarizeTrackerPlanSlots(slots), fmt.Errorf("context canceled: %w", err)
	}

	s.createPendingRecords(ctx, meta, slots)
	s.submitTrackerPlans(ctx, meta, slots)
	s.releaseTrackerPlans(slots)

	summary := summarizeTrackerPlanSlots(slots)
	if err := ctx.Err(); err != nil && trackerPlansCanceled(slots) {
		return summary, fmt.Errorf("context canceled: %w", err)
	}
	failures := trackerPlanFailures(slots)
	if len(failures) > 0 {
		return summary, &PartialUploadError{failures: failures}
	}
	return summary, nil
}

func (s *Service) prepareBannedState(ctx context.Context, meta api.UploadSubject, resolved []string) map[string]*TrackerFailure {
	group := NormalizeBannedReleaseGroup(meta.Tag)
	if group == "" || s.banned == nil {
		return nil
	}
	failures := make(map[string]*TrackerFailure)
	for _, tracker := range resolved {
		if err := s.banned.RefreshDynamic(ctx, s.cfg, []string{tracker}, s.logger); err != nil {
			failures[normalizeTrackerName(tracker)] = &TrackerFailure{
				Tracker: tracker,
				Code:    "banned_check",
				Message: safeTrackerMessage(err),
				cause:   err,
			}
			continue
		}
		isBanned, err := s.banned.IsBanned(tracker, group)
		switch {
		case err != nil:
			failures[normalizeTrackerName(tracker)] = &TrackerFailure{
				Tracker: tracker,
				Code:    "banned_check",
				Message: safeTrackerMessage(err),
				cause:   err,
			}
		case isBanned:
			failures[normalizeTrackerName(tracker)] = &TrackerFailure{
				Tracker: tracker,
				Code:    "banned_group",
				Message: fmt.Sprintf("release group %s is banned", group),
				cause:   internalerrors.ErrBannedGroup,
			}
		}
	}
	return failures
}

func (s *Service) prepareUploadPlans(
	ctx context.Context,
	meta api.UploadSubject,
	resolved []string,
	preflight imageHostPreflight,
	banned map[string]*TrackerFailure,
) []trackerPlanSlot {
	slots := make([]trackerPlanSlot, len(resolved))
	workerCount := min(defaultMaxConcurrentTrackerPreparations, len(resolved))
	jobs := make(chan int)
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for idx := range jobs {
			tracker := resolved[idx]
			slot := trackerPlanSlot{tracker: tracker}
			emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "running", "Preparing tracker plan")
			if failure := banned[normalizeTrackerName(tracker)]; failure != nil {
				slot.failure = failure
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", failure.Message)
				continue
			}
			if err := ctx.Err(); err != nil {
				slot.failure = trackerFailure(tracker, "canceled", err)
				slots[idx] = slot
				continue
			}
			definition, ok := s.registry.Lookup(tracker)
			if !ok {
				slot.failure = trackerFailure(tracker, "not_implemented", internalerrors.ErrNotImplemented)
				slots[idx] = slot
				continue
			}
			trackerCfg := applyTrackerConfigOverrides(trackerConfigFor(s.cfg, tracker), meta.TrackerConfigOverrides)
			trackerMeta, err := PrepareTrackerUploadTorrentWithRegistry(meta, s.cfg.MainSettings.DBPath, tracker, trackerCfg, s.registry)
			if err != nil {
				slot.failure = trackerFailure(tracker, "artifact", err)
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
				continue
			}
			resolution, ok := preflight[normalizeTrackerName(tracker)]
			if !ok {
				resolution, err = ensureDescriptionImageHostWithRegistry(ctx, tracker, trackerMeta, s.cfg, trackerCfg, s.repo, s.images, s.logger, s.registry)
			}
			if err != nil {
				slot.failure = trackerFailure(tracker, "image_host", err)
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
				continue
			}
			if resolution.blocking {
				message := strings.TrimSpace(resolution.feedback.Message)
				if message == "" {
					message = "image-host requirements could not be met"
				}
				slot.failure = &TrackerFailure{
					Tracker: tracker,
					Code:    "image_host",
					Message: message,
				}
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", message)
				continue
			}
			assets, err := ResolveDescriptionAssets(ctx, tracker, trackerMeta, s.repo, s.logger, s.registry)
			if err != nil {
				slot.failure = trackerFailure(tracker, "description_assets", err)
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
				continue
			}
			applyResolvedDescriptionScreenshots(ctx, trackerMeta, s.repo, nil, &assets, resolution.screenshots)
			plan, failure := definition.Prepare(ctx, s.preparationInput(PreparationIntentUpload, tracker, trackerMeta, trackerCfg, &assets))
			if failure != nil {
				slot.failure = &TrackerFailure{
					Tracker: tracker,
					Code:    failure.Code(),
					Message: safeTrackerMessage(failure),
					cause:   failure,
				}
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
				continue
			}
			slot.plan = plan
			slots[idx] = slot
			emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "completed", "Tracker plan ready")
		}
	}
	for range workerCount {
		wg.Add(1)
		go worker()
	}
	next := 0
enqueue:
	for ; next < len(resolved); next++ {
		select {
		case jobs <- next:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	wg.Wait()
	for ; next < len(resolved); next++ {
		slots[next] = trackerPlanSlot{
			tracker: resolved[next],
			failure: trackerFailure(resolved[next], "canceled", ctx.Err()),
		}
	}
	return slots
}

func (s *Service) preparationInput(
	intent PreparationIntent,
	tracker string,
	meta api.UploadSubject,
	trackerCfg config.TrackerConfig,
	assets *DescriptionAssets,
) PreparationInput {
	input := PreparationInput{
		Intent:        intent,
		Tracker:       tracker,
		Meta:          meta,
		TrackerConfig: trackerCfg,
		Runtime: PreparationRuntime{
			DBPath:      s.cfg.MainSettings.DBPath,
			Description: s.cfg.Description,
			Internal:    IsInternalGroup(s.cfg, tracker, meta),
			BTNAPIToken: config.ResolveBTNAPIToken(s.cfg),
		},
		Logger: s.logger,
		Assets: assets,
	}
	selectedHost, err := PreferredImageUploadHostWithRegistry(s.registry, tracker, trackerCfg, meta.ImageHostOverrides)
	if err != nil {
		s.logger.Warnf("trackers: image upload target failed tracker=%s err=%s", tracker, redaction.RedactValue(err.Error(), nil))
		return input
	}
	input.SelectedImageHost = strings.ToLower(strings.TrimSpace(selectedHost))
	if s.images != nil && input.SelectedImageHost != "" {
		host := input.SelectedImageHost
		input.UploadImages = func(ctx context.Context, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return s.images.Upload(ctx, imageHostingSubject(meta), host, usageScopeForHost(s.registry, host), append([]api.ScreenshotImage(nil), images...))
		}
	}
	return input
}

func (s *Service) createPendingRecords(ctx context.Context, meta api.UploadSubject, slots []trackerPlanSlot) {
	if s.repo == nil {
		return
	}
	for idx := range slots {
		slot := &slots[idx]
		if slot.failure != nil || slot.plan.Intent() != PreparationIntentUpload {
			continue
		}
		status := "pending"
		if IsInternalGroup(s.cfg, slot.tracker, meta) {
			status = "pending-internal"
		}
		if err := s.repo.CreateUploadRecord(ctx, api.UploadRecord{
			Tracker:    slot.tracker,
			Status:     status,
			CreatedAt:  time.Now().UTC(),
			SourcePath: meta.SourcePath,
		}); err != nil {
			slot.failure = trackerFailure(slot.tracker, "record", err)
			if releaseErr := slot.plan.Release(); releaseErr != nil {
				s.warnPlanRelease(slot.tracker, releaseErr)
			}
			emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
			continue
		}
	}
}

func (s *Service) submitTrackerPlans(ctx context.Context, meta api.UploadSubject, slots []trackerPlanSlot) {
	ready := make([]int, 0, len(slots))
	for idx := range slots {
		if slots[idx].failure == nil && slots[idx].plan.Intent() == PreparationIntentUpload {
			ready = append(ready, idx)
		}
	}
	workerCount := s.maxConcurrentTrackerUploads(len(ready))
	if workerCount == 0 {
		return
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for idx := range jobs {
			slot := &slots[idx]
			if err := ctx.Err(); err != nil {
				slot.failure = trackerFailure(slot.tracker, "canceled", err)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "canceled")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "canceled", "Upload canceled")
				if releaseErr := slot.plan.Release(); releaseErr != nil {
					s.warnPlanRelease(slot.tracker, releaseErr)
				}
				continue
			}
			emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "running", "Uploading to tracker")
			summary, err := slot.plan.Submit(ctx)
			slot.canceledDuringSubmission = ctx.Err() != nil
			if err != nil {
				slot.failure = trackerFailure(slot.tracker, "submit", err)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "failed")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
			} else {
				slot.summary = summary
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "uploaded")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "completed", "Tracker upload complete")
			}
			if releaseErr := slot.plan.Release(); releaseErr != nil {
				s.warnPlanRelease(slot.tracker, releaseErr)
			}
		}
	}
	for range workerCount {
		wg.Add(1)
		go worker()
	}
	next := 0
enqueue:
	for ; next < len(ready); next++ {
		select {
		case jobs <- ready[next]:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	wg.Wait()
	for ; next < len(ready); next++ {
		slot := &slots[ready[next]]
		slot.failure = trackerFailure(slot.tracker, "canceled", ctx.Err())
		s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "canceled")
		emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "canceled", "Upload canceled")
	}
}

func (s *Service) releaseTrackerPlans(slots []trackerPlanSlot) {
	for idx := range slots {
		if err := slots[idx].plan.Release(); err != nil {
			s.warnPlanRelease(slots[idx].tracker, err)
		}
	}
}

func (s *Service) updateUploadRecord(parent context.Context, sourcePath string, tracker string, status string) {
	if s.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), uploadRecordFinalizationTimeout)
	defer cancel()
	err := s.repo.UpdateLatestUploadRecordStatus(ctx, sourcePath, tracker, status)
	if err != nil && ctx.Err() == nil {
		err = s.repo.UpdateLatestUploadRecordStatus(ctx, sourcePath, tracker, status)
	}
	if err != nil {
		s.logger.Warnf("trackers: status update failed tracker=%s status=%s err=%s", tracker, status, redaction.RedactValue(err.Error(), nil))
	}
}

func (s *Service) warnPlanRelease(tracker string, err error) {
	s.logger.Warnf("trackers: plan release failed tracker=%s err=%s", tracker, redaction.RedactValue(err.Error(), nil))
}

func summarizeTrackerPlanSlots(slots []trackerPlanSlot) api.UploadSummary {
	summary := api.UploadSummary{}
	for _, slot := range slots {
		summary.Uploaded += slot.summary.Uploaded
		summary.UploadedTorrents = append(summary.UploadedTorrents, slot.summary.UploadedTorrents...)
	}
	return summary
}

func trackerPlanFailures(slots []trackerPlanSlot) []TrackerFailure {
	failures := make([]TrackerFailure, 0)
	for _, slot := range slots {
		if slot.failure != nil && slot.failure.Code != "canceled" {
			failures = append(failures, *slot.failure)
		}
	}
	return failures
}

func trackerFailure(tracker string, code string, err error) *TrackerFailure {
	return &TrackerFailure{
		Tracker: tracker,
		Code:    code,
		Message: safeTrackerMessage(err),
		cause:   err,
	}
}

func trackerPlansCanceled(slots []trackerPlanSlot) bool {
	for _, slot := range slots {
		if slot.canceledDuringSubmission || slot.failure != nil && slot.failure.Code == "canceled" {
			return true
		}
	}
	return false
}

func safeTrackerMessage(err error) string {
	if err == nil {
		return "tracker operation failed"
	}
	message := strings.TrimSpace(redaction.RedactValue(err.Error(), nil))
	if message == "" {
		return "tracker operation failed"
	}
	return message
}

func normalizeTrackerName(tracker string) string { return strings.ToUpper(strings.TrimSpace(tracker)) }

func emitTrackerPlanProgress(ctx context.Context, sourcePath string, tracker string, task string, status string, message string) {
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: sourcePath,
		Tracker:    tracker,
		Task:       task,
		Status:     status,
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
	})
}
