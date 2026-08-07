// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package dupe coordinates tracker-owned duplicate searches and shared matching.
package dupe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/redaction"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxDupeWorkers = 4
const duplicateCancelWarningThreshold = 5 * time.Second

type indexedTracker struct {
	index   int
	tracker string
}

type indexedResult struct {
	index  int
	result api.DupeCheckResult
	entry  assessmentEntry
}

// CheckOptions controls structural duplicate evaluation without synthesizing caller-side outcomes.
type CheckOptions struct {
	// SkipRemote suppresses tracker network search while preserving definitive local-client checks.
	SkipRemote bool
	// BypassBannedGroups classifies a known banned-group gate as explicitly
	// bypassed without refreshing dynamic policy or invoking the tracker adapter.
	BypassBannedGroups bool
}

// CheckProjectionSet searches dupe-ready projections and preserves definitive local-client matches from one exact set.
func (s *Service) CheckProjectionSet(
	ctx context.Context,
	meta api.DuplicateSubject,
	projectionSet api.TrackerReleaseProjectionSet,
	projectionOptions api.ProjectionDupeCheckOptions,
) (api.DupeCheckSummary, api.DupeAssessmentEvidence, error) {
	if err := projectionSet.Validate(); err != nil {
		return api.DupeCheckSummary{}, EmptyAssessment(), fmt.Errorf("dupechecking: projection set: %w", err)
	}
	options := CheckOptions{
		SkipRemote:         projectionOptions.SkipRemote,
		BypassBannedGroups: projectionOptions.BypassBannedGroups,
	}
	projections := make(map[string]api.TrackerReleaseProjection, len(projectionSet.Projections))
	trackers := make([]string, 0, len(projectionSet.Projections))
	for _, projection := range projectionSet.Projections {
		tracker := normalizeTracker(string(projection.TrackerID))
		if (projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady) && !containsTracker(meta.MatchedTrackers, tracker) {
			continue
		}
		projections[tracker] = projection
		trackers = append(trackers, tracker)
	}
	return s.checkWithAssessment(ctx, meta, trackers, options, projections)
}

// Service coordinates bounded duplicate checks through tracker-bound adapters.
type Service struct {
	cfg      config.Config
	logger   api.Logger
	http     *http.Client
	banned   *trackerspkg.BannedGroupChecker
	registry *trackerspkg.Registry
	adapters map[string]Adapter
	initErr  error

	cancelWarningThreshold time.Duration
}

// NewServiceWithRegistry constructs one tracker-bound adapter for every
// registered definition using a shared 20-second HTTP client. Construction
// failures are retained and returned by later checks rather than panicking.
func NewServiceWithRegistry(cfg config.Config, logger api.Logger, registry *trackerspkg.Registry) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	httpClient := &http.Client{Timeout: 20 * time.Second}
	service := &Service{
		cfg:                    cfg,
		logger:                 logger,
		http:                   httpClient,
		banned:                 trackerspkg.NewBannedGroupCheckerWithRegistry(cfg.MainSettings.DBPath, registry),
		registry:               registry,
		adapters:               make(map[string]Adapter),
		cancelWarningThreshold: duplicateCancelWarningThreshold,
	}
	if registry == nil {
		service.initErr = errors.New("dupechecking: tracker registry is nil")
		return service
	}
	for _, name := range registry.Names() {
		definition, ok := registry.Lookup(name)
		if !ok {
			service.initErr = fmt.Errorf("dupechecking: tracker definition unavailable: %s", name)
			return service
		}
		factory, ok := definition.(Factory)
		if !ok {
			service.initErr = fmt.Errorf("dupechecking: tracker duplicate factory unavailable: %s", name)
			return service
		}
		trackerConfig := effectiveTrackerConfig(cfg, name)
		dependencies := NewDependencies(name, trackerConfig, cfg.MainSettings.DBPath, httpClient, logger)
		dependencies.registry = registry
		adapter := factory.NewDuplicateAdapter(dependencies)
		if adapter == nil {
			service.initErr = fmt.Errorf("dupechecking: tracker duplicate adapter unavailable: %s", name)
			return service
		}
		service.adapters[name] = adapter
	}
	return service
}

// Check searches requested trackers. Normal completion returns exactly one result per tracker in resolved order.
// Cancellation stops enqueueing, waits for every started adapter, and returns completed evidence with the context error.
func (s *Service) Check(ctx context.Context, meta api.DuplicateSubject, trackerNames []string) (api.DupeCheckSummary, error) {
	summary, _, err := s.CheckWithAssessment(ctx, meta, trackerNames, CheckOptions{})
	return summary, err
}

// CheckWithAssessment runs at most four tracker adapters concurrently, returns
// public-safe results in requested order, and binds private evidence into an
// immutable assessment. Cancellation stops enqueueing, waits for started
// adapters, returns completed public evidence, and discards reusable assessment.
func (s *Service) CheckWithAssessment(
	ctx context.Context,
	meta api.DuplicateSubject,
	trackerNames []string,
	options CheckOptions,
) (api.DupeCheckSummary, Assessment, error) {
	return s.checkWithAssessment(ctx, meta, trackerNames, options, nil)
}

func (s *Service) checkWithAssessment(
	ctx context.Context,
	meta api.DuplicateSubject,
	trackerNames []string,
	options CheckOptions,
	projections map[string]api.TrackerReleaseProjection,
) (api.DupeCheckSummary, Assessment, error) {
	summary := api.DupeCheckSummary{SourcePath: meta.SourcePath}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return summary, EmptyAssessment(), errors.New("dupechecking: missing source path")
	}
	if s == nil || s.initErr != nil {
		if s == nil {
			return summary, EmptyAssessment(), errors.New("dupechecking: service is nil")
		}
		return summary, EmptyAssessment(), s.initErr
	}
	resolved := dedupeTrackers(trackerNames)
	if len(resolved) == 0 {
		summary.Notes = []string{"no trackers configured for dupe checking"}
		s.logger.Infof("dupechecking: no trackers configured source=%s", meta.SourcePath)
		return summary, EmptyAssessment(), nil
	}

	total := len(resolved)
	for _, tracker := range resolved {
		s.emitDupeProgress(ctx, api.DupeProgressUpdate{
			SourcePath: meta.SourcePath,
			Tracker:    tracker,
			Status:     "queued",
			Message:    "queued",
			Total:      total,
		})
	}

	jobs := make(chan indexedTracker)
	results := make(chan indexedResult, total)
	workerCount := min(total, maxDupeWorkers)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for job := range jobs {
				if ctx.Err() != nil {
					continue
				}
				s.emitDupeProgress(ctx, api.DupeProgressUpdate{
					SourcePath: meta.SourcePath,
					Tracker:    job.tracker,
					Status:     "running",
					Message:    "searching",
					Total:      total,
				})
				trackerMeta, projectionErr := duplicateSubjectForProjection(meta, job.tracker, projections)
				if projectionErr != nil {
					checkedAt := time.Now().UTC()
					result := notRunPublicResult(job.tracker, NotRunMissingMetadata, projectionErr.Error(), checkedAt)
					decorateProjectionResult(&result, trackerMeta.Projection)
					results <- indexedResult{
						index:  job.index,
						result: result,
						entry: newAssessmentEntry(
							trackerMeta,
							s.cfg,
							job.tracker,
							DispositionNotRun,
							NotRunMissingMetadata,
							false,
							api.DupeMatch{},
							nil,
						),
					}
					continue
				}
				result, entry, operationCanceled := s.checkTracker(ctx, trackerMeta, job.tracker, options)
				if operationCanceled {
					continue
				}
				decorateProjectionResult(&result, trackerMeta.Projection)
				results <- indexedResult{
					index:  job.index,
					result: result,
					entry:  entry,
				}
			}
		})
	}
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(results)
		close(workersDone)
	}()
	go func() {
		defer close(jobs)
		for index, tracker := range resolved {
			select {
			case <-ctx.Done():
				return
			case jobs <- indexedTracker{index: index, tracker: tracker}:
			}
		}
	}()

	watchdogDone := make(chan struct{})
	go s.warnOnSlowCancellation(ctx, workersDone, watchdogDone, meta.SourcePath)
	byIndex := make([]*api.DupeCheckResult, total)
	assessmentByIndex := make([]*assessmentEntry, total)
	completed := 0
	for completedResult := range results {
		result := completedResult.result
		byIndex[completedResult.index] = &result
		entry := completedResult.entry
		assessmentByIndex[completedResult.index] = &entry
		completed++
		s.emitDupeProgress(ctx, api.DupeProgressUpdate{
			SourcePath: meta.SourcePath,
			Tracker:    result.Tracker,
			Status:     result.Status,
			Message:    dupeProgressMessage(result),
			Completed:  completed,
			Total:      total,
			Result:     result,
		})
	}
	<-watchdogDone
	for _, result := range byIndex {
		if result != nil {
			summary.Results = append(summary.Results, *result)
		}
	}
	if err := ctx.Err(); err != nil {
		return summary, EmptyAssessment(), fmt.Errorf("context canceled: %w", err)
	}
	assessment := Assessment{entries: make(map[string]assessmentEntry, total), assessed: true}
	for _, entry := range assessmentByIndex {
		if entry != nil {
			assessment.entries[entry.tracker] = cloneAssessmentEntry(*entry)
		}
	}
	return summary, assessment, nil
}

func duplicateSubjectForProjection(
	meta api.DuplicateSubject,
	tracker string,
	projections map[string]api.TrackerReleaseProjection,
) (api.DuplicateSubject, error) {
	if projections == nil {
		return meta, nil
	}
	projection, ok := projections[normalizeTracker(tracker)]
	if !ok {
		return meta, errors.New("tracker projection is missing")
	}
	meta.Projection = &projection
	if containsTracker(meta.MatchedTrackers, tracker) {
		for _, name := range []string{projection.DuplicateCriteria.Name, projection.UploadReleaseName, projection.CanonicalReleaseName} {
			if name = strings.TrimSpace(name); name != "" {
				meta.ReleaseName = name
				break
			}
		}
		return meta, nil
	}
	criteria := projection.DuplicateCriteria
	name := strings.TrimSpace(criteria.Name)
	if name == "" {
		return meta, errors.New("tracker projection duplicate-search name is missing")
	}
	if projection.UploadReleaseName != name {
		declared := false
		for _, additional := range projection.AdditionalNames {
			if additional.Role == api.TrackerReleaseNameRoleSearch && strings.TrimSpace(additional.Value) == name {
				declared = true
				break
			}
		}
		if !declared {
			return meta, errors.New("tracker projection duplicate-search name is not declared")
		}
	}
	meta.ReleaseName = name
	target := projection.DuplicateTarget
	meta.HDRFacts = cloneHDRFacts(target.HDR)
	if value := strings.TrimSpace(target.Type); value != "" {
		meta.Type = value
	}
	if value := strings.TrimSpace(target.Source); value != "" {
		meta.Source = value
	}
	if value := strings.TrimSpace(target.VideoCodec); value != "" {
		meta.VideoCodec = value
	}
	if value := strings.TrimSpace(target.VideoEncode); value != "" {
		meta.VideoEncode = value
	}
	if value := strings.TrimSpace(criteria.Type.Label); value != "" {
		meta.Type = value
	}
	if value := strings.TrimSpace(criteria.Source.Label); value != "" {
		meta.Source = value
	}
	if criteria.Season > 0 {
		meta.SeasonInt = criteria.Season
	}
	if criteria.Episode > 0 {
		meta.EpisodeInt = criteria.Episode
	}
	if value := strings.TrimSpace(criteria.Date); value != "" {
		meta.DailyEpisodeDate = value
	}
	if !projection.DupeReady || projection.Readiness != api.ReadinessStatusReady {
		return meta, errors.New("tracker projection is not duplicate-ready")
	}
	return meta, nil
}

func decorateProjectionResult(result *api.DupeCheckResult, projection *api.TrackerReleaseProjection) {
	if result == nil || projection == nil {
		return
	}
	result.CanonicalReleaseName = projection.CanonicalReleaseName
	result.UploadReleaseName = projection.UploadReleaseName
	result.ProjectionFingerprint = projection.ProjectorFingerprint
	result.CriteriaFingerprint = projection.CriteriaFingerprint
	result.ProjectionStatus = projection.Readiness
	result.PolicyID = projection.DuplicatePolicyID
	result.PolicyFingerprint = projection.DuplicatePolicyFingerprint
	result.TargetFingerprint = projection.DuplicateTargetFingerprint
	result.SearchFingerprint = projection.DuplicateSearchFingerprint
}

func (s *Service) warnOnSlowCancellation(ctx context.Context, workersDone <-chan struct{}, done chan<- struct{}, sourcePath string) {
	defer close(done)
	select {
	case <-workersDone:
		return
	case <-ctx.Done():
	}
	threshold := s.cancelWarningThreshold
	if threshold <= 0 {
		threshold = duplicateCancelWarningThreshold
	}
	timer := time.NewTimer(threshold)
	defer timer.Stop()
	select {
	case <-workersDone:
	case <-timer.C:
		s.logger.Warnf("dupechecking: cancellation drain still active source=%s", sourcePath)
		<-workersDone
	}
}

func (s *Service) emitDupeProgress(ctx context.Context, update api.DupeProgressUpdate) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Warnf(
				"dupechecking: progress reporter panicked tracker=%s status=%s err=%s",
				update.Tracker,
				update.Status,
				panicProgressMessage(recovered),
			)
		}
	}()
	api.EmitDupeProgress(ctx, update)
}

func (s *Service) checkTracker(
	ctx context.Context,
	meta api.DuplicateSubject,
	tracker string,
	options CheckOptions,
) (result api.DupeCheckResult, entry assessmentEntry, operationCanceled bool) {
	checkedAt := time.Now().UTC()
	defer func() {
		if recovered := recover(); recovered != nil {
			message := panicFailureMessage(recovered)
			result = failedPublicResult(tracker, FailureInternal, message, checkedAt)
			entry = newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil)
			s.logger.Warnf("dupechecking: search panicked tracker=%s source=%s err=%s", tracker, meta.SourcePath, message)
		}
	}()

	if containsTracker(meta.MatchedTrackers, tracker) {
		match := api.DupeMatch{MatchedReason: "in_client"}
		result = api.DupeCheckResult{
			Tracker:  tracker,
			HasDupes: true,
			Evaluations: []api.DupeCandidateEvaluation{{
				Name:     strings.TrimSpace(meta.ReleaseName),
				Relation: api.DupeRelationExactDuplicate,
				Reasons: []api.DupeReason{{
					Code:    "in_client",
					Message: "Matching torrent is already present in the configured client.",
				}},
				HDR:            cloneHDRFacts(meta.HDRFacts),
				EvidenceStatus: meta.HDRFacts.Status,
			}},
			Search: api.DupeSearchEvidence{
				Complete:       true,
				CandidateCount: 1,
				Scope:          "local_client",
			},
			Status:    "completed",
			CheckedAt: checkedAt,
		}
		s.logger.Infof(
			"dupechecking: search tracker=%s state=completed source=local_client candidates=1 complete=true candidate_action=true review_required=true",
			tracker,
		)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionResolved, "", true, match, nil), false
	}
	if options.SkipRemote {
		result = notRunPublicResult(tracker, NotRunUserRequested, "duplicate search skipped by user request", checkedAt)
		entry = newAssessmentEntry(meta, s.cfg, tracker, DispositionNotRun, NotRunUserRequested, false, api.DupeMatch{}, nil)
		entry.authorization = AuthorizationWaiver
		entry.verdict = VerdictWaived
		return result, entry, false
	}

	if trackerspkg.NormalizeBannedReleaseGroup(meta.Tag) != "" && !options.BypassBannedGroups {
		if err := s.banned.RefreshDynamic(ctx, s.cfg, []string{tracker}, s.logger); err != nil {
			if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				return api.DupeCheckResult{}, assessmentEntry{}, true
			}
			message := bannedGroupCheckFailureMessage(err)
			s.logger.Warnf(
				"dupechecking: banned-group refresh failed tracker=%s source=%s err=%s",
				tracker,
				meta.SourcePath,
				redaction.RedactValue(err.Error(), nil),
			)
			result = failedPublicResult(tracker, FailureInternal, message, checkedAt)
			return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil), false
		}
	}
	if reason, err := s.bannedGroupSkipReason(tracker, meta.Tag); reason != "" || err != nil {
		switch {
		case err != nil:
			if !options.BypassBannedGroups {
				message := bannedGroupCheckFailureMessage(err)
				s.logger.Warnf(
					"dupechecking: banned-group check failed tracker=%s source=%s err=%s",
					tracker,
					meta.SourcePath,
					redaction.RedactValue(err.Error(), nil),
				)
				result = failedPublicResult(tracker, FailureInternal, message, checkedAt)
				return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil), false
			}
		case options.BypassBannedGroups:
			result = bypassedPublicResult(tracker, NotRunBannedGroup, reason, checkedAt)
			entry = newAssessmentEntry(meta, s.cfg, tracker, DispositionNotRun, NotRunBannedGroup, false, api.DupeMatch{}, nil)
			entry.authorization = AuthorizationWaiver
			entry.verdict = VerdictWaived
			return result, entry, false
		default:
			result = notRunPublicResult(tracker, NotRunBannedGroup, reason, checkedAt)
			return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionNotRun, NotRunBannedGroup, false, api.DupeMatch{}, nil), false
		}
	}

	adapter, ok := s.adapters[tracker]
	if !ok || adapter == nil {
		result = failedPublicResult(tracker, FailureInternal, "duplicate adapter unavailable", checkedAt)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil), false
	}
	s.logger.Infof("dupechecking: search tracker=%s state=start", tracker)
	adapterResult := adapter.Search(ctx, cloneDuplicateSubject(meta))
	if err := adapterResult.cause(); err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
		return api.DupeCheckResult{}, assessmentEntry{}, true
	}
	result, entry = s.projectAdapterResult(tracker, meta, adapterResult, checkedAt)
	return result, entry, false
}

func (s *Service) projectAdapterResult(
	tracker string,
	meta api.DuplicateSubject,
	adapterResult AdapterResult,
	checkedAt time.Time,
) (api.DupeCheckResult, assessmentEntry) {
	switch adapterResult.Disposition() {
	case DispositionInvalid:
		result := failedPublicResult(tracker, FailureInternal, "duplicate adapter returned invalid disposition", checkedAt)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil)
	case DispositionResolved:
		raw := trimEntries(adapterResult.Entries())
		candidates := make([]TrackerCandidate, len(raw))
		for index, rawCandidate := range raw {
			candidates[index] = NormalizeCandidate(rawCandidate, tracker)
		}
		policy := s.duplicatePolicy(tracker, meta)
		search := adapterResult.SearchEvidence()
		evaluation := Evaluate(duplicateTargetForEvaluation(meta), candidates, policy, search)
		effectiveComplete := search.EffectiveComplete()
		match := evaluationMatch(evaluation)
		hasDupes := evaluation.Blocks || hasActionableCandidateEvaluations(evaluation.Candidates)
		result := api.DupeCheckResult{
			Tracker:     tracker,
			HasDupes:    hasDupes,
			Notes:       cloneNotes(adapterResult.Notes()),
			Status:      "completed",
			CheckedAt:   checkedAt,
			PolicyID:    policy.ID,
			Evaluations: publicCandidateEvaluations(evaluation),
			Search: api.DupeSearchEvidence{
				Complete:       effectiveComplete,
				Pages:          search.Pages,
				CandidateCount: len(candidates),
				Scope:          search.Scope,
				WorkScope:      string(search.WorkScope),
				WrongWorkCount: search.WrongWorkCount,
				Warnings:       cloneNotes(search.Warnings),
			},
		}
		s.logger.Infof(
			"dupechecking: search tracker=%s state=completed work_scope=%s received=%d evaluated=%d wrong_work=%d exhaustive=%t effective_complete=%t candidate_action=%t review_required=%t",
			tracker,
			search.WorkScope,
			len(raw),
			len(candidates),
			search.WrongWorkCount,
			search.Complete,
			effectiveComplete,
			hasDupes,
			hasDupes || evaluation.RequiresAction,
		)
		s.logTargetEvaluation(tracker, policy, evaluation.TargetFacts, search)
		for _, finding := range evaluation.SetFindings {
			s.logSetFinding(tracker, finding)
		}
		s.logger.Debugf(
			"dupechecking: search tracker=%s pages=%d exhaustive=%t effective_complete=%t work_scope=%s scope=%s wrong_work=%d warnings=%q",
			tracker,
			search.Pages,
			search.Complete,
			effectiveComplete,
			search.WorkScope,
			dupeCandidateLogValue(search.Scope),
			search.WrongWorkCount,
			dupeCandidateLogValues(search.Warnings),
		)
		for _, candidate := range evaluation.Candidates {
			s.logCandidateEvaluation(tracker, candidate)
		}
		entry := newAssessmentEntry(meta, s.cfg, tracker, DispositionResolved, "", result.HasDupes, match, raw)
		return result, entry
	case DispositionNotRun:
		code := adapterResult.Code()
		if !validNotRunCode(code) {
			result := failedPublicResult(tracker, FailureInternal, "duplicate adapter returned invalid not-run code", checkedAt)
			return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil)
		}
		message := sanitizeSafeMessage(adapterResult.SafeMessage(), "duplicate search not run")
		result := api.DupeCheckResult{
			Tracker:    tracker,
			Notes:      cloneNotes(adapterResult.Notes()),
			Skipped:    true,
			SkipReason: message,
			SkipCode:   code,
			Status:     "skipped",
			CheckedAt:  checkedAt,
		}
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionNotRun, code, false, api.DupeMatch{}, nil)
	case DispositionFailed:
		code := adapterResult.Code()
		if !validFailureCode(code) {
			code = FailureInternal
		}
		message := sanitizeSafeMessage(adapterResult.SafeMessage(), "duplicate search failed")
		if cause := adapterResult.cause(); cause != nil {
			s.logger.Warnf(
				"dupechecking: adapter failed tracker=%s source=%s code=%s err=%s",
				tracker,
				meta.SourcePath,
				code,
				redaction.RedactValue(cause.Error(), nil),
			)
		}
		result := failedPublicResult(tracker, code, message, checkedAt)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, code, false, api.DupeMatch{}, nil)
	default:
		result := failedPublicResult(tracker, FailureInternal, "duplicate adapter returned invalid result", checkedAt)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil)
	}
}

func (s *Service) logSetFinding(tracker string, finding SetFinding) {
	observed := "unknown"
	if finding.SeparationKnown {
		observed = fmt.Sprintf("%.2f", finding.ObservedMinimumSeparationPercent)
	}
	s.logger.Debugf(
		"dupechecking: set tracker=%s rule=%s relation=%s reason=%s occupancy=%d capacity=%d "+
			"minimum_size_separation=%.2f observed_size_separation=%s candidates=%q facts=%q missing=%q",
		tracker,
		dupeCandidateLogValue(finding.RuleID),
		finding.Relation,
		dupeCandidateLogValue(finding.ReasonCode),
		finding.ExistingOccupancy,
		finding.Capacity,
		finding.MinimumSizeSeparationPercent,
		observed,
		dupeCandidateLogValues(finding.CandidateIDs),
		dupeCandidateLogValues(finding.FactSummaries),
		dupeCandidateLogValues(finding.Missing),
	)
}

func (s *Service) logCandidateEvaluation(tracker string, evaluation CandidateEvaluation) {
	candidate := evaluation.Candidate
	findings := decisiveCandidateFindings(evaluation)
	hdr := candidateHDRLogValue(evaluation.Facts.HDR)
	if hdr == "" {
		hdr = "unknown"
	}
	evidence := evaluation.Facts.HDR.Status
	if evidence == "" {
		evidence = api.HDREvidenceMissing
	}
	s.logger.Debugf(
		"dupechecking: candidate tracker=%s candidate_id=%q relation=%s winning_rule=%s raw_type=%q comparison_type=%q "+
			"kind=%s class=%s source_family=%s resolution=%s "+
			"hdr_status=%s compared=%q missing=%q matched=%q indeterminate=%q name=%q facts=%q hdr=%q hdr_origin=%s reasons=%q",
		tracker,
		dupeCandidateLogValue(candidate.ID),
		evaluation.Relation,
		dupeCandidateLogValue(evaluation.WinningRule),
		dupeCandidateLogValue(candidate.Type),
		dupeCandidateLogValue(evaluation.Facts.Type.Value),
		evaluation.Facts.MediaKind,
		evaluation.Facts.MediaClass,
		evaluation.Facts.SourceFamily,
		dupeCandidateLogValue(evaluation.Facts.Resolution.Value),
		evidence,
		dupeFindingComparisonValues(findings),
		dupeFindingValues(findings, func(finding RuleFinding) []string {
			return append(append([]string(nil), finding.Missing...), finding.Contradictions...)
		}),
		dupeFindingRuleIDs(findings, RuleFindingMatched),
		dupeFindingRuleIDs(findings, RuleFindingIndeterminate),
		dupeCandidateLogValue(candidate.Name),
		dupeCandidateFactsLogValue(candidate),
		hdr,
		evaluation.Facts.HDR.Origin,
		dupeCandidateReasonsLogValue(evaluation.Reasons),
	)
}

func (s *Service) logTargetEvaluation(
	tracker string,
	policy trackerspkg.DupePolicy,
	facts normalizedFacts,
	search SearchEvidence,
) {
	hdrStatus := facts.HDR.Status
	if hdrStatus == "" {
		hdrStatus = api.HDREvidenceMissing
	}
	s.logger.Debugf(
		"dupechecking: target tracker=%s general_policy=%s tracker_policy=%s comparison_type=%q type_status=%s type_origin=%s "+
			"kind=%s class=%s source_family=%s source=%s resolution=%s hdr_status=%s scope=%s",
		tracker,
		GeneralPolicyID,
		dupeCandidateLogValue(policy.ID),
		dupeCandidateLogValue(facts.Type.Value),
		facts.Type.Status,
		facts.Type.Origin,
		facts.MediaKind,
		facts.MediaClass,
		facts.SourceFamily,
		dupeCandidateLogValue(facts.Source.Value),
		dupeCandidateLogValue(facts.Resolution.Value),
		hdrStatus,
		dupeCandidateLogValue(search.Scope),
	)
}

func dupeFindingComparisonValues(findings []RuleFinding) string {
	values := make([]string, 0, len(findings))
	seen := make(map[string]struct{})
	for _, finding := range findings {
		if len(finding.comparisons) == 0 {
			for _, value := range finding.Compared {
				if value = dupeCandidateLogValue(value); value != "" {
					if _, ok := seen[value]; !ok {
						seen[value] = struct{}{}
						values = append(values, value)
					}
				}
			}
			continue
		}
		for _, comparison := range finding.comparisons {
			value := fmt.Sprintf(
				"%s[target={%s},target_status=%s,target_origin=%s,candidate={%s},candidate_status=%s,candidate_origin=%s,result=%s]",
				comparison.Dimension,
				dupeCandidateLogValue(comparison.Target.Value),
				comparison.Target.Status,
				comparison.Target.Origin,
				dupeCandidateLogValue(comparison.Candidate.Value),
				comparison.Candidate.Status,
				comparison.Candidate.Origin,
				comparison.Result,
			)
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	return strings.Join(values, " | ")
}

func dupeFindingValues(findings []RuleFinding, values func(RuleFinding) []string) string {
	var result []string
	seen := make(map[string]struct{})
	for _, finding := range findings {
		for _, value := range values(finding) {
			if value = dupeCandidateLogValue(value); value != "" {
				if _, ok := seen[value]; ok {
					continue
				}
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	return strings.Join(result, " | ")
}

func decisiveCandidateFindings(evaluation CandidateEvaluation) []RuleFinding {
	applicable := make([]RuleFinding, 0, len(evaluation.Findings))
	var priority, specificity int
	haveRank := false
	for _, finding := range evaluation.Findings {
		if finding.Status != RuleFindingMatched && finding.Status != RuleFindingIndeterminate {
			continue
		}
		applicable = append(applicable, finding)
		if finding.RuleID == evaluation.WinningRule {
			priority, specificity, haveRank = finding.Priority, finding.Specificity, true
		}
	}
	if !haveRank {
		for _, finding := range applicable {
			if !haveRank || finding.Priority > priority || finding.Priority == priority && finding.Specificity > specificity {
				priority, specificity, haveRank = finding.Priority, finding.Specificity, true
			}
		}
	}
	if !haveRank {
		return nil
	}
	result := make([]RuleFinding, 0, len(applicable))
	for _, finding := range applicable {
		if finding.Priority == priority && finding.Specificity == specificity {
			result = append(result, finding)
		}
	}
	return result
}

func dupeFindingRuleIDs(findings []RuleFinding, status RuleFindingStatus) string {
	values := make([]string, 0, len(findings))
	seen := make(map[string]struct{}, len(findings))
	for _, finding := range findings {
		value := dupeCandidateLogValue(finding.RuleID)
		if finding.Status != status || value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return strings.Join(values, " | ")
}

func dupeCandidateFactsLogValue(candidate TrackerCandidate) string {
	values := []string{
		candidate.Type,
		candidate.Source,
		candidate.Provider,
		candidate.Resolution,
		candidate.Codec,
		candidate.Container,
		candidate.Edition,
		candidate.Region,
		candidate.ThreeD,
		candidate.Repack,
		candidate.Date,
	}
	if candidate.Pack {
		values = append(values, "season pack")
	}
	facts := make([]string, 0, len(values))
	for _, value := range values {
		if value = dupeCandidateLogValue(value); value != "" {
			facts = append(facts, value)
		}
	}
	return strings.Join(facts, " · ")
}

func candidateHDRLogValue(hdr api.HDRFacts) string {
	parts := make([]string, 0, 2)
	if len(hdr.Formats) > 0 {
		formats := make([]string, 0, len(hdr.Formats))
		for _, format := range hdr.Formats {
			formats = append(formats, strings.ReplaceAll(string(format), "_", " "))
		}
		parts = append(parts, strings.Join(formats, " + "))
	}
	if profile := dupeCandidateLogValue(hdr.DolbyVisionProfile); profile != "" {
		parts = append(parts, "profile "+profile)
	}
	return strings.Join(parts, " · ")
}

func dupeCandidateReasonsLogValue(reasons []api.DupeReason) string {
	values := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		code := dupeCandidateLogValue(reason.Code)
		message := dupeCandidateLogValue(reason.Message)
		switch {
		case code != "" && message != "":
			values = append(values, code+" — "+message)
		case code != "":
			values = append(values, code)
		case message != "":
			values = append(values, message)
		}
	}
	return strings.Join(values, " | ")
}

func dupeCandidateLogValues(values []string) string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = dupeCandidateLogValue(value); value != "" {
			result = append(result, value)
		}
	}
	return strings.Join(result, " | ")
}

func dupeCandidateLogValue(value string) string {
	value = redaction.RedactValue(strings.TrimSpace(value), nil)
	return strings.Join(strings.Fields(value), " ")
}

func (s *Service) duplicatePolicy(tracker string, meta api.DuplicateSubject) trackerspkg.DupePolicy {
	var policy trackerspkg.DupePolicy
	if s != nil && s.registry != nil {
		policy, _ = s.registry.LookupDupePolicy(tracker)
	}
	if strings.TrimSpace(policy.ID) == "" {
		if meta.Projection != nil && strings.TrimSpace(meta.Projection.DuplicatePolicyID) != "" {
			policy.ID = meta.Projection.DuplicatePolicyID
		} else {
			policy.ID = strings.ToLower(normalizeTracker(tracker)) + "/duplicate/v1"
		}
	}
	if policy.SearchScope.MaxPages <= 0 {
		policy.SearchScope.MaxPages = 100
	}
	return policy
}

func duplicateTargetForEvaluation(meta api.DuplicateSubject) api.TrackerDuplicateTarget {
	if meta.Projection != nil {
		return meta.Projection.DuplicateTarget
	}
	return api.TrackerDuplicateTarget{
		Names:       append([]string(nil), meta.ReleaseName, meta.Filename),
		Category:    string(meta.Identity.Category),
		Type:        meta.Type,
		Source:      meta.Source,
		Resolution:  meta.Release.Resolution,
		VideoCodec:  meta.VideoCodec,
		VideoEncode: meta.VideoEncode,
		HDR:         cloneHDRFacts(meta.HDRFacts),
		Edition:     strings.Join(meta.Release.Edition, " "),
		Region:      meta.Release.Region,
		Group:       meta.Tag,
		Season:      meta.SeasonInt,
		Episode:     meta.EpisodeInt,
		Date:        meta.DailyEpisodeDate,
		Pack:        meta.TVPack,
		SizeBytes:   meta.SourceSize,
		FileNames:   append([]string(nil), meta.FileList...),
	}
}

func evaluationMatch(evaluation Evaluation) api.DupeMatch {
	for _, candidate := range evaluation.Candidates {
		if candidate.Relation == api.DupeRelationCoexists {
			continue
		}
		reason := string(candidate.Relation)
		if len(candidate.Reasons) > 0 && strings.TrimSpace(candidate.Reasons[0].Code) != "" {
			reason = candidate.Reasons[0].Code
		}
		return api.DupeMatch{
			MatchedID:       candidate.Candidate.ID,
			MatchedName:     candidate.Candidate.Name,
			MatchedLink:     candidate.Candidate.DetailsLink,
			MatchedDownload: candidate.Candidate.privateDownload,
			MatchedReason:   reason,
		}
	}
	if evaluation.RequiresAction && !evaluation.Complete {
		return api.DupeMatch{MatchedReason: "incomplete_search"}
	}
	return api.DupeMatch{}
}

func hasActionableCandidateEvaluations(candidates []CandidateEvaluation) bool {
	for _, candidate := range candidates {
		if candidate.Relation != api.DupeRelationCoexists {
			return true
		}
	}
	return false
}

func containsTracker(trackers []string, tracker string) bool {
	for _, candidate := range trackers {
		if normalizeTracker(candidate) == normalizeTracker(tracker) {
			return true
		}
	}
	return false
}

func notRunPublicResult(tracker string, code string, message string, checkedAt time.Time) api.DupeCheckResult {
	message = sanitizeSafeMessage(message, "duplicate search not run")
	return api.DupeCheckResult{
		Tracker:    tracker,
		Notes:      []string{message},
		Skipped:    true,
		SkipReason: message,
		SkipCode:   code,
		Status:     "skipped",
		CheckedAt:  checkedAt,
	}
}

func bypassedPublicResult(tracker string, code string, message string, checkedAt time.Time) api.DupeCheckResult {
	message = sanitizeSafeMessage(message, "duplicate policy bypassed")
	return api.DupeCheckResult{
		Tracker:    tracker,
		Notes:      []string{message},
		Skipped:    true,
		SkipReason: "debug mode bypassed policy: " + message,
		SkipCode:   code,
		Status:     "bypassed",
		CheckedAt:  checkedAt,
	}
}

func failedPublicResult(tracker string, _ string, message string, checkedAt time.Time) api.DupeCheckResult {
	message = sanitizeSafeMessage(message, "duplicate search failed")
	return api.DupeCheckResult{
		Tracker:   tracker,
		Status:    "failed",
		Error:     message,
		CheckedAt: checkedAt,
	}
}

func sanitizeSafeMessage(message string, fallback string) string {
	message = strings.TrimSpace(logging.SanitizeMessage(message))
	if message == "" {
		return fallback
	}
	return message
}

func sanitizePublicURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	publicQuery := url.Values{}
	for key, values := range parsed.Query() {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "id", "torrentid":
			publicQuery[key] = append([]string(nil), values...)
		}
	}
	parsed.RawQuery = publicQuery.Encode()
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

func (s *Service) bannedGroupSkipReason(tracker string, tag string) (string, error) {
	group := trackerspkg.NormalizeBannedReleaseGroup(tag)
	if group == "" {
		return "", nil
	}
	banned, err := s.banned.IsBanned(tracker, group)
	if err != nil {
		return "", fmt.Errorf("dupechecking: %s banned group: %w", normalizeTracker(tracker), err)
	}
	if !banned {
		return "", nil
	}
	return fmt.Sprintf("group %s is banned on %s", group, normalizeTracker(tracker)), nil
}

func effectiveTrackerConfig(cfg config.Config, tracker string) config.TrackerConfig {
	for name, trackerConfig := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), tracker) {
			return trackerConfig
		}
	}
	return config.TrackerConfig{}
}

func cloneDuplicateSubject(meta api.DuplicateSubject) api.DuplicateSubject {
	// Prepared metadata is already immutable at the Core boundary; copying the value
	// prevents adapter replacement while slice/map fields remain read-only by contract.
	return meta
}

func dedupeTrackers(trackers []string) []string {
	resolved := make([]string, 0, len(trackers))
	seen := make(map[string]struct{}, len(trackers))
	for _, trackerName := range trackers {
		tracker := normalizeTracker(trackerName)
		if tracker == "" {
			continue
		}
		if _, ok := seen[tracker]; ok {
			continue
		}
		seen[tracker] = struct{}{}
		resolved = append(resolved, tracker)
	}
	return resolved
}

func dupeProgressMessage(result api.DupeCheckResult) string {
	switch result.Status {
	case "failed":
		return sanitizeSafeMessage(result.Error, "duplicate search failed")
	case "skipped":
		return sanitizeSafeMessage(result.SkipReason, "duplicate search not run")
	case "bypassed":
		return sanitizeSafeMessage(result.SkipReason, "duplicate policy bypassed")
	default:
		if !result.Search.Complete {
			if count := actionableCandidateCount(result.Evaluations); count > 0 {
				return fmt.Sprintf("search incomplete; %d candidates require attention", count)
			}
			return "search incomplete; review required"
		}
		if result.HasDupes {
			return fmt.Sprintf("%d candidates require attention", actionableCandidateCount(result.Evaluations))
		}
		return "no dupes found"
	}
}

func actionableCandidateCount(candidates []api.DupeCandidateEvaluation) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Relation != api.DupeRelationCoexists {
			count++
		}
	}
	return count
}

func panicFailureMessage(recovered any) string {
	detail := strings.TrimSpace(redaction.RedactValue(fmt.Sprint(recovered), nil))
	if detail == "" {
		return "duplicate search panicked"
	}
	return "duplicate search panicked: " + detail
}

func bannedGroupCheckFailureMessage(err error) string {
	detail := strings.TrimSpace(redaction.RedactValue(err.Error(), nil))
	if detail == "" {
		detail = "unknown error"
	}
	return "banned group check failed: " + detail + "; action=fix banned-group configuration and retry"
}

func panicProgressMessage(recovered any) string {
	detail := strings.TrimSpace(redaction.RedactValue(fmt.Sprint(recovered), nil))
	if detail == "" {
		return "duplicate progress reporter panicked"
	}
	return "duplicate progress reporter panicked: " + detail
}
