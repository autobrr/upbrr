// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/logging"
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

type trackerPlanSlot struct {
	tracker                  string
	torrentPath              string
	plan                     TrackerPlan
	failure                  *TrackerFailure
	resolving                bool
	summary                  api.UploadSummary
	ruleFailures             []api.TrackerRuleFailure
	canceledDuringSubmission bool
}

// Upload prepares every selected tracker, reaches a full barrier, then submits
// ready plans concurrently. Tracker-local failures do not stop unrelated work
// and return a partial summary with [PartialUploadError]; cancellation returns
// completed uploads with the context error. Pending record finalization uses a
// bounded context detached from caller cancellation.
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
	resolved = filterTrackersByBlocks(resolved, meta.BlockedTrackers, s.logger)
	if len(resolved) == 0 {
		s.logger.Infof("trackers: no trackers configured, skipping upload")
		return api.UploadSummary{}, nil
	}
	if baseTorrent, err := resolveUploadTorrentBasePath(meta, s.cfg.MainSettings.DBPath); err == nil {
		meta.TorrentPath = baseTorrent
	} else if !isUploadTorrentNotFound(err) {
		return api.UploadSummary{}, fmt.Errorf("trackers: shared upload torrent: %w", err)
	}

	preflight := s.preflightDescriptionImageHosts(ctx, meta, resolved)
	banned := s.prepareBannedState(ctx, meta, resolved)
	slots := s.prepareUploadPlans(ctx, meta, resolved, preflight, banned, nil)
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
	projections map[string]api.TrackerReleaseProjection,
) []trackerPlanSlot {
	slots := make([]trackerPlanSlot, len(resolved))
	workerCount := min(defaultMaxConcurrentTrackerPreparations, len(resolved))
	jobs := make(chan int)
	var wg sync.WaitGroup
	worker := func() {
		for idx := range jobs {
			tracker := resolved[idx]
			slot := trackerPlanSlot{tracker: tracker}
			if projection, ok := projections[normalizeTrackerName(tracker)]; ok {
				slot.ruleFailures = projectedRuleFailureRecords(projection)
			}
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
			if projection, ok := projections[normalizeTrackerName(tracker)]; ok && projection.ProjectorFingerprint != "" {
				descriptor, descriptorOK := s.registry.LookupDescriptor(tracker)
				recordedPolicyID := projectedReleaseNamePolicyID(projection)
				_, instructionOK := projectedRequestedReleaseName(projection)
				if !descriptorOK || recordedPolicyID == "" || recordedPolicyID != descriptor.ReleaseNamePolicy.ID || !instructionOK {
					slot.failure = &TrackerFailure{
						Tracker: tracker,
						Code:    "name_policy_stale",
						Message: "reviewed tracker release-name policy is stale",
					}
					slots[idx] = slot
					emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
					continue
				}
			}
			trackerCfg := applyTrackerConfigOverrides(trackerConfigFor(s.cfg, tracker), meta.TrackerConfigOverrides)
			trackerMeta, err := prepareTrackerUploadTorrentWithRegistry(meta, s.cfg.MainSettings.DBPath, tracker, trackerCfg, s.registry)
			if err != nil {
				slot.failure = trackerFailure(tracker, "artifact", err)
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
				continue
			}
			content := s.prepareUploadContent(ctx, tracker, trackerMeta, trackerCfg, nil, preflight)
			if content.State == preparedUploadContentFailed {
				slot.failure = &TrackerFailure{
					Tracker: tracker,
					Code:    string(content.Failure.Code),
					Message: content.Failure.Message,
				}
				slots[idx] = slot
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "failed", slot.failure.Message)
				continue
			}
			input := s.preparationInput(ctx, PreparationIntentUpload, tracker, trackerMeta, trackerCfg, content.Assets)
			if projection, ok := projections[normalizeTrackerName(tracker)]; ok {
				projected := projection
				input.Projection = &projected
				if requestedName, provenanceOK := projectedRequestedReleaseName(projected); provenanceOK {
					input.RequestedUploadName = requestedName
				}
			}
			plan, failure := definition.Prepare(ctx, input)
			if failure != nil {
				slot.failure = &TrackerFailure{
					Tracker: tracker,
					Code:    failure.Code(),
					Message: safeTrackerMessage(failure),
					cause:   failure,
				}
				slots[idx] = slot
				status := "failed"
				if slot.failure.Code == PreparationFailureCodeSkipped {
					status = "skipped"
				}
				emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", status, slot.failure.Message)
				continue
			}
			slot.torrentPath = trackerMeta.TorrentPath
			slot.plan = plan
			slots[idx] = slot
			emitTrackerPlanProgress(ctx, meta.SourcePath, tracker, "tracker_preparation", "completed", "Tracker plan ready")
		}
	}
	for range workerCount {
		wg.Go(worker)
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

// RetainedTrackerPreparation is one process-local view of a private prepared tracker operation.
type RetainedTrackerPreparation struct {
	Tracker string
	// TorrentPath is the exact prepared upload artifact used by preview/debug
	// injection and tracker submission. Live post-submit injection must use the
	// registered artifact returned by the tracker.
	TorrentPath string
	Preview     api.TrackerDryRunEntry
	Failure     *TrackerFailure
}

// RetainedTrackerResult is one tracker outcome produced by a retained operation.
type RetainedTrackerResult struct {
	Tracker string
	Summary api.UploadSummary
	Failure *TrackerFailure
}

// RetainedUploadPlan owns exact single-use tracker plans produced by one
// preparation barrier. It is process-local execution authority.
type RetainedUploadPlan struct {
	service  *Service
	meta     api.UploadSubject
	slots    []trackerPlanSlot
	mu       sync.Mutex
	executed bool
	released bool
}

// PrepareRetainedUploadPlan builds submittable operations from exact reviewed projections.
func (s *Service) PrepareRetainedUploadPlan(
	ctx context.Context,
	meta api.UploadSubject,
	projections []api.TrackerReleaseProjection,
) (*RetainedUploadPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: retained upload preparation: %w", err)
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return nil, errors.New("trackers: retained upload preparation source is missing")
	}
	if s == nil || s.registry == nil {
		return nil, errors.New("trackers: retained upload preparation registry is unavailable")
	}
	resolved := make([]string, 0, len(projections))
	projectionByTracker := make(map[string]api.TrackerReleaseProjection, len(projections))
	for _, projection := range projections {
		tracker := normalizeTrackerName(string(projection.TrackerID))
		if tracker == "" || !projection.UploadReady || projection.Readiness != api.ReadinessStatusReady {
			continue
		}
		if _, exists := projectionByTracker[tracker]; exists {
			return nil, fmt.Errorf("trackers: retained upload preparation contains duplicate tracker %s", tracker)
		}
		resolved = append(resolved, tracker)
		projectionByTracker[tracker] = projection
	}
	if len(resolved) == 0 {
		return nil, errors.New("trackers: retained upload preparation has no eligible projections")
	}
	meta.Trackers = append([]string(nil), resolved...)
	if baseTorrent, err := resolveUploadTorrentBasePath(meta, s.cfg.MainSettings.DBPath); err == nil {
		meta.TorrentPath = baseTorrent
	} else if !isUploadTorrentNotFound(err) {
		return nil, fmt.Errorf("trackers: retained upload shared torrent: %w", err)
	}
	preflight := s.preflightDescriptionImageHosts(ctx, meta, resolved)
	slots := s.prepareUploadPlans(ctx, meta, resolved, preflight, nil, projectionByTracker)
	if err := ctx.Err(); err != nil {
		s.releaseTrackerPlans(slots)
		return nil, fmt.Errorf("trackers: retained upload preparation: %w", err)
	}
	return &RetainedUploadPlan{
		service: s,
		meta:    meta,
		slots:   slots,
	}, nil
}

// Preparations returns detached sanitized-preparation source values. Callers
// must sanitize the payload projection before transport exposure.
func (p *RetainedUploadPlan) Preparations() []RetainedTrackerPreparation {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]RetainedTrackerPreparation, 0, len(p.slots))
	for _, slot := range p.slots {
		result = append(result, retainedTrackerPreparation(slot))
	}
	return result
}

// ResolveAction updates one retained tracker operation without rebuilding
// unrelated tracker plans.
func (p *RetainedUploadPlan) ResolveAction(
	ctx context.Context,
	tracker string,
	kind api.RequiredActionKind,
	confirmed bool,
) (RetainedTrackerPreparation, error) {
	if p == nil {
		return RetainedTrackerPreparation{}, ErrPlanNotSubmittable
	}
	tracker = normalizeTrackerName(tracker)
	if tracker == "" {
		return RetainedTrackerPreparation{}, fmt.Errorf("%w: tracker is required", ErrPlanActionUnavailable)
	}
	p.mu.Lock()
	switch {
	case p.released:
		p.mu.Unlock()
		return RetainedTrackerPreparation{}, ErrPlanReleased
	case p.executed:
		p.mu.Unlock()
		return RetainedTrackerPreparation{}, ErrPlanAlreadyUsed
	}
	index := slices.IndexFunc(p.slots, func(slot trackerPlanSlot) bool {
		return normalizeTrackerName(slot.tracker) == tracker
	})
	if index < 0 || p.slots[index].failure != nil || p.slots[index].resolving {
		p.mu.Unlock()
		return RetainedTrackerPreparation{}, fmt.Errorf("%w: tracker %s", ErrPlanActionUnavailable, tracker)
	}
	p.slots[index].resolving = true
	plan := p.slots[index].plan
	p.mu.Unlock()

	resolved, err := plan.ResolveAction(ctx, kind, confirmed)
	p.mu.Lock()
	p.slots[index].resolving = false
	switch {
	case p.released:
		p.mu.Unlock()
		if err == nil {
			_ = resolved.Release()
		}
		return RetainedTrackerPreparation{}, ErrPlanReleased
	case p.executed:
		p.mu.Unlock()
		if err == nil {
			_ = resolved.Release()
		}
		return RetainedTrackerPreparation{}, ErrPlanAlreadyUsed
	}
	if err != nil {
		var failure *PreparationFailure
		if !errors.As(err, &failure) {
			p.mu.Unlock()
			return RetainedTrackerPreparation{}, err
		}
		p.slots[index].plan = TrackerPlan{}
		p.slots[index].failure = &TrackerFailure{
			Tracker: tracker,
			Code:    failure.Code(),
			Message: safeTrackerMessage(failure),
			cause:   failure,
		}
		preparation := retainedTrackerPreparation(p.slots[index])
		p.mu.Unlock()
		return preparation, nil
	}
	p.slots[index].plan = resolved
	p.slots[index].failure = nil
	preparation := retainedTrackerPreparation(p.slots[index])
	p.mu.Unlock()
	return preparation, nil
}

func retainedTrackerPreparation(slot trackerPlanSlot) RetainedTrackerPreparation {
	preparation := RetainedTrackerPreparation{Tracker: slot.tracker, TorrentPath: slot.torrentPath}
	if slot.failure != nil {
		failure := *slot.failure
		preparation.Failure = &failure
	} else {
		preparation.Preview = slot.plan.DryRun()
	}
	return preparation
}

// Execute consumes all retained tracker plans without rebuilding payloads.
func (p *RetainedUploadPlan) Execute(ctx context.Context) ([]RetainedTrackerResult, error) {
	return p.execute(ctx, nil)
}

// ExecuteSelected consumes only the named retained tracker plans without
// rebuilding payloads. Unselected plans are released without submission.
func (p *RetainedUploadPlan) ExecuteSelected(
	ctx context.Context,
	trackerIDs []string,
) ([]RetainedTrackerResult, error) {
	if len(trackerIDs) == 0 {
		return nil, fmt.Errorf("%w: selected trackers are required", ErrPlanNotSubmittable)
	}
	return p.execute(ctx, trackerIDs)
}

func (p *RetainedUploadPlan) execute(
	ctx context.Context,
	trackerIDs []string,
) ([]RetainedTrackerResult, error) {
	if p == nil || p.service == nil {
		return nil, ErrPlanNotSubmittable
	}
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return nil, ErrPlanReleased
	}
	if p.executed {
		p.mu.Unlock()
		return nil, ErrPlanAlreadyUsed
	}
	if slices.ContainsFunc(p.slots, func(slot trackerPlanSlot) bool { return slot.resolving }) {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: tracker action resolution is in progress", ErrPlanActionUnavailable)
	}
	selectedSlots, unselectedSlots, err := selectRetainedTrackerPlanSlots(p.slots, trackerIDs)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	p.executed = true
	p.mu.Unlock()

	p.service.releaseTrackerPlans(unselectedSlots)
	p.service.createPendingRecords(ctx, p.meta, selectedSlots)
	p.service.submitTrackerPlans(ctx, p.meta, selectedSlots)
	p.service.releaseTrackerPlans(selectedSlots)
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()

	results := make([]RetainedTrackerResult, 0, len(selectedSlots))
	for _, slot := range selectedSlots {
		result := RetainedTrackerResult{Tracker: slot.tracker, Summary: slot.summary}
		if slot.failure != nil {
			failure := *slot.failure
			result.Failure = &failure
		}
		results = append(results, result)
	}
	if err := ctx.Err(); err != nil && trackerPlansCanceled(selectedSlots) {
		return results, fmt.Errorf("trackers: retained upload execution: %w", err)
	}
	return results, nil
}

func selectRetainedTrackerPlanSlots(
	slots []trackerPlanSlot,
	trackerIDs []string,
) ([]trackerPlanSlot, []trackerPlanSlot, error) {
	if len(trackerIDs) == 0 {
		return append([]trackerPlanSlot(nil), slots...), nil, nil
	}
	requested := make(map[string]struct{}, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		trackerID = normalizeTrackerName(trackerID)
		if trackerID == "" {
			return nil, nil, fmt.Errorf("%w: selected tracker is empty", ErrPlanNotSubmittable)
		}
		if _, duplicate := requested[trackerID]; duplicate {
			return nil, nil, fmt.Errorf("%w: selected tracker %s is duplicated", ErrPlanNotSubmittable, trackerID)
		}
		requested[trackerID] = struct{}{}
	}
	selected := make([]trackerPlanSlot, 0, len(requested))
	unselected := make([]trackerPlanSlot, 0, len(slots)-len(requested))
	for _, slot := range slots {
		if _, exists := requested[normalizeTrackerName(slot.tracker)]; exists {
			selected = append(selected, slot)
			delete(requested, normalizeTrackerName(slot.tracker))
		} else {
			unselected = append(unselected, slot)
		}
	}
	if len(requested) > 0 {
		missing := make([]string, 0, len(requested))
		for trackerID := range requested {
			missing = append(missing, trackerID)
		}
		slices.Sort(missing)
		return nil, nil, fmt.Errorf("%w: selected tracker plans are unavailable: %s", ErrPlanNotSubmittable, strings.Join(missing, ", "))
	}
	return selected, unselected, nil
}

// Release invalidates unconsumed retained plans and frees adapter resources.
func (p *RetainedUploadPlan) Release() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return nil
	}
	p.released = true
	plans := make([]TrackerPlan, len(p.slots))
	for index := range p.slots {
		plans[index] = p.slots[index].plan
	}
	p.mu.Unlock()
	var errs []error
	for _, plan := range plans {
		if err := plan.Release(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) preparationInput(
	ctx context.Context,
	intent PreparationIntent,
	tracker string,
	meta api.UploadSubject,
	trackerCfg config.TrackerConfig,
	assets *DescriptionAssets,
) PreparationInput {
	logger := logging.FromContext(ctx, s.logger)
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
		Logger: logger,
		Assets: assets,
	}
	selectedHost, err := PreferredImageUploadHostWithRegistry(s.registry, tracker, trackerCfg, meta.ImageHostOverrides)
	if err != nil {
		logger.Warnf("trackers: image upload target failed tracker=%s err=%s", tracker, redaction.RedactValue(err.Error(), nil))
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
		if slot.ruleFailures != nil {
			if err := s.repo.SaveTrackerRuleFailures(ctx, meta.SourcePath, slot.tracker, slot.ruleFailures); err != nil {
				slot.failure = trackerFailure(slot.tracker, "rule_history", err)
				if releaseErr := slot.plan.Release(); releaseErr != nil {
					s.warnPlanRelease(slot.tracker, releaseErr)
				}
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
				continue
			}
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

// projectedRuleFailureRecords converts rule decisions into persisted audit
// rows. A waivable decision is marked authorized only when both projection
// fingerprints match exactly. It returns nil when no decision qualifies.
func projectedRuleFailureRecords(projection api.TrackerReleaseProjection) []api.TrackerRuleFailure {
	records := make([]api.TrackerRuleFailure, 0, len(projection.PolicyDecisions))
	authorized := projection.RuleAuthorizationFingerprint != "" &&
		projection.RuleAuthorizationFingerprint == projection.WaivableRuleFingerprint
	for _, decision := range projection.PolicyDecisions {
		if strings.TrimSpace(decision.Code) == "" || decision.Disposition == "" {
			continue
		}
		disposition := api.NormalizeRuleDisposition(decision.Disposition)
		records = append(records, api.TrackerRuleFailure{
			Tracker:     string(projection.TrackerID),
			Rule:        strings.TrimSpace(decision.Code),
			Reason:      strings.TrimSpace(decision.Message),
			Disposition: disposition,
			Authorized:  disposition == api.RuleDispositionWaivable && authorized,
		})
	}
	if len(records) == 0 {
		return nil
	}
	return records
}

// submitTrackerPlans finishes reusable-base uploads first, then waits the
// configured rehash cooldown before starting rehash-dependent uploads.
func (s *Service) submitTrackerPlans(ctx context.Context, meta api.UploadSubject, slots []trackerPlanSlot) {
	ready, delayedStart := orderedReadyTrackerPlanIndexes(slots, meta.RehashedTrackers)
	workerCount := s.maxConcurrentTrackerUploads(len(ready))
	if workerCount == 0 {
		return
	}
	jobs := make(chan int)
	var phase sync.WaitGroup
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
				phase.Done()
				continue
			}
			emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "running", "Uploading to tracker")
			effectFingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(struct {
				Tracker string
				Preview api.TrackerDryRunEntry
			}{
				Tracker: slot.tracker,
				Preview: slot.plan.DryRun(),
			})
			if fingerprintErr != nil {
				slot.failure = trackerFailure(slot.tracker, "effect_fence", fingerprintErr)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "failed")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
				phase.Done()
				continue
			}
			effectReceipt, effectErr := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
				Kind:                api.WorkflowExternalEffectTrackerSubmission,
				ScopeID:             slot.tracker,
				SemanticFingerprint: effectFingerprint,
			})
			if effectErr != nil {
				code := "effect_fence"
				if errors.Is(effectErr, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
					code = "unknown_outcome"
				}
				slot.failure = trackerFailure(slot.tracker, code, effectErr)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, code)
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
				phase.Done()
				continue
			}
			if effectReceipt.AlreadySucceeded {
				slot.failure = trackerFailure(slot.tracker, "unknown_outcome", api.ErrReleaseWorkflowEffectAlreadySucceeded)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "unknown_outcome")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
				phase.Done()
				continue
			}
			summary, err := slot.plan.Submit(ctx)
			slot.canceledDuringSubmission = ctx.Err() != nil
			receiptErr := api.CompleteWorkflowExternalEffect(ctx, effectReceipt, err == nil)
			if receiptErr != nil {
				slot.failure = trackerFailure(slot.tracker, "unknown_outcome", receiptErr)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "unknown_outcome")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
				phase.Done()
				continue
			}
			if err != nil {
				slot.failure = trackerFailure(slot.tracker, "submit", err)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "failed")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "failed", slot.failure.Message)
			} else {
				slot.summary = summary
				if summary.PendingPublication {
					s.logger.Infof(
						"trackers: upload tracker=%s state=pending_publication decision=defer_client_injection count=%d",
						slot.tracker,
						summary.Uploaded,
					)
				}
				s.logUploadedTorrentURLs(slot.tracker, summary)
				s.updateUploadRecord(ctx, meta.SourcePath, slot.tracker, "uploaded")
				emitTrackerPlanProgress(ctx, meta.SourcePath, slot.tracker, "tracker_upload", "completed", "Tracker upload complete")
			}
			if releaseErr := slot.plan.Release(); releaseErr != nil {
				s.warnPlanRelease(slot.tracker, releaseErr)
			}
			phase.Done()
		}
	}
	for range workerCount {
		wg.Add(1)
		go worker()
	}
	next := 0
enqueue:
	for ; next < len(ready); next++ {
		if next == delayedStart && delayedStart < len(ready) {
			cooldown := time.Duration(max(s.cfg.TorrentCreation.RehashCooldown, 0)) * time.Second
			s.logger.Infof("trackers: rehash uploads queued last count=%d cooldown=%s", len(ready)-delayedStart, cooldown)
			if delayedStart > 0 {
				phase.Wait()
			}
			if delayedStart > 0 && cooldown > 0 {
				timer := time.NewTimer(cooldown)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					break enqueue
				}
			}
		}
		phase.Add(1)
		select {
		case jobs <- ready[next]:
		case <-ctx.Done():
			phase.Done()
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

// orderedReadyTrackerPlanIndexes stably partitions ready upload plans so
// reusable-base plans precede rehash-dependent plans. delayedStart is the first
// rehash-dependent index in the returned slice.
func orderedReadyTrackerPlanIndexes(slots []trackerPlanSlot, rehashedTrackers []string) ([]int, int) {
	rehashed := make(map[string]struct{}, len(rehashedTrackers))
	for _, tracker := range rehashedTrackers {
		if tracker = normalizeTrackerName(tracker); tracker != "" {
			rehashed[tracker] = struct{}{}
		}
	}
	ready := make([]int, 0, len(slots))
	delayed := make([]int, 0, len(rehashed))
	for idx := range slots {
		if slots[idx].failure != nil || slots[idx].plan.Intent() != PreparationIntentUpload {
			continue
		}
		if _, ok := rehashed[normalizeTrackerName(slots[idx].tracker)]; ok {
			delayed = append(delayed, idx)
		} else {
			ready = append(ready, idx)
		}
	}
	delayedStart := len(ready)
	ready = append(ready, delayed...)
	return ready, delayedStart
}

func (s *Service) logUploadedTorrentURLs(fallbackTracker string, summary api.UploadSummary) {
	for _, uploaded := range summary.UploadedTorrents {
		torrentURL := sanitizeTorrentPageURLForLog(uploaded.TorrentURL)
		if torrentURL == "" {
			continue
		}
		tracker := strings.TrimSpace(uploaded.Tracker)
		if tracker == "" {
			tracker = fallbackTracker
		}
		s.logger.Infof("trackers: %s torrent URL: %s", tracker, torrentURL)
	}
}

func sanitizeTorrentPageURLForLog(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}

	publicQuery := url.Values{}
	for key, values := range parsed.Query() {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "hash", "id", "torrentid":
			publicQuery[key] = append([]string(nil), values...)
		case "page":
			for _, queryValue := range values {
				if strings.EqualFold(strings.TrimSpace(queryValue), "torrent-details") {
					publicQuery[key] = append(publicQuery[key], queryValue)
				}
			}
		case "uploaded":
			for _, queryValue := range values {
				if strings.TrimSpace(queryValue) == "1" {
					publicQuery[key] = append(publicQuery[key], queryValue)
				}
			}
		}
	}
	parsed.RawQuery = publicQuery.Encode()
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
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
		summary.PendingPublication = summary.PendingPublication || slot.summary.PendingPublication
	}
	return summary
}

func trackerPlanFailures(slots []trackerPlanSlot) []TrackerFailure {
	failures := make([]TrackerFailure, 0)
	for _, slot := range slots {
		if slot.failure != nil && slot.failure.Code != "canceled" && slot.failure.Code != PreparationFailureCodeSkipped {
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
	completed := 0
	percent := 0
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "canceled", "cancelled", "skipped":
		completed = 1
		percent = 100
	}
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      sourcePath,
		Tracker:         tracker,
		Task:            task,
		Status:          status,
		Message:         message,
		CompletedPieces: completed,
		TotalPieces:     1,
		Percent:         percent,
		Timestamp:       time.Now().UTC().Format(time.RFC3339Nano),
	})
}
