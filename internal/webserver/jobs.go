// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/webserver/jobs"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	seedPreparedMetaTimeout             = 30 * time.Second
	trackerUploadSnapshotThrottle       = 200 * time.Millisecond
	trackerUploadHashRateEmitDeltaMiB   = 1.0
	trackerUploadProgressEvent          = "upload:progress"
	errDupeCheckRequiresMetadataPreview = "dupe check requires metadata preview"
)

type preparedUploadRunner interface {
	RunAcceptedUpload(context.Context, api.UploadExecutionPlan) (api.Result, error)
}

type dupeCheckRunner interface {
	CheckAcceptedDupes(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error)
}

// DupeCheckTrackerState aliases the shared frontend-visible state for one duplicate-check tracker.
type DupeCheckTrackerState = jobs.DupeCheckTrackerState

// DupeCheckSnapshot aliases the shared frontend-visible duplicate-check job snapshot.
type DupeCheckSnapshot = jobs.DupeCheckSnapshot

// TrackerUploadTrackerState aliases the shared frontend-visible state for one tracker upload.
type TrackerUploadTrackerState = jobs.TrackerUploadTrackerState

// TrackerUploadSnapshot aliases the shared frontend-visible tracker-upload job snapshot.
type TrackerUploadSnapshot = jobs.TrackerUploadSnapshot

// webJobSink routes shared snapshots to the authenticated owner's event stream.
type webJobSink struct {
	hub    *eventHub
	logger api.Logger
}

// EmitDupe emits a duplicate-check snapshot only to the owning session.
func (s webJobSink) EmitDupe(ownerID string, snapshot jobs.DupeCheckSnapshot) {
	if s.hub != nil {
		s.hub.Emit(ownerID, "dupe:job:"+snapshot.JobID, snapshot)
		s.hub.Emit(ownerID, "jobs:update", jobs.OwnerJobSnapshot{
			Kind:          jobs.KindDuplicateCheck,
			JobID:         snapshot.JobID,
			CorrelationID: snapshot.CorrelationID,
			Release:       snapshot.Release,
			Status:        snapshot.Status,
			StartedAt:     snapshot.StartedAt,
			FinishedAt:    snapshot.FinishedAt,
			Dupe:          &snapshot,
		})
	}
}

// EmitUpload emits a tracker-upload snapshot only to the owning session.
func (s webJobSink) EmitUpload(ownerID string, snapshot jobs.TrackerUploadSnapshot) {
	if s.hub != nil {
		s.hub.Emit(ownerID, "upload:job:"+snapshot.JobID, snapshot)
		s.hub.Emit(ownerID, "jobs:update", jobs.OwnerJobSnapshot{
			Kind:          jobs.KindTrackerUpload,
			JobID:         snapshot.JobID,
			CorrelationID: snapshot.CorrelationID,
			RetryOf:       snapshot.RetryOf,
			Release:       snapshot.Release,
			Status:        snapshot.Status,
			StartedAt:     snapshot.StartedAt,
			FinishedAt:    snapshot.FinishedAt,
			Upload:        &snapshot,
		})
	}
}

// WarnJob records a sanitized lifecycle warning through the event hub logger.
func (s webJobSink) WarnJob(message string) {
	if s.logger != nil {
		s.logger.Warnf("jobs: %s", message)
	}
}

func (b *Backend) ensureJobOwner(sessionID string) (*jobs.OwnerHandle, error) {
	if b == nil {
		return nil, jobs.ErrEngineClosed
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	b.jobOwnerMu.Lock()
	defer b.jobOwnerMu.Unlock()
	if b.jobEngine == nil {
		b.jobEngine = jobs.New(webJobSink{hub: b.hub, logger: b.currentLogger()}, jobs.Config{})
	}
	if b.jobOwners == nil {
		b.jobOwners = make(map[string]*jobs.OwnerHandle)
	}
	if owner := b.jobOwners[sessionID]; owner != nil {
		return owner, nil
	}
	owner, err := b.jobEngine.RegisterOwner(sessionID)
	if err != nil {
		return nil, fmt.Errorf("register job owner: %w", err)
	}
	b.jobOwners[sessionID] = owner
	return owner, nil
}

func (b *Backend) lookupJobOwner(sessionID string) *jobs.OwnerHandle {
	if b == nil {
		return nil
	}
	b.jobOwnerMu.Lock()
	defer b.jobOwnerMu.Unlock()
	return b.jobOwners[strings.TrimSpace(sessionID)]
}

func (b *Backend) removeJobOwner(sessionID string) {
	if b == nil || b.jobEngine == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	b.jobOwnerMu.Lock()
	owner := b.jobOwners[sessionID]
	delete(b.jobOwners, sessionID)
	b.jobOwnerMu.Unlock()
	if owner != nil {
		_ = b.jobEngine.RemoveOwner(owner)
	}
	if b.reviews != nil {
		b.reviews.PurgeOwner(sessionID)
	}
}

// webUploadRunner preserves the embedded-web error prefix around prepared upload execution.
type webUploadRunner struct {
	core             preparedUploadRunner
	generationTarget PreparedGenerationTransfer
	generationSeed   preparedrelease.Seed
	expectedRelease  api.ReleaseRef
}

// RunUpload delegates prepared upload execution and applies the embedded-web error contract.
func (r webUploadRunner) RunUpload(ctx context.Context, plan api.UploadExecutionPlan) (api.Result, error) {
	ref, err := r.generationTarget.ImportReleaseSeed(ctx, r.generationSeed)
	if err != nil {
		return api.Result{}, fmt.Errorf("web: import canonical generation: %w", err)
	}
	if ref != r.expectedRelease {
		return api.Result{}, errors.New("web: import canonical generation: unexpected release reference")
	}
	return wrapWebResult(r.core.RunAcceptedUpload(ctx, plan))
}

// webDupeRunner preserves the direct duplicate-check error contract used by both transports.
type webDupeRunner struct {
	core             dupeCheckRunner
	generationTarget PreparedGenerationTransfer
	generationSeed   preparedrelease.Seed
	expectedRelease  api.ReleaseRef
}

// CheckDupes delegates duplicate checking without changing runner error text.
func (r webDupeRunner) CheckDupes(ctx context.Context, input api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	ref, err := r.generationTarget.ImportReleaseSeed(ctx, r.generationSeed)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("import canonical generation: %w", err)
	}
	if ref != r.expectedRelease {
		return api.DupeCheckSummary{}, errors.New("import canonical generation: unexpected release reference")
	}
	summary, err := r.core.CheckAcceptedDupes(ctx, input)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("%w", err)
	}
	return summary, nil
}

// normalizeTrackerList trims, removes blanks, and deduplicates tracker names case-insensitively.
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

// cloneQuestionnaireAnswers deep-copies tracker questionnaire maps for request isolation.
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

// normalizePatterns trims, removes blanks, and deduplicates log exclusion patterns.
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

// StartDupeCheck starts a session-owned duplicate-check job for a host filesystem path.
// Request cancellation applies only to preview-cache preparation; accepted jobs continue until completion or explicit cancellation.
func (b *Backend) StartDupeCheck(
	ctx context.Context,
	sessionID string,
	release api.ReleaseRef,
	trackers []string,
	correlationID string,
) (string, error) {
	owner, err := b.ensureJobOwner(sessionID)
	if err != nil {
		return "", err
	}
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	if !rt.capabilities.PreparedGenerationReady() {
		return "", ErrPreparedGenerationUnavailable
	}
	if ctx == nil {
		return "", errors.New("request context is required")
	}
	release.SourcePath = strings.TrimSpace(release.SourcePath)
	if release.SourcePath == "" || release.Generation == 0 {
		return "", api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureInvalidSource,
			Operation: api.OperationKindDuplicateCheck,
			Message:   "Source path is required.",
			Recovery:  api.OperationRecoveryEditInput,
		}, errors.New("path is required"))
	}
	resolvedTrackers := normalizeTrackerList(trackers)
	if len(resolvedTrackers) == 0 {
		return "", api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureNoEligibleTrackers,
			Operation: api.OperationKindDuplicateCheck,
			Message:   "No trackers are eligible for duplicate checking.",
			Recovery:  api.OperationRecoverySelectTrackers,
		}, errors.New("at least one tracker must be selected"))
	}

	generationSeed, err := rt.capabilities.PreparedGenerationTransfer.ExportReleaseSeed(ctx, release)
	if err != nil {
		return "", fmt.Errorf("dupe check exact generation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("dupe check canceled before run setup: %w", err)
	}

	runCapabilities, runOwner, runLogger, err := b.buildRunCoreFromSnapshot(ctx, rt, runOptions{})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		closeJobResources(runOwner, runLogger)
		return "", fmt.Errorf("dupe check metadata preview cache: %w", err)
	}
	if !runCapabilities.PreparedDupeReady() || !runCapabilities.PreparedGenerationReady() {
		closeJobResources(runOwner, runLogger)
		return "", ErrPreparedDupeUnavailable
	}
	jobID, err := b.jobEngine.StartDupe(context.WithoutCancel(ctx), owner, jobs.DupeSpec{
		CorrelationID: correlationID,
		Snapshot: jobs.DuplicateExecutionSnapshot{
			Seed:               generationSeed,
			PreparedGeneration: release.Generation,
			RuntimeGeneration:  rt.generationID,
			Input: api.DuplicateCheckInput{
				Release:     release,
				Trackers:    append([]string(nil), resolvedTrackers...),
				Interaction: api.InteractionModeInteractive,
			},
		},
		Runner: webDupeRunner{
			core:             runCapabilities.DuplicateExecution,
			generationTarget: runCapabilities.PreparedGenerationTransfer,
			generationSeed:   generationSeed,
			expectedRelease:  release,
		},
		Resources: jobs.Resources{Core: runOwner, Logger: runLogger},
	})
	if err != nil {
		closeJobResources(runOwner, runLogger)
		return "", fmt.Errorf("web: start dupe job: %w", err)
	}
	return jobID, nil
}

// GetDupeCheckSnapshot returns an immutable job snapshot when sessionID owns jobID.
// Unknown and foreign-owned IDs return the same not-found error.
func (b *Backend) GetDupeCheckSnapshot(sessionID string, jobID string) (DupeCheckSnapshot, error) {
	snapshot, err := b.jobEngine.DupeSnapshot(b.lookupJobOwner(sessionID), strings.TrimSpace(jobID))
	if err != nil {
		return DupeCheckSnapshot{}, fmt.Errorf("%w", err)
	}
	return snapshot, nil
}

// CancelDupeCheck requests cancellation when sessionID owns jobID.
// Unknown and foreign-owned IDs return the same not-found error.
func (b *Backend) CancelDupeCheck(sessionID string, jobID string) error {
	if err := b.jobEngine.CancelDupe(b.lookupJobOwner(sessionID), strings.TrimSpace(jobID)); err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// ReviewTrackerUpload stores a session-owned exact execution snapshot and
// returns the single-use opaque token required to start it.
func (b *Backend) ReviewTrackerUpload(
	ctx context.Context,
	sessionID string,
	release api.ReleaseRef,
	trackers []string,
	ignoreDupesFor []string,
	ruleAuthorizations []api.RuleAuthorization,
	questionnaireAnswers map[string]map[string]string,
	descriptionGroups []api.DescriptionBuilderGroup,
	noSeed bool,
	runLogLevel string,
) (api.UploadReviewResult, error) {
	runOpts, err := b.buildRunOptions(noSeed, runLogLevel)
	if err != nil {
		return api.UploadReviewResult{}, err
	}
	if _, err := b.ensureJobOwner(sessionID); err != nil {
		return api.UploadReviewResult{}, err
	}
	rt, err := b.requireRuntime()
	if err != nil {
		return api.UploadReviewResult{}, err
	}
	if !rt.capabilities.PreparedGenerationReady() || !CapabilityAvailable(rt.capabilities.UploadReview) {
		return api.UploadReviewResult{}, ErrPreparedGenerationUnavailable
	}
	reviewCore, err := rt.uploadReviewCore()
	if err != nil {
		return api.UploadReviewResult{}, err
	}
	release.SourcePath = strings.TrimSpace(release.SourcePath)
	if release.SourcePath == "" || release.Generation == 0 {
		return api.UploadReviewResult{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureInvalidSource,
			Operation: api.OperationKindUploadReview,
			Message:   "Source path is required.",
			Recovery:  api.OperationRecoveryEditInput,
		}, errors.New("path is required"))
	}
	resolvedTrackers := normalizeTrackerList(trackers)
	if len(resolvedTrackers) == 0 {
		return api.UploadReviewResult{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureNoEligibleTrackers,
			Operation: api.OperationKindUploadReview,
			Message:   "No trackers are eligible for upload.",
			Recovery:  api.OperationRecoverySelectTrackers,
		}, errors.New("at least one tracker must be selected"))
	}
	ignore := normalizeTrackerList(ignoreDupesFor)
	options := buildRunUploadOptions(rt.cfg, runOpts)
	if ctx == nil {
		return api.UploadReviewResult{}, errors.New("request context is required")
	}
	generationSeed, err := rt.capabilities.PreparedGenerationTransfer.ExportReleaseSeed(ctx, release)
	if err != nil {
		return api.UploadReviewResult{}, fmt.Errorf("web: exact upload generation: %w", err)
	}
	reviewInput := api.UploadReviewInput{
		Release:              release,
		Trackers:             append([]string(nil), resolvedTrackers...),
		IgnoreDupesFor:       append([]string(nil), ignore...),
		RuleAuthorizations:   cloneWebRuleAuthorizations(ruleAuthorizations),
		QuestionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
		DescriptionGroups:    api.CloneDescriptionBuilderGroups(descriptionGroups),
		Options:              options,
	}
	reviewed, err := reviewCore.ReviewAcceptedUpload(ctx, reviewInput)
	if err != nil {
		return api.UploadReviewResult{}, fmt.Errorf("web: %w", err)
	}
	token, err := b.uploadReviewRegistry().Issue(sessionID, UploadReviewSnapshot{
		Execution: jobs.UploadExecutionSnapshot{
			Seed:               generationSeed,
			PreparedGeneration: release.Generation,
			RuntimeGeneration:  rt.generationID,
			Input:              reviewInput,
			Outcome:            reviewed.Outcome,
		},
		Review: reviewed.Review,
	})
	if err != nil {
		return api.UploadReviewResult{}, fmt.Errorf("web: issue upload review token: %w", err)
	}
	return api.UploadReviewResult{Review: reviewed.Review, Token: token}, nil
}

func cloneWebRuleAuthorizations(values []api.RuleAuthorization) []api.RuleAuthorization {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]api.RuleAuthorization, len(values))
	for idx, value := range values {
		cloned[idx] = api.RuleAuthorization{Tracker: value.Tracker, Rules: append([]string(nil), value.Rules...)}
	}
	return cloned
}

// StartReviewedTrackerUpload consumes one session-owned reviewed snapshot and
// starts it only while the reviewed runtime generation remains active.
func (b *Backend) StartReviewedTrackerUpload(sessionID string, token string, correlationID string) (string, error) {
	owner, err := b.ensureJobOwner(sessionID)
	if err != nil {
		return "", err
	}
	reviewed, err := b.uploadReviewRegistry().Consume(sessionID, token)
	if err != nil {
		return "", api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureMissingReview,
			Operation: api.OperationKindUploadExecute,
			Message:   "Upload review is missing or expired.",
			Recovery:  api.OperationRecoveryReviewAgain,
		}, err)
	}
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	if reviewed.Execution.RuntimeGeneration != rt.generationID {
		return "", api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleReview,
			Operation: api.OperationKindUploadExecute,
			Message:   "Upload review is stale after configuration changed.",
			Recovery:  api.OperationRecoveryReviewAgain,
		}, errors.New("runtime generation changed"))
	}
	runOpts := runOptions{
		NoSeed:      reviewed.Execution.Input.Options.NoSeed,
		RunLogLevel: reviewed.Execution.Input.Options.RunLogLevel,
	}
	runCapabilities, runOwner, runLogger, err := b.buildRunCoreFromSnapshot(context.Background(), rt, runOpts)
	if err != nil {
		return "", err
	}
	if !runCapabilities.PreparedUploadReady() || !runCapabilities.PreparedGenerationReady() {
		closeJobResources(runOwner, runLogger)
		return "", ErrPreparedUploadUnavailable
	}
	input := reviewed.Execution.Input
	spec := jobs.UploadSpec{
		CorrelationID: correlationID,
		Snapshot:      reviewed.Execution,
		Runner: webUploadRunner{
			core:             runCapabilities.UploadExecution,
			generationTarget: runCapabilities.PreparedGenerationTransfer,
			generationSeed:   reviewed.Execution.Seed,
			expectedRelease:  input.Release,
		},
		Resources: jobs.Resources{Core: runOwner, Logger: runLogger},
	}
	jobID, err := b.jobEngine.StartUpload(context.Background(), owner, spec)
	if err != nil {
		closeJobResources(runOwner, runLogger)
		return "", fmt.Errorf("web: start upload job: %w", err)
	}
	return jobID, nil
}

func (b *Backend) uploadReviewRegistry() *UploadReviewRegistry {
	b.reviewMu.Lock()
	defer b.reviewMu.Unlock()
	if b.reviews == nil {
		b.reviews = NewUploadReviewRegistry()
	}
	return b.reviews
}

// retryTrackerUpload rebuilds per-run resources while preserving the exact
// reviewed operation and canonical preparation lineage.
func (b *Backend) retryTrackerUpload(sessionID string, spec jobs.UploadSpec, runOpts runOptions) (string, error) {
	owner, err := b.ensureJobOwner(sessionID)
	if err != nil {
		return "", err
	}
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	if !rt.capabilities.PreparedGenerationReady() {
		return "", ErrPreparedGenerationUnavailable
	}
	input := spec.Snapshot.Input
	input.Release.SourcePath = strings.TrimSpace(input.Release.SourcePath)
	if input.Release.SourcePath == "" {
		return "", errors.New("path is required")
	}
	input.Trackers = normalizeTrackerList(input.Trackers)
	if len(input.Trackers) == 0 {
		return "", errors.New("at least one tracker must be selected")
	}
	input.IgnoreDupesFor = normalizeTrackerList(input.IgnoreDupesFor)
	spec.Snapshot.Input = input
	if spec.Snapshot.RuntimeGeneration != rt.generationID {
		return "", api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleReview,
			Operation: api.OperationKindUploadExecute,
			Message:   "Upload retry requires a new review after configuration changed.",
			Recovery:  api.OperationRecoveryReviewAgain,
		}, errors.New("runtime generation changed"))
	}

	seedCtx, cancel := context.WithTimeout(context.Background(), seedPreparedMetaTimeout)
	defer cancel()
	releaseRef := spec.Snapshot.Input.Release
	if _, err = rt.capabilities.PreparedGenerationTransfer.ExportReleaseSeed(seedCtx, releaseRef); err != nil {
		return "", fmt.Errorf("web: canonical preparation: %w", err)
	}
	generationSeed := spec.Snapshot.Seed
	runCapabilities, runOwner, runLogger, err := b.buildRunCoreFromSnapshot(seedCtx, rt, runOpts)
	if err != nil {
		return "", err
	}
	if !runCapabilities.PreparedUploadReady() || !runCapabilities.PreparedGenerationReady() {
		closeJobResources(runOwner, runLogger)
		return "", ErrPreparedUploadUnavailable
	}

	spec.Runner = webUploadRunner{
		core:             runCapabilities.UploadExecution,
		generationTarget: runCapabilities.PreparedGenerationTransfer,
		generationSeed:   generationSeed,
		expectedRelease:  releaseRef,
	}
	spec.Resources = jobs.Resources{Core: runOwner, Logger: runLogger}
	jobID, err := b.jobEngine.StartUpload(context.Background(), owner, spec)
	if err != nil {
		closeJobResources(runOwner, runLogger)
		return "", fmt.Errorf("web: start upload job: %w", err)
	}
	return jobID, nil
}

// CancelTrackerUpload requests cancellation when sessionID owns jobID.
// Unknown and foreign-owned IDs return the same not-found error.
func (b *Backend) CancelTrackerUpload(sessionID string, jobID string) error {
	if err := b.jobEngine.CancelUpload(b.lookupJobOwner(sessionID), strings.TrimSpace(jobID)); err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// RetryFailedTrackerUpload starts a new session-owned job for only the original failed trackers.
// It rebuilds per-run resources from the current runtime while preserving the original job input and options.
func (b *Backend) RetryFailedTrackerUpload(sessionID string, jobID string, correlationID string) (string, error) {
	retry, err := b.jobEngine.UploadRetry(b.lookupJobOwner(sessionID), strings.TrimSpace(jobID))
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	runOpts := runOptions{
		NoSeed:      retry.Snapshot.Input.Options.NoSeed,
		RunLogLevel: retry.Snapshot.Input.Options.RunLogLevel,
	}
	return b.retryTrackerUpload(sessionID, retry.Spec(correlationID, nil, jobs.Resources{}), runOpts)
}

// ListJobs returns immutable retained duplicate-check and upload Jobs for one authenticated owner.
func (b *Backend) ListJobs(sessionID string) ([]jobs.OwnerJobSnapshot, error) {
	owner, err := b.ensureJobOwner(sessionID)
	if err != nil {
		return nil, err
	}
	snapshots, err := b.jobEngine.List(owner)
	if err != nil {
		return nil, fmt.Errorf("web: list owner jobs: %w", err)
	}
	return snapshots, nil
}

// GetTrackerUploadSnapshot returns an immutable job snapshot when sessionID owns jobID.
// Unknown and foreign-owned IDs return the same not-found error.
func (b *Backend) GetTrackerUploadSnapshot(sessionID string, jobID string) (TrackerUploadSnapshot, error) {
	snapshot, err := b.jobEngine.UploadSnapshot(b.lookupJobOwner(sessionID), strings.TrimSpace(jobID))
	if err != nil {
		return TrackerUploadSnapshot{}, fmt.Errorf("%w", err)
	}
	return snapshot, nil
}

// closeJobResources releases adapter-owned resources after preparation or engine enrollment fails.
func closeJobResources(coreSvc jobs.Closer, logger *logging.Logger) {
	if coreSvc != nil {
		_ = coreSvc.Close()
	}
	if logger != nil {
		_ = logger.Close()
	}
}

// stopAllDupeJobs remains as a compatibility shim for shutdown tests and closes the shared engine.
func (b *Backend) stopAllDupeJobs() {
	if b != nil && b.jobEngine != nil {
		b.jobEngine.Close()
	}
}

// stopAllUploadJobs remains as a compatibility shim for shutdown tests and closes the shared engine.
func (b *Backend) stopAllUploadJobs() {
	if b != nil && b.jobEngine != nil {
		b.jobEngine.Close()
	}
}

// stopAllLogStreams signals every active stream and waits for its worker to exit.
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
		if stream != nil {
			<-stream.done
		}
	}
	b.streamWG.Wait()
}
