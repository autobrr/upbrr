// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package dupechecking runs tracker-specific duplicate searches and converts
// the results into upload review state.
package dupechecking

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxDupeWorkers = 4
const defaultDupeCancelDrainTimeout = 5 * time.Second
const (
	skipCodeNotePrefix = "\x00upbrr-skip-code:"
	skipCodeNoteSuffix = "\x00"
)

// Service coordinates duplicate checks across configured tracker handlers.
type Service struct {
	cfg      config.Config
	logger   api.Logger
	http     *http.Client
	tracker  *trackerdata.Client
	handlers map[string]searchHandler
	filter   func([]api.DupeEntry, api.PreparedMetadata, string, config.Config, api.Logger) ([]api.DupeEntry, api.DupeMatch)

	cancelDrainTimeout time.Duration
}

// NewService builds a duplicate-checking service with shared HTTP and tracker
// metadata clients.
func NewService(cfg config.Config, logger api.Logger) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	httpClient := &http.Client{Timeout: 20 * time.Second}
	trackerClient := trackerdata.NewClient(cfg, logger, httpClient)
	deps := handlerDeps{cfg: cfg, logger: logger, http: httpClient, tracker: trackerClient}
	return &Service{
		cfg:      cfg,
		logger:   logger,
		http:     httpClient,
		tracker:  trackerClient,
		handlers: buildHandlers(deps),
		filter:   FilterDupes,

		cancelDrainTimeout: defaultDupeCancelDrainTimeout,
	}
}

// Check searches the requested trackers for duplicates of meta.SourcePath.
// Missing source paths return an error, an empty tracker list returns a summary
// note, and context cancellation stops queued work, waits briefly for active
// workers, and returns a cancellation error instead of a partial summary.
// Progress callback panics are logged and do not abort the duplicate check.
func (s *Service) Check(ctx context.Context, meta api.PreparedMetadata, trackers []string) (api.DupeCheckSummary, error) {
	if strings.TrimSpace(meta.SourcePath) == "" {
		return api.DupeCheckSummary{}, errors.New("dupechecking: missing source path")
	}
	summary := api.DupeCheckSummary{SourcePath: meta.SourcePath}
	resolvedTrackers := dedupeTrackers(trackers)
	if len(resolvedTrackers) == 0 {
		summary.Notes = append(summary.Notes, "no trackers configured for dupe checking")
		s.logger.Infof("dupechecking: no trackers configured for %s", meta.SourcePath)
		return summary, nil
	}

	total := len(resolvedTrackers)
	for _, tracker := range resolvedTrackers {
		s.emitDupeProgress(ctx, api.DupeProgressUpdate{
			SourcePath: meta.SourcePath,
			Tracker:    tracker,
			Status:     "queued",
			Message:    "queued",
			Completed:  0,
			Total:      total,
		})
	}

	jobs := make(chan string)
	results := make(chan api.DupeCheckResult, total)
	workerCount := min(total, maxDupeWorkers)

	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for tracker := range jobs {
				if ctx.Err() != nil {
					return
				}
				s.emitDupeProgress(ctx, api.DupeProgressUpdate{
					SourcePath: meta.SourcePath,
					Tracker:    tracker,
					Status:     "running",
					Message:    "searching",
					Total:      total,
				})
				results <- s.checkTracker(ctx, meta, tracker)
			}
		})
	}
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()

	go func() {
		defer close(jobs)
		for _, tracker := range resolvedTrackers {
			select {
			case <-ctx.Done():
				return
			case jobs <- tracker:
			}
		}
	}()

	completed := 0
	for completed < total {
		select {
		case <-ctx.Done():
			s.waitForCanceledDupeWorkers(workersDone, meta.SourcePath)
			return api.DupeCheckSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
		case result := <-results:
			if ctx.Err() != nil {
				s.waitForCanceledDupeWorkers(workersDone, meta.SourcePath)
				return api.DupeCheckSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
			}
			completed++
			summary.Results = append(summary.Results, result)
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
	}

	<-workersDone
	summary.Results = groupRuleSkippedResults(summary.Results)
	sort.Slice(summary.Results, func(i, j int) bool {
		return summary.Results[i].Tracker < summary.Results[j].Tracker
	})

	return summary, nil
}

// waitForCanceledDupeWorkers joins canceled duplicate-check workers up to the
// configured drain timeout so ctx-ignoring handlers cannot delay cancellation
// indefinitely.
func (s *Service) waitForCanceledDupeWorkers(workersDone <-chan struct{}, sourcePath string) {
	timeout := s.cancelDrainTimeout
	if timeout <= 0 {
		timeout = defaultDupeCancelDrainTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-workersDone:
	case <-timer.C:
		if s.logger != nil {
			s.logger.Warnf("dupechecking: timed out waiting for canceled workers for %s", sourcePath)
		}
	}
}

func (s *Service) emitDupeProgress(ctx context.Context, update api.DupeProgressUpdate) {
	defer func() {
		if recovered := recover(); recovered != nil && s.logger != nil {
			s.logger.Warnf("dupechecking: progress reporter panicked for %s %s: %s", update.Tracker, update.Status, panicProgressMessage(recovered))
		}
	}()
	api.EmitDupeProgress(ctx, update)
}

func (s *Service) checkTracker(ctx context.Context, meta api.PreparedMetadata, tracker string) (result api.DupeCheckResult) {
	result = api.DupeCheckResult{Tracker: tracker, CheckedAt: time.Now().UTC(), Status: "completed"}
	defer func() {
		if recovered := recover(); recovered != nil {
			message := panicFailureMessage(recovered)
			result = api.DupeCheckResult{
				Tracker:   tracker,
				CheckedAt: result.CheckedAt,
				Status:    "failed",
				Error:     message,
				Notes:     []string{message},
			}
			s.logger.Warnf("dupechecking: %s search panicked for %s: %s", tracker, meta.SourcePath, message)
		}
	}()

	if reason, rules := skipReason(meta, tracker); reason != "" {
		result.Skipped = true
		result.SkipReason = reason
		result.SkipRules = rules
		result.Status = "skipped"
		result.Notes = append(result.Notes, reason)
		s.logger.Debugf("dupechecking: skipped %s for %s due to rules: %s", tracker, meta.SourcePath, reason)
		return result
	}

	handler, ok := s.handlers[tracker]
	if !ok {
		reason := handlerNotImplementedReason(tracker)
		result.Skipped = true
		result.SkipReason = reason
		result.Status = "skipped"
		result.Notes = append(result.Notes, reason)
		s.logger.Warnf("dupechecking: no handler for %s (%s)", tracker, meta.SourcePath)
		return result
	}

	raw, notes, err := handler.Search(ctx, meta, tracker)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		result.Notes = append(result.Notes, fmt.Sprintf("dupe search failed: %v", err))
		s.logger.Warnf("dupechecking: %s search failed for %s: %v", tracker, meta.SourcePath, err)
		return result
	}
	skipCode, notes := splitSkipCodeNotes(notes)
	if reason, ok := parseSkipReason(notes); ok {
		result.Skipped = true
		result.SkipReason = reason
		result.SkipCode = skipCode
		result.Status = "skipped"
	}
	result.Notes = append(result.Notes, notes...)
	result.Raw = trimEntries(raw)
	if result.Skipped {
		s.logger.Debugf("dupechecking: handler marked skipped for %s (%s)", tracker, meta.SourcePath)
		return result
	}

	filter := s.filter
	if filter == nil {
		filter = FilterDupes
	}
	filtered, match := filter(result.Raw, meta, tracker, s.cfg, s.logger)
	result.Filtered = filtered
	result.Match = match
	result.HasDupes = len(filtered) > 0
	s.logger.Debugf("dupechecking: %s checked for %s raw=%d filtered=%d dupes=%t", tracker, meta.SourcePath, len(result.Raw), len(filtered), result.HasDupes)
	return result
}

func panicFailureMessage(recovered any) string {
	detail := strings.TrimSpace(redaction.RedactValue(fmt.Sprint(recovered), nil))
	if detail == "" {
		return "dupe search panicked"
	}
	return "dupe search panicked: " + detail
}

func panicProgressMessage(recovered any) string {
	detail := strings.TrimSpace(redaction.RedactValue(fmt.Sprint(recovered), nil))
	if detail == "" {
		return "dupe progress reporter panicked"
	}
	return "dupe progress reporter panicked: " + detail
}

func noteSkipCode(code string) string {
	return skipCodeNotePrefix + strings.TrimSpace(code) + skipCodeNoteSuffix
}

// splitSkipCodeNotes extracts trusted internal skip-code metadata and leaves
// handler display notes intact.
func splitSkipCodeNotes(notes []string) (string, []string) {
	if len(notes) == 0 {
		return "", nil
	}
	out := make([]string, 0, len(notes))
	var code string
	for _, note := range notes {
		skipCode, ok := parseSkipCodeNote(note)
		if ok {
			if code == "" {
				code = skipCode
			}
			continue
		}
		out = append(out, note)
	}
	return code, out
}

// parseSkipCodeNote accepts only the NUL-wrapped internal marker so tracker
// notes that merely begin with similar text stay user-visible.
func parseSkipCodeNote(note string) (string, bool) {
	if !strings.HasPrefix(note, skipCodeNotePrefix) || !strings.HasSuffix(note, skipCodeNoteSuffix) {
		return "", false
	}
	code := strings.TrimSuffix(strings.TrimPrefix(note, skipCodeNotePrefix), skipCodeNoteSuffix)
	return strings.TrimSpace(code), true
}

func dedupeTrackers(trackers []string) []string {
	if len(trackers) == 0 {
		return nil
	}
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
		if strings.TrimSpace(result.Error) != "" {
			return result.Error
		}
		return "dupe search failed"
	case "skipped":
		if strings.TrimSpace(result.SkipReason) != "" {
			return result.SkipReason
		}
		return "skipped"
	default:
		if result.HasDupes {
			return fmt.Sprintf("%d dupes found", len(result.Filtered))
		}
		return "no dupes found"
	}
}

type groupedResult struct {
	result api.DupeCheckResult
	used   map[int]struct{}
}

func groupRuleSkippedResults(results []api.DupeCheckResult) []api.DupeCheckResult {
	if len(results) < 2 {
		return results
	}

	groupedByRule, groupedByReason := collectRuleSkipGroups(results)
	ruleCombined, indexesInCombinedRule := buildRuleGroupedResults(results, groupedByRule)
	reasonCombined := buildReasonGroupedResults(results, groupedByReason)
	combined := append(append([]groupedResult(nil), ruleCombined...), reasonCombined...)

	out := make([]api.DupeCheckResult, 0, len(results))
	consumedByReason := make(map[int]struct{}, len(results))
	for _, entry := range combined {
		out = append(out, entry.result)
		for idx := range entry.used {
			consumedByReason[idx] = struct{}{}
		}
	}

	for idx, result := range results {
		reason := strings.TrimSpace(result.SkipReason)
		if !result.Skipped || !isRuleFailureReason(reason) {
			out = append(out, result)
			continue
		}
		if _, groupedByRule := indexesInCombinedRule[idx]; groupedByRule {
			continue
		}
		if _, groupedByReason := consumedByReason[idx]; groupedByReason {
			continue
		}
		out = append(out, result)
	}

	return out
}

func collectRuleSkipGroups(results []api.DupeCheckResult) (map[string][]int, map[string][]int) {
	groupedByRule := make(map[string][]int)
	groupedByReason := make(map[string][]int)
	for idx, result := range results {
		reason := strings.TrimSpace(result.SkipReason)
		if !result.Skipped || !isRuleFailureReason(reason) {
			continue
		}
		if len(result.SkipRules) > 0 {
			for _, rule := range result.SkipRules {
				key := strings.ToLower(strings.TrimSpace(rule))
				if key == "" {
					continue
				}
				groupedByRule[key] = append(groupedByRule[key], idx)
			}
			continue
		}
		key := strings.ToLower(reason)
		groupedByReason[key] = append(groupedByReason[key], idx)
	}
	return groupedByRule, groupedByReason
}

func buildRuleGroupedResults(results []api.DupeCheckResult, groupedByRule map[string][]int) ([]groupedResult, map[int]struct{}) {
	combined := make([]groupedResult, 0, len(groupedByRule))
	indexesInCombinedRule := make(map[int]struct{}, len(results))
	for _, rule := range sortedStringKeys(groupedByRule) {
		group := uniqueSortedIndexes(groupedByRule[rule])
		if len(group) < 2 {
			continue
		}
		for _, idx := range group {
			indexesInCombinedRule[idx] = struct{}{}
		}
		base := results[group[0]]
		base.Tracker = strings.Join(trackersForIndexes(results, group), ", ")
		base.SkipRules = []string{rule}
		combined = append(combined, groupedResult{result: base, used: indexSliceToSet(group)})
	}
	return combined, indexesInCombinedRule
}

func buildReasonGroupedResults(results []api.DupeCheckResult, groupedByReason map[string][]int) []groupedResult {
	combined := make([]groupedResult, 0, len(groupedByReason))
	for _, reason := range sortedStringKeys(groupedByReason) {
		group := uniqueSortedIndexes(groupedByReason[reason])
		if len(group) < 2 {
			continue
		}
		base := results[group[0]]
		base.Tracker = strings.Join(trackersForIndexes(results, group), ", ")
		combined = append(combined, groupedResult{result: base, used: indexSliceToSet(group)})
	}
	return combined
}

func uniqueSortedIndexes(indexes []int) []int {
	out := make([]int, 0, len(indexes))
	seen := make(map[int]struct{}, len(indexes))
	for _, idx := range indexes {
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

func trackersForIndexes(results []api.DupeCheckResult, indexes []int) []string {
	trackers := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		trackers = append(trackers, strings.TrimSpace(results[idx].Tracker))
	}
	sort.Strings(trackers)
	return trackers
}

func sortedStringKeys(values map[string][]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func indexSliceToSet(indexes []int) map[int]struct{} {
	set := make(map[int]struct{}, len(indexes))
	for _, idx := range indexes {
		set[idx] = struct{}{}
	}
	return set
}

func isRuleFailureReason(reason string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(reason)), "rule check failed")
}
