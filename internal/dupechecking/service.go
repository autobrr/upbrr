// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

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
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxDupeWorkers = 4

type Service struct {
	cfg      config.Config
	logger   api.Logger
	http     *http.Client
	tracker  *trackerdata.Client
	handlers map[string]searchHandler
}

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
	}
}

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
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
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
	workerCount := total
	if workerCount > maxDupeWorkers {
		workerCount = maxDupeWorkers
	}

	var workers sync.WaitGroup
	for idx := 0; idx < workerCount; idx++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for tracker := range jobs {
				if ctx.Err() != nil {
					return
				}
				api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
					SourcePath: meta.SourcePath,
					Tracker:    tracker,
					Status:     "running",
					Message:    "searching",
					Total:      total,
				})
				results <- s.checkTracker(ctx, meta, tracker)
			}
		}()
	}

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
			return api.DupeCheckSummary{}, ctx.Err()
		case result := <-results:
			completed++
			summary.Results = append(summary.Results, result)
			api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
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

	workers.Wait()
	summary.Results = groupRuleSkippedResults(summary.Results)
	sort.Slice(summary.Results, func(i, j int) bool {
		return summary.Results[i].Tracker < summary.Results[j].Tracker
	})

	return summary, nil
}

func (s *Service) checkTracker(ctx context.Context, meta api.PreparedMetadata, tracker string) api.DupeCheckResult {
	result := api.DupeCheckResult{Tracker: tracker, CheckedAt: time.Now().UTC(), Status: "completed"}
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
	if reason, ok := parseSkipReason(notes); ok {
		result.Skipped = true
		result.SkipReason = reason
		result.Status = "skipped"
	}
	result.Notes = append(result.Notes, notes...)
	result.Raw = trimEntries(raw)
	if result.Skipped {
		s.logger.Debugf("dupechecking: handler marked skipped for %s (%s)", tracker, meta.SourcePath)
		return result
	}

	filtered, match := FilterDupes(result.Raw, meta, tracker, s.cfg, s.logger)
	result.Filtered = filtered
	result.Match = match
	result.HasDupes = len(filtered) > 0
	s.logger.Debugf("dupechecking: %s checked for %s raw=%d filtered=%d dupes=%t", tracker, meta.SourcePath, len(result.Raw), len(filtered), result.HasDupes)
	return result
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
