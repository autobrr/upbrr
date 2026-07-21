// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

func TestUploadImagesToTargetsOverlapsHostsAndPreservesTargetOrder(t *testing.T) {
	t.Parallel()

	alphaRelease := make(chan struct{})
	betaRelease := make(chan struct{})
	service := &barrierImageHostingService{
		entered: make(chan imageHostCall, 2),
		behaviors: map[string]imageHostBehavior{
			"alpha": {release: alphaRelease, err: errors.New("alpha failed")},
			"beta":  {release: betaRelease, err: errors.New("beta failed")},
		},
	}
	logger := &eligibilityCaptureLogger{}
	module := &mediaModule{images: service, logger: logger}
	targets := []trackers.ImageUploadTarget{
		{
Host: "alpha",
 UsageScope: "global",
 Trackers: []string{"ONE"},
},
		{
Host: "beta",
 UsageScope: "global",
 Trackers: []string{"TWO"},
},
	}
	done := make(chan api.UploadImagesResult, 1)
	go func() {
		done <- module.uploadImagesToTargets(
			context.Background(),
			api.UploadSubject{SourcePath: "C:\\media\\Example.Release.2026.mkv"},
			targets,
			[]api.ScreenshotImage{{Path: "screen.png"}},
			false,
		)
	}()

	first := receiveImageHostCall(t, service.entered)
	second := receiveImageHostCall(t, service.entered)
	if first.host == second.host || first.fallback || second.fallback {
		t.Fatalf("primary host entries = %#v, %#v", first, second)
	}
	close(betaRelease)
	close(alphaRelease)
	result := receiveImageUploadResult(t, done)
	if got := uploadLinkHosts(result.Links); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("link order = %v", got)
	}
	if got := uploadFailureHosts(result.Failures); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("failure order = %v", got)
	}
	if info := strings.Join(logger.level("info"), "\n"); !strings.Contains(info, "hosts=2 host_names=alpha,beta fallback=false images=1") {
		t.Fatalf("round-start log missing: %q", info)
	}
}

func TestUploadImagesFallbackWaitsForPrimaryRoundAndOverlapsFallbackHosts(t *testing.T) {
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
	module := &mediaModule{
		cfg: config.Config{ImageHosting: config.ImageHostingConfig{
			Host1: "pixhost",
			Host2: "onlyimage",
			Host3: "imgbb",
			Host4: "ptscreens",
		}},
		images:   service,
		logger:   api.NopLogger{},
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
			[]trackers.ImageUploadTarget{
				{
Host: "pixhost",
 UsageScope: "global",
 Trackers: []string{"ONE"},
},
				{
Host: "onlyimage",
 UsageScope: "global",
 Trackers: []string{"TWO"},
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
	select {
	case call := <-service.entered:
		t.Fatalf("fallback started before primary round completed: %#v", call)
	default:
	}
	close(releases["onlyimage"])

	fallback := []imageHostCall{
		receiveImageHostCall(t, service.entered),
		receiveImageHostCall(t, service.entered),
	}
	if got := sortedCallHosts(fallback); !slices.Equal(got, []string{"imgbb", "ptscreens"}) {
		t.Fatalf("fallback hosts = %v", got)
	}
	if !fallback[0].fallback || !fallback[1].fallback {
		t.Fatalf("fallback flags = %#v", fallback)
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
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for image-host call")
		return imageHostCall{}
	}
}

func receiveImageUploadResult(t *testing.T, results <-chan api.UploadImagesResult) api.UploadImagesResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for image upload result")
		return api.UploadImagesResult{}
	}
}

func receiveImageUploadCallResult(t *testing.T, results <-chan imageUploadCallResult) imageUploadCallResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for image upload result")
		return imageUploadCallResult{}
	}
}

func uploadLinkHosts(links []api.UploadedImageLink) []string {
	hosts := make([]string, 0, len(links))
	for _, link := range links {
		hosts = append(hosts, link.Host)
	}
	return hosts
}

func uploadFailureHosts(failures []api.UploadImageHostFailure) []string {
	hosts := make([]string, 0, len(failures))
	for _, failure := range failures {
		hosts = append(hosts, failure.Host)
	}
	return hosts
}

func sortedCallHosts(calls []imageHostCall) []string {
	hosts := make([]string, 0, len(calls))
	for _, call := range calls {
		hosts = append(hosts, call.host)
	}
	slices.Sort(hosts)
	return hosts
}
