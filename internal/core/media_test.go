// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type imageHostCall struct {
	host     string
	fallback bool
}

type imageHostBehavior struct {
	release <-chan struct{}
	err     error
}

type imageUploadCallResult struct {
	result api.UploadImagesResult
	err    error
}

type mediaLogEntry struct {
	level   string
	message string
}

type recordingMediaLogger struct {
	mu      sync.Mutex
	entries []mediaLogEntry
}

func (l *recordingMediaLogger) record(level string, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, mediaLogEntry{level: level, message: fmt.Sprintf(format, args...)})
}

func (l *recordingMediaLogger) Tracef(format string, args ...any) {
	l.record("TRACE", format, args...)
}

func (l *recordingMediaLogger) Debugf(format string, args ...any) {
	l.record("DEBUG", format, args...)
}

func (l *recordingMediaLogger) Infof(format string, args ...any) {
	l.record("INFO", format, args...)
}

func (l *recordingMediaLogger) Warnf(format string, args ...any) {
	l.record("WARN", format, args...)
}

func (l *recordingMediaLogger) Errorf(format string, args ...any) {
	l.record("ERROR", format, args...)
}

func (l *recordingMediaLogger) countLevelContaining(level string, value string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, entry := range l.entries {
		if entry.level == level && strings.Contains(entry.message, value) {
			count++
		}
	}
	return count
}

type barrierImageHostingService struct {
	entered   chan imageHostCall
	behaviors map[string]imageHostBehavior
}

func (*barrierImageHostingService) ListCandidates(context.Context, api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *barrierImageHostingService) Upload(
	ctx context.Context,
	_ api.ImageHostingSubject,
	host string,
	usageScope string,
	images []api.ScreenshotImage,
) ([]api.UploadedImageLink, error) {
	target, _ := api.ImageUploadProgressTargetFromContext(ctx)
	s.entered <- imageHostCall{host: host, fallback: target.Fallback}
	behavior := s.behaviors[host]
	select {
	case <-behavior.release:
	case <-ctx.Done():
		return nil, fmt.Errorf("context canceled: %w", ctx.Err())
	}
	links := make([]api.UploadedImageLink, 0, len(images))
	for _, image := range images {
		links = append(links, api.UploadedImageLink{
			ImagePath:  image.Path,
			Host:       host,
			UsageScope: usageScope,
			RawURL:     "https://images.example.invalid/" + host,
		})
	}
	return links, behavior.err
}

func TestUploadImagesFallbackStartsBeforeUnrelatedPrimaryCompletes(t *testing.T) {
	t.Parallel()

	registry := mediaImageHostRegistry(t)
	releases := map[string]chan struct{}{
		"pixhost":   make(chan struct{}),
		"onlyimage": make(chan struct{}),
		"imgbb":     make(chan struct{}),
		"ptscreens": make(chan struct{}),
	}
	service := &barrierImageHostingService{
		entered: make(chan imageHostCall, 4),
		behaviors: map[string]imageHostBehavior{
			"pixhost":   {release: releases["pixhost"], err: errors.New("pixhost failed")},
			"onlyimage": {release: releases["onlyimage"], err: errors.New("onlyimage failed")},
			"imgbb":     {release: releases["imgbb"]},
			"ptscreens": {release: releases["ptscreens"]},
		},
	}
	logger := &recordingMediaLogger{}
	module := &mediaModule{
		cfg: config.Config{ImageHosting: config.ImageHostingConfig{
			Host1: "pixhost",
			Host2: "onlyimage",
			Host3: "imgbb",
			Host4: "ptscreens",
		}},
		images:   service,
		logger:   logger,
		registry: registry,
	}
	resolvedFallbacks, err := module.resolveFallbackImageUploadTargets(
		"pixhost",
		[]string{"ONE", "TWO"},
		[]string{"pixhost", "onlyimage"},
		api.UploadSubject{},
	)
	if err != nil {
		t.Fatalf("resolve fallback targets: %v", err)
	}
	if got := uploadTargetHosts(resolvedFallbacks); !slices.Equal(got, []string{"imgbb", "ptscreens"}) {
		t.Fatalf("resolved fallback hosts = %v, targets=%#v", got, resolvedFallbacks)
	}
	done := make(chan imageUploadCallResult, 1)
	go func() {
		result, err := module.uploadImagesToTargetsWithFallback(
			context.Background(),
			api.UploadSubject{SourcePath: "C:\\media\\Example.Release.2026.mkv"},
			"pixhost",
			nil,
			[]trackers.ImageUploadTarget{
				{
					Host:       "pixhost",
					UsageScope: "global",
					Trackers:   []string{"ONE"},
				},
				{
					Host:       "onlyimage",
					UsageScope: "global",
					Trackers:   []string{"TWO"},
				},
			},
			[]api.ScreenshotImage{{Path: "screen.png"}},
		)
		done <- imageUploadCallResult{result: result, err: err}
	}()

	primary := []imageHostCall{
		receiveImageHostCall(t, service.entered),
		receiveImageHostCall(t, service.entered),
	}
	if got := sortedCallHosts(primary); !slices.Equal(got, []string{"onlyimage", "pixhost"}) {
		t.Fatalf("primary hosts = %v", got)
	}
	close(releases["pixhost"])
	firstFallback := receiveImageHostCall(t, service.entered)
	if firstFallback.host != "imgbb" || !firstFallback.fallback {
		t.Fatalf("first fallback = %#v, want imgbb before onlyimage completes", firstFallback)
	}
	close(releases["onlyimage"])
	secondFallback := receiveImageHostCall(t, service.entered)
	if secondFallback.host != "ptscreens" || !secondFallback.fallback {
		t.Fatalf("second fallback = %#v", secondFallback)
	}
	close(releases["ptscreens"])
	close(releases["imgbb"])
	callResult := receiveImageUploadCallResult(t, done)
	if callResult.err != nil {
		t.Fatalf("upload images with fallback: %v", callResult.err)
	}
	result := callResult.result
	if len(result.Failures) != 0 || len(result.Links) != 4 {
		t.Fatalf("fallback result = %#v", result)
	}
	if !slices.Equal(result.FailedHosts, []string{"onlyimage", "pixhost"}) {
		t.Fatalf("failed hosts = %v", result.FailedHosts)
	}
	if len(result.Attempts) != 4 {
		t.Fatalf("host attempts = %#v", result.Attempts)
	}
	failedAttempts, fallbackAttempts := 0, 0
	for _, attempt := range result.Attempts {
		if attempt.Failure != nil {
			failedAttempts++
		}
		if attempt.Fallback {
			fallbackAttempts++
		}
	}
	if failedAttempts != 2 || fallbackAttempts != 2 {
		t.Fatalf("host attempt accounting failed=%d fallback=%d attempts=%#v", failedAttempts, fallbackAttempts, result.Attempts)
	}
	if logger.countLevelContaining("INFO", "starting image upload fallback") != 2 {
		t.Fatal("expected fallback decisions at info level")
	}
	if logger.countLevelContaining("WARN", "starting image upload fallback") != 0 {
		t.Fatal("recovering fallback decisions must not log at warning level")
	}
}

func TestResolveImageUploadTargetsUsesExactWorkflowTrackerSelection(t *testing.T) {
	t.Parallel()

	registry := mediaImageHostRegistry(t)
	if err := registry.RegisterDescriptor(trackers.Descriptor{
		Name:              "LST",
		Definition:        mediaImageHostDefinition("LST"),
		Family:            trackers.FamilyUnit3D,
		BaseURL:           "https://lst.example.invalid",
		UploadContentMode: trackers.UploadContentModeScreenshots,
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "lostimg",
			OwnedHosts:             []string{"lostimg"},
			EnableWithImageHosting: true,
		},
	}); err != nil {
		t.Fatalf("register LST: %v", err)
	}
	module := &mediaModule{
		cfg: config.Config{ImageHosting: config.ImageHostingConfig{
			Host1:          "imgbb",
			LostimgEnabled: true,
		}},
		logger:   api.NopLogger{},
		registry: registry,
	}

	for _, test := range []struct {
		name    string
		subject api.UploadSubject
	}{
		{
			name: "legacy removal",
			subject: api.UploadSubject{
				TrackersRemove: []string{"LST"},
			},
		},
		{
			name: "prepared client match",
			subject: api.UploadSubject{
				MatchedTrackers: []string{"LST"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			targets, err := module.resolveImageUploadTargets([]string{"ONE", "LST"}, test.subject, "imgbb", nil)
			if err != nil {
				t.Fatalf("resolve image upload targets: %v", err)
			}
			if got := uploadTargetHosts(targets); !slices.Equal(got, []string{"imgbb", "lostimg"}) {
				t.Fatalf("resolved hosts = %v, targets=%#v", got, targets)
			}
			if len(targets) != 2 || targets[1].Host != "lostimg" ||
				targets[1].UsageScope != "tracker:LST" || !slices.Equal(targets[1].Trackers, []string{"LST"}) {
				t.Fatalf("LST target = %#v, targets=%#v", targets[1], targets)
			}
		})
	}
}

type mediaImageHostDefinition string

func (d mediaImageHostDefinition) Name() string { return string(d) }

func (mediaImageHostDefinition) Prepare(context.Context, trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.TrackerPlan{}, nil
}

func mediaImageHostRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry := trackers.NewRegistry()
	for _, item := range []struct {
		name  string
		hosts []string
	}{
		{name: "ONE", hosts: []string{"pixhost", "imgbb"}},
		{name: "TWO", hosts: []string{"onlyimage", "ptscreens"}},
	} {
		err := registry.RegisterDescriptor(trackers.Descriptor{
			Name:              item.name,
			Definition:        mediaImageHostDefinition(item.name),
			Family:            trackers.FamilyStandalone,
			BaseURL:           "https://" + strings.ToLower(item.name) + ".example.invalid",
			UploadContentMode: trackers.UploadContentModeScreenshots,
			ImageHost:         &trackers.ImageHostPolicy{AllowedHosts: item.hosts},
		})
		if err != nil {
			t.Fatalf("register %s: %v", item.name, err)
		}
	}
	return registry
}

func receiveImageHostCall(t *testing.T, calls <-chan imageHostCall) imageHostCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for image-host call")
		return imageHostCall{}
	}
}

func receiveImageUploadCallResult(t *testing.T, results <-chan imageUploadCallResult) imageUploadCallResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for image upload result")
		return imageUploadCallResult{}
	}
}

func sortedCallHosts(calls []imageHostCall) []string {
	hosts := make([]string, 0, len(calls))
	for _, call := range calls {
		hosts = append(hosts, call.host)
	}
	slices.Sort(hosts)
	return hosts
}
