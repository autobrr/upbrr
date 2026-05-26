// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type testSearchHandler struct {
	delay   time.Duration
	entries []api.DupeEntry
	notes   []string
	err     error
}

func (h testSearchHandler) Search(ctx context.Context, _ api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	if h.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("context canceled: %w", ctx.Err())
		case <-time.After(h.delay):
		}
	}
	if h.err != nil {
		return nil, nil, h.err
	}
	return h.entries, h.notes, nil
}

func TestCheckMissingSourcePath(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	_, err := svc.Check(context.Background(), api.PreparedMetadata{}, []string{"AITHER"})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestCheckNoTrackers(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Notes) == 0 {
		t.Fatalf("expected summary note")
	}
}

func TestCheckSkipsRuleFailures(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {{Rule: "require_unique_id", Reason: "missing MediaInfo Unique ID"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if !result.Skipped {
		t.Fatalf("expected skipped result")
	}
	if result.SkipReason == "" {
		t.Fatalf("expected skip reason")
	}
	if strings.Contains(strings.Join(result.Notes, " "), "api_key") {
		t.Fatalf("expected skipped tracker to bypass API checks")
	}
}

func TestCheckSkipsClaimRuleFailures(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"BTN": {{Rule: "claim_active", Reason: "BTN has an active claim for this release; approximately 11 hours remain in the 48-hour claim window"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"BTN"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if !result.Skipped {
		t.Fatalf("expected skipped result")
	}
	if !strings.Contains(result.SkipReason, "active claim") {
		t.Fatalf("expected claim skip reason, got %q", result.SkipReason)
	}
	if !strings.Contains(result.SkipReason, "11 hours remain") {
		t.Fatalf("expected hours remaining in skip reason, got %q", result.SkipReason)
	}
	if len(result.SkipRules) != 1 || result.SkipRules[0] != "claim_active" {
		t.Fatalf("expected claim_active skip rule, got %#v", result.SkipRules)
	}
}

func TestCheckUnsupportedTrackerMarkedSkipped(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x"}, []string{"UNKNOWN"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected one result")
	}
	result := summary.Results[0]
	if !result.Skipped {
		t.Fatalf("expected skipped")
	}
	if !strings.Contains(strings.ToLower(result.SkipReason), "not implemented") {
		t.Fatalf("expected not implemented reason, got %q", result.SkipReason)
	}
}

func TestCheckTrackerFailureDoesNotAbortWholeSummary(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	svc.handlers = map[string]searchHandler{
		"AITHER": testSearchHandler{err: errors.New("boom")},
		"BLU":    testSearchHandler{entries: []api.DupeEntry{{Name: "release"}}},
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER", "BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(summary.Results))
	}

	byTracker := make(map[string]api.DupeCheckResult, len(summary.Results))
	for _, result := range summary.Results {
		byTracker[result.Tracker] = result
	}

	failed, ok := byTracker["AITHER"]
	if !ok {
		t.Fatalf("missing AITHER result")
	}
	if failed.Status != "failed" {
		t.Fatalf("expected failed status, got %q", failed.Status)
	}
	if failed.Error == "" {
		t.Fatalf("expected tracker error")
	}

	success, ok := byTracker["BLU"]
	if !ok {
		t.Fatalf("missing BLU result")
	}
	if success.Status != "completed" {
		t.Fatalf("expected completed status, got %q", success.Status)
	}
}

func TestCheckGroupsSkippedTrackersWithSameRuleFailure(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {{Rule: "require_unique_id", Reason: "missing MediaInfo Unique ID"}},
			"BLU":    {{Rule: "require_unique_id", Reason: "missing MediaInfo Unique ID"}},
			"HDB":    {{Rule: "require_bdinfo", Reason: "missing BDInfo summary"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"AITHER", "BLU", "HDB"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 grouped results, got %d", len(summary.Results))
	}

	combined, foundCombined := findResultByTracker(summary.Results, "AITHER, BLU")
	if !foundCombined {
		t.Fatalf("expected combined AITHER, BLU result")
	}
	if !combined.Skipped {
		t.Fatalf("expected combined result to be skipped")
	}
	if !strings.Contains(combined.SkipReason, "missing MediaInfo Unique ID") {
		t.Fatalf("expected combined skip reason to contain original failure, got %q", combined.SkipReason)
	}

	separate, foundSeparate := findResultByTracker(summary.Results, "HDB")
	if !foundSeparate {
		t.Fatalf("expected separate HDB result")
	}
	if !separate.Skipped {
		t.Fatalf("expected HDB result to stay skipped")
	}
	if !strings.Contains(separate.SkipReason, "missing BDInfo summary") {
		t.Fatalf("expected HDB reason to be preserved, got %q", separate.SkipReason)
	}
}

func TestCheckGroupsSkippedTrackersByRuleKeyWhenReasonsDiffer(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"PTP": {{Rule: "require_movie_only", Reason: "tv content requires movie category"}},
			"RF":  {{Rule: "require_movie_only", Reason: "category tv is not movie"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"PTP", "RF"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 grouped result, got %d", len(summary.Results))
	}
	combined := summary.Results[0]
	if combined.Tracker != "PTP, RF" {
		t.Fatalf("expected combined tracker list, got %q", combined.Tracker)
	}
	if len(combined.SkipRules) != 1 || combined.SkipRules[0] != "require_movie_only" {
		t.Fatalf("expected grouped skip rule key, got %#v", combined.SkipRules)
	}
}

func TestCheckGroupsMultiRuleFailuresPerRuleKey(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {
				{Rule: "require_movie_only", Reason: "tv category blocked"},
				{Rule: "min_resolution", Reason: "resolution 720p below 1080p"},
			},
			"BLU": {{Rule: "require_movie_only", Reason: "not a movie category"}},
			"HDB": {{Rule: "min_resolution", Reason: "requires 1080p minimum"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"AITHER", "BLU", "HDB"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 grouped results, got %d", len(summary.Results))
	}

	movieGroup, ok := findResultByTracker(summary.Results, "AITHER, BLU")
	if !ok {
		t.Fatalf("expected movie rule group")
	}
	if len(movieGroup.SkipRules) != 1 || movieGroup.SkipRules[0] != "require_movie_only" {
		t.Fatalf("expected movie rule group key, got %#v", movieGroup.SkipRules)
	}

	resolutionGroup, ok := findResultByTracker(summary.Results, "AITHER, HDB")
	if !ok {
		t.Fatalf("expected resolution rule group")
	}
	if len(resolutionGroup.SkipRules) != 1 || resolutionGroup.SkipRules[0] != "min_resolution" {
		t.Fatalf("expected resolution rule group key, got %#v", resolutionGroup.SkipRules)
	}
}

func TestCheckGroupsANTAndNBLWithMatchingRuleKeys(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"ANT": {{Rule: "require_movie_only", Reason: "category tv is not movie"}},
			"RF":  {{Rule: "require_movie_only", Reason: "category tv is not movie"}},
			"NBL": {{Rule: "require_tv_only", Reason: "category movie is not tv"}},
			"STC": {{Rule: "require_tv_only", Reason: "category movie is not tv"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"ANT", "RF", "NBL", "STC"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 grouped results, got %d", len(summary.Results))
	}

	movieGroup, ok := findResultByTracker(summary.Results, "ANT, RF")
	if !ok {
		t.Fatalf("expected movie rule group")
	}
	if len(movieGroup.SkipRules) != 1 || movieGroup.SkipRules[0] != "require_movie_only" {
		t.Fatalf("expected require_movie_only key, got %#v", movieGroup.SkipRules)
	}

	tvGroup, ok := findResultByTracker(summary.Results, "NBL, STC")
	if !ok {
		t.Fatalf("expected tv rule group")
	}
	if len(tvGroup.SkipRules) != 1 || tvGroup.SkipRules[0] != "require_tv_only" {
		t.Fatalf("expected require_tv_only key, got %#v", tvGroup.SkipRules)
	}
}

func TestCheckEmitsPerTrackerProgressUpdates(t *testing.T) {
	t.Parallel()
	svc := NewService(config.Config{}, api.NopLogger{})
	svc.handlers = map[string]searchHandler{
		"AITHER": testSearchHandler{delay: 15 * time.Millisecond},
		"BLU":    testSearchHandler{delay: 15 * time.Millisecond},
	}

	var mu sync.Mutex
	updates := make([]api.DupeProgressUpdate, 0)
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	})

	_, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER", "BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(updates) == 0 {
		t.Fatalf("expected progress updates")
	}

	completedByTracker := make(map[string]bool)
	for _, update := range updates {
		if strings.EqualFold(update.Status, "completed") || strings.EqualFold(update.Status, "failed") || strings.EqualFold(update.Status, "skipped") {
			completedByTracker[strings.ToUpper(strings.TrimSpace(update.Tracker))] = true
		}
	}
	if !completedByTracker["AITHER"] || !completedByTracker["BLU"] {
		t.Fatalf("expected terminal progress events for both trackers, got %+v", completedByTracker)
	}
}

func findResultByTracker(results []api.DupeCheckResult, tracker string) (api.DupeCheckResult, bool) {
	for _, result := range results {
		if result.Tracker == tracker {
			return result, true
		}
	}
	return api.DupeCheckResult{}, false
}
