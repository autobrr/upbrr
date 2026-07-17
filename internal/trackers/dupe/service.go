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
}

// Service coordinates bounded duplicate checks through tracker-bound adapters.
type Service struct {
	cfg      config.Config
	logger   api.Logger
	http     *http.Client
	banned   *trackerspkg.BannedGroupChecker
	registry *trackerspkg.Registry
	adapters map[string]Adapter
	filter   func([]api.DupeEntry, api.DuplicateSubject, string, config.Config, api.Logger) ([]api.DupeEntry, api.DupeMatch)
	initErr  error

	cancelWarningThreshold time.Duration
}

// NewServiceWithRegistry constructs and validates one adapter for every registered tracker.
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
	service.filter = func(entries []api.DupeEntry, meta api.DuplicateSubject, tracker string, _ config.Config, _ api.Logger) ([]api.DupeEntry, api.DupeMatch) {
		return filterDupes(entries, meta, tracker, registry)
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

// CheckWithAssessment returns full operation evidence plus reusable decision state.
func (s *Service) CheckWithAssessment(
	ctx context.Context,
	meta api.DuplicateSubject,
	trackerNames []string,
	options CheckOptions,
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
				result, entry, operationCanceled := s.checkTracker(ctx, meta, job.tracker, options)
				if operationCanceled {
					continue
				}
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
			Tracker:   tracker,
			HasDupes:  true,
			Match:     match,
			Status:    "completed",
			CheckedAt: checkedAt,
		}
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionResolved, "", true, match, nil), false
	}
	if options.SkipRemote {
		result = notRunPublicResult(tracker, NotRunUserRequested, "duplicate search skipped by user request", checkedAt)
		entry = newAssessmentEntry(meta, s.cfg, tracker, DispositionNotRun, NotRunUserRequested, false, api.DupeMatch{}, nil)
		entry.authorization = AuthorizationWaiver
		entry.verdict = VerdictWaived
		return result, entry, false
	}

	if trackerspkg.NormalizeBannedReleaseGroup(meta.Tag) != "" {
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
		if err != nil {
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
		result = notRunPublicResult(tracker, NotRunBannedGroup, reason, checkedAt)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionNotRun, NotRunBannedGroup, false, api.DupeMatch{}, nil), false
	}

	adapter, ok := s.adapters[tracker]
	if !ok || adapter == nil {
		result = failedPublicResult(tracker, FailureInternal, "duplicate adapter unavailable", checkedAt)
		return result, newAssessmentEntry(meta, s.cfg, tracker, DispositionFailed, FailureInternal, false, api.DupeMatch{}, nil), false
	}
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
		filter := s.filter
		if filter == nil {
			filter = FilterDupes
		}
		filtered, match := filter(raw, meta, tracker, s.cfg, s.logger)
		result := api.DupeCheckResult{
			Tracker:   tracker,
			Raw:       publicEntries(raw),
			Filtered:  publicEntries(filtered),
			HasDupes:  len(filtered) > 0,
			Match:     publicMatch(match),
			Notes:     cloneNotes(adapterResult.Notes()),
			Status:    "completed",
			CheckedAt: checkedAt,
		}
		s.logger.Tracef(
			"dupechecking: checked tracker=%s source=%s raw=%d filtered=%d dupes=%t",
			tracker,
			meta.SourcePath,
			len(raw),
			len(filtered),
			result.HasDupes,
		)
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

func publicEntries(entries []api.DupeEntry) []api.DupeEntry {
	out := cloneEntries(entries)
	for idx := range out {
		out[idx].Download = ""
		out[idx].Link = sanitizePublicURL(out[idx].Link)
	}
	return out
}

func publicMatch(match api.DupeMatch) api.DupeMatch {
	match.MatchedDownload = ""
	match.MatchedLink = sanitizePublicURL(match.MatchedLink)
	match.SeasonPackLink = sanitizePublicURL(match.SeasonPackLink)
	match.MatchedEpisodeIDs = append([]api.DupeEpisodeMatch(nil), match.MatchedEpisodeIDs...)
	for idx := range match.MatchedEpisodeIDs {
		match.MatchedEpisodeIDs[idx].Link = sanitizePublicURL(match.MatchedEpisodeIDs[idx].Link)
	}
	return match
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
	parsed.RawQuery = ""
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
	default:
		if result.HasDupes {
			return fmt.Sprintf("%d dupes found", len(result.Filtered))
		}
		return "no dupes found"
	}
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
