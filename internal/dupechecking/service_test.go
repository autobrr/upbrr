// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type testSearchHandler struct {
	delay      time.Duration
	entries    []api.DupeEntry
	notes      []string
	err        error
	panicValue any
}

func (h testSearchHandler) Search(ctx context.Context, _ api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	if h.panicValue != nil {
		panic(h.panicValue)
	}
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

type searchHandlerFunc func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error)

func (f searchHandlerFunc) Search(ctx context.Context, meta api.PreparedMetadata, tracker string) ([]api.DupeEntry, []string, error) {
	return f(ctx, meta, tracker)
}

type recordingLogger struct {
	api.NopLogger
	mu    sync.Mutex
	warns []string
}

func (l *recordingLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
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

func TestCheckRedactsSearchErrorsFromResultProgressAndLogs(t *testing.T) {
	t.Parallel()

	logger := &recordingLogger{}
	svc := NewService(config.Config{}, logger)
	svc.handlers = map[string]searchHandler{
		"DP": testSearchHandler{err: errors.New(`unit3d: request: Get "https://tracker.example/api/torrents/filter?api_token=secret-api-token&name=Example.Release.2026.1080p-GRP": context deadline exceeded`)},
	}

	var updatesMu sync.Mutex
	updates := make([]api.DupeProgressUpdate, 0, 3)
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		updatesMu.Lock()
		updates = append(updates, update)
		updatesMu.Unlock()
	})

	summary, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: `D:\media\Example's.Release.2026-GRP`}, []string{"DP"})
	if err != nil {
		t.Fatalf("check dupes: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if strings.Contains(result.Error, "secret-api-token") || strings.Contains(strings.Join(result.Notes, " "), "secret-api-token") {
		t.Fatal("expected dupe result error details to redact request secret")
	}
	if !strings.Contains(result.Error, "api_token=[REDACTED]") {
		t.Fatal("expected dupe result to preserve redacted request context")
	}

	updatesMu.Lock()
	capturedUpdates := slices.Clone(updates)
	updatesMu.Unlock()
	foundFailed := false
	for _, update := range capturedUpdates {
		if strings.Contains(update.Message, "secret-api-token") || strings.Contains(update.Result.Error, "secret-api-token") {
			t.Fatal("expected dupe progress to redact request secret")
		}
		if update.Status == "failed" {
			foundFailed = true
			if !strings.Contains(update.Message, "api_token=[REDACTED]") {
				t.Fatal("expected failed progress to preserve redacted request context")
			}
		}
	}
	if !foundFailed {
		t.Fatal("expected failed dupe progress update")
	}

	logger.mu.Lock()
	warns := slices.Clone(logger.warns)
	logger.mu.Unlock()
	if strings.Contains(strings.Join(warns, " "), "secret-api-token") {
		t.Fatal("expected dupe warning to redact request secret before logger boundary")
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

func TestCheckRuleSkipTakesPrecedenceOverBannedGroup(t *testing.T) {
	t.Parallel()

	svc := NewService(config.Config{}, api.NopLogger{})
	called := false
	svc.handlers = map[string]searchHandler{
		"DP": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			called = true
			return nil, nil, nil
		}),
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/example",
		Tag:        "-FGT",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"DP": {{Rule: "example_rule", Reason: "example rule failure"}},
		},
	}

	summary, err := svc.Check(context.Background(), meta, []string{"DP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatalf("expected terminal skip before tracker search")
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if !result.Skipped || result.Status != "skipped" {
		t.Fatalf("expected skipped result, got %#v", result)
	}
	if result.SkipCode == "banned_group" {
		t.Fatalf("expected rule skip to take precedence over banned group, got %#v", result)
	}
	if len(result.SkipRules) != 1 || result.SkipRules[0] != "example_rule" {
		t.Fatalf("expected rule skip metadata, got %#v", result)
	}
	if !strings.Contains(result.SkipReason, "example rule failure") {
		t.Fatalf("expected rule skip reason, got %q", result.SkipReason)
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

func TestCheckSkipsBannedGroupBeforeSearch(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")},
	}
	svc := NewService(cfg, api.NopLogger{})
	called := false
	svc.handlers = map[string]searchHandler{
		"DP": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			called = true
			return []api.DupeEntry{{Name: "Example.Release.2026.1080p-GRP"}}, nil, nil
		}),
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x", Tag: "-FGT"}, []string{"DP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatalf("expected banned group to skip before tracker search")
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if !result.Skipped || result.Status != "skipped" {
		t.Fatalf("expected skipped banned-group result, got %#v", result)
	}
	if result.SkipCode != "banned_group" {
		t.Fatalf("expected banned_group skip code, got %q", result.SkipCode)
	}
	if result.SkipReason != "group fgt is banned on DP" {
		t.Fatalf("expected banned-group reason, got %q", result.SkipReason)
	}
}

func TestCheckDoesNotCollapseUnrelatedTAoESubstringGroup(t *testing.T) {
	t.Parallel()

	svc := NewService(config.Config{}, api.NopLogger{})
	called := false
	svc.handlers = map[string]searchHandler{
		"DP": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			called = true
			return nil, nil, nil
		}),
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x", Tag: "-NotTAoE"}, []string{"DP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected unrelated TAoE substring group to reach tracker search")
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Skipped || result.SkipCode == "banned_group" {
		t.Fatalf("expected unskipped result for unrelated TAoE substring, got %#v", result)
	}
}

func TestCheckRefreshesDynamicBannedGroupsBeforeSearch(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Path; got != "/api/blacklists/releasegroups" {
			t.Errorf("unexpected path %q", got)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer aither-key" {
			t.Error("unexpected auth header")
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"name":"GRP"}],"meta":{"next_cursor":""}}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key", URL: server.URL},
			},
		},
	}
	svc := NewService(cfg, api.NopLogger{})
	called := false
	svc.handlers = map[string]searchHandler{
		"AITHER": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			called = true
			return []api.DupeEntry{{Name: "Example.Release.2026.1080p-GRP"}}, nil, nil
		}),
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x", Tag: "-GRP"}, []string{"AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected banned groups fetch, got %d requests", requests)
	}
	if called {
		t.Fatalf("expected fetched banned group to skip before tracker search")
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if !result.Skipped || result.Status != "skipped" || result.SkipCode != "banned_group" {
		t.Fatalf("expected banned group skip, got %#v", result)
	}
	if result.SkipReason != "group grp is banned on AITHER" {
		t.Fatalf("expected dynamic banned-group reason, got %q", result.SkipReason)
	}
}

func TestCheckSkipsDynamicBannedRefreshForEmptyEffectiveGroup(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"data":[],"meta":{"next_cursor":""}}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "aither-key", URL: server.URL},
			},
		},
	}
	svc := NewService(cfg, api.NopLogger{})
	called := false
	svc.handlers = map[string]searchHandler{
		"AITHER": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			called = true
			return nil, nil, nil
		}),
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x", Tag: "-"}, []string{"AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected no dynamic banned refresh for empty effective group, got %d request(s)", got)
	}
	if !called {
		t.Fatalf("expected tracker search to proceed")
	}
	if len(summary.Results) != 1 || summary.Results[0].Skipped {
		t.Fatalf("expected unskipped result, got %#v", summary.Results)
	}
}

func TestCheckBannedGroupFailureIncludesModeAndAction(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "upbrr.db")},
	}
	svc := NewService(cfg, api.NopLogger{})

	cachePath := filepath.Join(tempDir, "cache", "banned", "RHD_banned_groups.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("create banned cache dir: %v", err)
	}
	if err := os.Mkdir(cachePath, 0o700); err != nil {
		t.Fatalf("create unreadable banned cache path: %v", err)
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x", Tag: "-CustomRHD"}, []string{"RHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Status != "failed" {
		t.Fatalf("expected failed status, got %#v", result)
	}
	assertBannedGroupFailureMessage(t, result.Error)
	if len(result.Notes) != 1 {
		t.Fatalf("expected one failure note, got %#v", result.Notes)
	}
	assertBannedGroupFailureMessage(t, result.Notes[0])
}

func assertBannedGroupFailureMessage(t *testing.T, message string) {
	t.Helper()
	for _, want := range []string{
		"banned group check failed:",
		"read banned groups",
		"mode=non-interactive dupe check",
		"action=fix banned-group cache or dynamic banned-group configuration, then retry",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected failure message to contain %q, got %q", want, message)
		}
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

func TestCheckCancelWaitsForInFlightWorkerBeforeReturning(t *testing.T) {
	svc := NewService(config.Config{}, api.NopLogger{})
	started := make(chan struct{})
	release := make(chan struct{})
	svc.handlers = map[string]searchHandler{
		"AITHER": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			close(started)
			<-release
			return []api.DupeEntry{{Name: "release"}}, nil, nil
		}),
	}

	var mu sync.Mutex
	updates := make([]api.DupeProgressUpdate, 0)
	progressCtx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(progressCtx)

	returned := make(chan error, 1)
	go func() {
		_, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER"})
		returned <- err
	}()

	<-started
	cancel()

	select {
	case err := <-returned:
		t.Fatalf("Check returned before active worker finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-returned:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("expected cancellation error after worker finished, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Check did not return after active worker finished")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, update := range updates {
		if update.Tracker == "AITHER" && update.Status == "completed" {
			t.Fatalf("expected no terminal progress after cancellation, got %+v", updates)
		}
	}
}

func TestCheckCancelWorkerWaitIsBounded(t *testing.T) {
	logger := &recordingLogger{}
	svc := NewService(config.Config{}, logger)
	svc.cancelDrainTimeout = 20 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	svc.handlers = map[string]searchHandler{
		"AITHER": searchHandlerFunc(func(context.Context, api.PreparedMetadata, string) ([]api.DupeEntry, []string, error) {
			close(started)
			<-release
			return []api.DupeEntry{{Name: "release"}}, nil, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER"})
		returned <- err
	}()

	<-started
	cancel()

	select {
	case err := <-returned:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("expected cancellation error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Check did not return after cancel drain timeout")
	}
	close(release)

	logger.mu.Lock()
	warns := strings.Join(logger.warns, "\n")
	logger.mu.Unlock()
	if !strings.Contains(warns, "timed out waiting for canceled workers") {
		t.Fatalf("expected bounded wait warning, got %q", warns)
	}
}

func TestCheckContainsHandlerPanicAsFailedTrackerResult(t *testing.T) {
	t.Parallel()
	secret := "0123456789abcdef0123456789abcdef"
	logger := &recordingLogger{}
	svc := NewService(config.Config{}, logger)
	svc.handlers = map[string]searchHandler{
		"AITHER": testSearchHandler{panicValue: "handler token=" + secret},
		"BLU":    testSearchHandler{entries: []api.DupeEntry{{Name: "release"}}},
	}

	var mu sync.Mutex
	updates := make([]api.DupeProgressUpdate, 0)
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	})

	summary, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER", "BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(summary.Results))
	}

	failed, ok := findResultByTracker(summary.Results, "AITHER")
	if !ok {
		t.Fatalf("missing AITHER result")
	}
	assertPanicFailureResult(t, failed, secret)

	success, ok := findResultByTracker(summary.Results, "BLU")
	if !ok {
		t.Fatalf("missing BLU result")
	}
	if success.Status != "completed" {
		t.Fatalf("expected sibling completed status, got %q", success.Status)
	}

	mu.Lock()
	terminalFailed := false
	for _, update := range updates {
		if update.Tracker == "AITHER" && update.Status == "failed" && update.Result.Status == "failed" {
			terminalFailed = true
			break
		}
	}
	mu.Unlock()
	if !terminalFailed {
		t.Fatalf("expected terminal failed progress for panicking tracker, got %+v", updates)
	}

	logger.mu.Lock()
	warns := strings.Join(logger.warns, "\n")
	logger.mu.Unlock()
	if strings.Contains(warns, secret) {
		t.Fatalf("expected panic log to redact secret, got %q", warns)
	}
	if !strings.Contains(warns, "[REDACTED]") {
		t.Fatalf("expected redacted panic log detail, got %q", warns)
	}
}

func TestCheckContainsFilterPanicAsFailedTrackerResult(t *testing.T) {
	t.Parallel()
	secret := "abcdefabcdefabcdefabcdefabcdefab"
	svc := NewService(config.Config{}, api.NopLogger{})
	svc.handlers = map[string]searchHandler{
		"AITHER": testSearchHandler{entries: []api.DupeEntry{{Name: "release"}}},
		"BLU":    testSearchHandler{entries: []api.DupeEntry{{Name: "release"}}},
	}
	svc.filter = func(dupes []api.DupeEntry, meta api.PreparedMetadata, tracker string, cfg config.Config, logger api.Logger) ([]api.DupeEntry, api.DupeMatch) {
		if tracker == "AITHER" {
			panic("filter api_token=" + secret)
		}
		return FilterDupes(dupes, meta, tracker, cfg, logger)
	}

	summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER", "BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(summary.Results))
	}

	failed, ok := findResultByTracker(summary.Results, "AITHER")
	if !ok {
		t.Fatalf("missing AITHER result")
	}
	assertPanicFailureResult(t, failed, secret)

	success, ok := findResultByTracker(summary.Results, "BLU")
	if !ok {
		t.Fatalf("missing BLU result")
	}
	if success.Status != "completed" {
		t.Fatalf("expected sibling completed status, got %q", success.Status)
	}
}

func TestCheckContainsRunningProgressReporterPanic(t *testing.T) {
	t.Parallel()
	secret := "0123456789abcdef0123456789abcdef"
	logger := &recordingLogger{}
	svc := NewService(config.Config{}, logger)
	svc.handlers = map[string]searchHandler{
		"AITHER": testSearchHandler{entries: []api.DupeEntry{{Name: "release"}}},
	}

	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		if update.Status == "running" {
			panic("progress token=" + secret)
		}
	})

	summary, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Status != "completed" {
		t.Fatalf("expected completed result after reporter panic, got %#v", result)
	}

	logger.mu.Lock()
	warns := strings.Join(logger.warns, "\n")
	logger.mu.Unlock()
	if !strings.Contains(warns, "progress reporter panicked") {
		t.Fatalf("expected progress reporter panic warning, got %q", warns)
	}
	if strings.Contains(warns, secret) {
		t.Fatalf("expected progress reporter warning to redact secret, got %q", warns)
	}
	if !strings.Contains(warns, "[REDACTED]") {
		t.Fatalf("expected redacted progress reporter detail, got %q", warns)
	}
}

func TestCheckContainsTerminalProgressReporterPanic(t *testing.T) {
	t.Parallel()
	logger := &recordingLogger{}
	svc := NewService(config.Config{}, logger)
	svc.handlers = map[string]searchHandler{
		"AITHER": testSearchHandler{entries: []api.DupeEntry{{Name: "release"}}},
	}

	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		if update.Status == "completed" {
			panic("terminal progress failed")
		}
	})

	summary, err := svc.Check(ctx, api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Status != "completed" || result.Tracker != "AITHER" {
		t.Fatalf("expected completed AITHER result after terminal reporter panic, got %#v", result)
	}

	logger.mu.Lock()
	warns := strings.Join(logger.warns, "\n")
	logger.mu.Unlock()
	if !strings.Contains(warns, "progress reporter panicked") {
		t.Fatalf("expected terminal progress reporter panic warning, got %q", warns)
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
	if combined.SkipCode != "" {
		t.Fatalf("expected rule grouped result not to carry structured skip code, got %q", combined.SkipCode)
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

func TestCheckPreservesVisibleSkipCodeMarkerNotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		notes       []string
		wantCode    string
		wantSkipped bool
	}{
		{
			name:        "visible marker without metadata",
			notes:       []string{"skip-code: tracker-visible note"},
			wantCode:    "",
			wantSkipped: false,
		},
		{
			name:        "metadata plus visible skip reason",
			notes:       []string{noteSkipCode("test_skip_code"), noteSkip("tracker skipped"), "skip-code: tracker-visible note"},
			wantCode:    "test_skip_code",
			wantSkipped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := NewService(config.Config{}, api.NopLogger{})
			svc.handlers = map[string]searchHandler{
				"AITHER": testSearchHandler{notes: tc.notes},
			}

			summary, err := svc.Check(context.Background(), api.PreparedMetadata{SourcePath: "x"}, []string{"AITHER"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(summary.Results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(summary.Results))
			}
			result := summary.Results[0]
			if result.SkipCode != tc.wantCode {
				t.Fatalf("expected skip code %q, got %q", tc.wantCode, result.SkipCode)
			}
			if result.Skipped != tc.wantSkipped {
				t.Fatalf("expected skipped=%t, got %#v", tc.wantSkipped, result)
			}
			if !slices.Contains(result.Notes, "skip-code: tracker-visible note") {
				t.Fatalf("expected visible skip-code note to remain, got %#v", result.Notes)
			}
			for _, note := range result.Notes {
				if strings.Contains(note, skipCodeNotePrefix) {
					t.Fatalf("internal skip-code metadata leaked into display notes: %#v", result.Notes)
				}
			}
		})
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

func TestSplitSkipCodeNotesOnlyRemovesInternalMetadata(t *testing.T) {
	t.Parallel()

	notes := []string{
		noteSkipCode("test_skip_code"),
		"skip-code: tracker-visible note",
		"  SKIP-CODE: tracker-visible mixed case  ",
		noteSkipCode("secondary_code"),
	}

	code, displayNotes := splitSkipCodeNotes(notes)
	if code != "test_skip_code" {
		t.Fatalf("expected first skip code %q, got %q", "test_skip_code", code)
	}
	if len(displayNotes) != 2 {
		t.Fatalf("expected 2 display notes, got %#v", displayNotes)
	}
	if displayNotes[0] != "skip-code: tracker-visible note" {
		t.Fatalf("expected visible marker note to remain, got %#v", displayNotes)
	}
	if displayNotes[1] != "  SKIP-CODE: tracker-visible mixed case  " {
		t.Fatalf("expected mixed-case visible marker note to remain, got %#v", displayNotes)
	}
	for _, note := range displayNotes {
		if strings.Contains(note, skipCodeNotePrefix) {
			t.Fatalf("internal skip-code metadata leaked into display notes: %#v", displayNotes)
		}
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

func assertPanicFailureResult(t *testing.T, result api.DupeCheckResult, secret string) {
	t.Helper()
	if result.Status != "failed" {
		t.Fatalf("expected failed status, got %q", result.Status)
	}
	if result.Skipped {
		t.Fatalf("expected panic failure not to be marked skipped")
	}
	if result.SkipReason != "" || len(result.SkipRules) != 0 {
		t.Fatalf("expected panic failure not to carry skip fields, got reason=%q rules=%#v", result.SkipReason, result.SkipRules)
	}
	if !strings.Contains(result.Error, "dupe search panicked") {
		t.Fatalf("expected panic failure error, got %q", result.Error)
	}
	if strings.Contains(result.Error, secret) || strings.Contains(strings.Join(result.Notes, "\n"), secret) {
		t.Fatalf("expected panic failure text to redact secret, got error=%q notes=%#v", result.Error, result.Notes)
	}
	if !strings.Contains(result.Error, "[REDACTED]") {
		t.Fatalf("expected redacted panic detail, got %q", result.Error)
	}
}
