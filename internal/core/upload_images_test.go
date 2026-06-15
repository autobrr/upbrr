// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubImageHosting struct {
	candidates []api.ScreenshotImage
	uploaded   []api.UploadedImageLink
	err        error
	lastMeta   api.PreparedMetadata
	mu         sync.Mutex
	calls      []stubImageUploadCall
	uploadFn   func(ctx context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error)
}

type stubImageUploadCall struct {
	host       string
	usageScope string
	images     []api.ScreenshotImage
}

func (s *stubImageHosting) ListCandidates(_ context.Context, meta api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastMeta = meta
	return s.candidates, nil
}

func (s *stubImageHosting) Upload(ctx context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.mu.Lock()
	s.lastMeta = meta
	s.calls = append(s.calls, stubImageUploadCall{
		host:       host,
		usageScope: usageScope,
		images:     append([]api.ScreenshotImage{}, images...),
	})
	s.mu.Unlock()
	if s.uploadFn != nil {
		return s.uploadFn(ctx, meta, host, usageScope, images)
	}
	return s.uploaded, nil
}

func TestUploadImagesWithoutCache(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	uploaded := []api.UploadedImageLink{{ImagePath: "/tmp/img1.png", Host: "imgbox"}}

	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     &stubImageHosting{uploaded: uploaded},
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
	}, "imgbox", images)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result.Links) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Links))
	}
	if result.Links[0].Host != "imgbox" {
		t.Fatalf("expected host imgbox, got %s", result.Links[0].Host)
	}
}

func TestListUploadCandidatesWithoutCache(t *testing.T) {
	t.Parallel()

	candidates := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}

	core := &Core{
		logger: api.NopLogger{},
		cfg:    config.Config{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     &stubImageHosting{candidates: candidates},
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.ListUploadCandidates(context.Background(), api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result))
	}
	if result[0].Path != "/tmp/img1.png" {
		t.Fatalf("expected path /tmp/img1.png, got %s", result[0].Path)
	}
}

func TestUploadImagesGUIFallbackReappliesReleaseOverrides(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{uploaded: []api.UploadedImageLink{{ImagePath: "/tmp/img1.png", Host: "imgbox"}}}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Metadata:   &stubMeta{},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache("/tmp/source", "", api.PreparedMetadata{
		SourcePath: "/tmp/source",
		Release: api.ReleaseInfo{
			Title: "Example",
		},
	})

	edition := "Director's Cut"
	_, err := core.UploadImages(context.Background(), api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			Edition: &edition,
		},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if imageService.lastMeta.ReleaseNameOverrides.Edition == nil || *imageService.lastMeta.ReleaseNameOverrides.Edition != edition {
		t.Fatalf("expected upload images to receive edition override, got %#v", imageService.lastMeta.ReleaseNameOverrides)
	}
}

func TestUploadImagesUploadsApplicableTrackerHosts(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"PTP": {ImageHost: "pixhost"},
					"STC": {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"PTP", "STC"},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(imageService.calls) != 2 {
		t.Fatalf("expected uploads to two hosts, got %d calls: %#v", len(imageService.calls), imageService.calls)
	}
	calledHosts := map[string]string{}
	for _, call := range imageService.calls {
		calledHosts[call.host] = call.usageScope
	}
	if calledHosts["imgbox"] != "global" {
		t.Fatalf("expected selected host imgbox, got %#v", imageService.calls)
	}
	if calledHosts["pixhost"] != "global" {
		t.Fatalf("expected configured PTP host pixhost, got %#v", imageService.calls)
	}
	if len(result.Links) != 2 {
		t.Fatalf("expected result from both hosts, got %d", len(result.Links))
	}
}

func TestUploadImagesUsesConfiguredHostPriorityWhenSelectedHostIsNotApproved(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{
				Host1: "imgbb",
				Host2: "pixhost",
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"PTP": {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"PTP"},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(imageService.calls) != 1 {
		t.Fatalf("expected upload to required tracker host only, got %d calls: %#v", len(imageService.calls), imageService.calls)
	}
	if imageService.calls[0].host != "pixhost" {
		t.Fatalf("expected configured PTP-approved host pixhost, got %#v", imageService.calls[0])
	}
	if len(result.Links) != 1 || result.Links[0].Host != "pixhost" {
		t.Fatalf("expected pixhost result only, got %#v", result)
	}
}

func TestUploadImagesUsesSelectedHostWhenApprovedForTracker(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"MTV"},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(imageService.calls) != 1 {
		t.Fatalf("expected upload to selected approved host only, got %d calls: %#v", len(imageService.calls), imageService.calls)
	}
	if imageService.calls[0].host != "imgbox" {
		t.Fatalf("expected selected host imgbox, got %#v", imageService.calls[0])
	}
	if len(result.Links) != 1 || result.Links[0].Host != "imgbox" {
		t.Fatalf("expected imgbox result only, got %#v", result)
	}
}

func TestUploadImagesFiltersCachedBlockedTrackers(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbb"},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {ImageHost: "hdb"},
					"NBL": {},
					"OE":  {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache("/tmp/source", "", api.PreparedMetadata{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"NBL": {{Rule: "require_tv_only", Reason: "category movie is not tv"}},
		},
	})

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"HDB", "NBL", "OE"},
	}, "imgbb", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(imageService.calls) != 1 {
		t.Fatalf("expected upload only for unblocked tracker, got %d calls: %#v", len(imageService.calls), imageService.calls)
	}
	if imageService.calls[0].host != "imgbb" {
		t.Fatalf("expected only selected global host for OE, got %#v", imageService.calls[0])
	}
	if len(result.Links) != 1 || result.Links[0].Host != "imgbb" {
		t.Fatalf("expected imgbb result only, got %#v", result)
	}
}

func TestUploadImagesFiltersCachedMatchedTrackers(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbb"},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {ImageHost: "hdb"},
					"OE":  {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache("/tmp/source", "", api.PreparedMetadata{
		SourcePath:      "/tmp/source",
		MatchedTrackers: []string{"hdb"},
	})

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"HDB", "OE"},
	}, "imgbb", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(imageService.calls) != 1 {
		t.Fatalf("expected upload only for unmatched tracker, got %d calls: %#v", len(imageService.calls), imageService.calls)
	}
	if imageService.calls[0].host != "imgbb" {
		t.Fatalf("expected only selected global host for OE, got %#v", imageService.calls[0])
	}
	if len(result.Links) != 1 || result.Links[0].Host != "imgbb" {
		t.Fatalf("expected imgbb result only, got %#v", result)
	}
}

func TestUploadImagesUploadsHostsConcurrently(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	bothStarted := make(chan struct{})
	var startedMu sync.Mutex
	started := 0
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			startedMu.Lock()
			started++
			if started == 2 {
				close(bothStarted)
			}
			startedMu.Unlock()

			select {
			case <-bothStarted:
			case <-time.After(500 * time.Millisecond):
				return nil, errors.New("second host did not start")
			}
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"PTP": {ImageHost: "pixhost"},
					"MTV": {ImageHost: "imgbox"},
					"STC": {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"PTP", "MTV", "STC"},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result.Links) != 2 {
		t.Fatalf("expected both concurrent uploads to succeed, got %#v", result)
	}
}

func TestUploadImagesReturnsHostFailuresWithSuccessfulLinks(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			if host == "pixhost" {
				return nil, fmt.Errorf("%s unavailable", host)
			}
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{
				Host1: "imgbox",
				Host2: "pixhost",
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"PTP": {ImageHost: "pixhost"},
					"MTV": {ImageHost: "imgbox"},
					"STC": {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"PTP", "MTV", "STC"},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected partial host failure to return successful links, got %v", err)
	}
	if len(result.Links) != 1 || result.Links[0].Host != "imgbox" {
		t.Fatalf("expected only successful imgbox upload, got %#v", result)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected one host failure, got %#v", result.Failures)
	}
	failure := result.Failures[0]
	if failure.Host != "pixhost" || failure.Message != "pixhost unavailable" {
		t.Fatalf("expected exhausted pixhost fallback failure, got %#v", failure)
	}
	expectedTrackers := []string{"PTP"}
	sort.Strings(failure.Trackers)
	sort.Strings(expectedTrackers)
	if !slices.Equal(failure.Trackers, expectedTrackers) {
		t.Fatalf("expected failure to block only linked trackers, got %#v", failure.Trackers)
	}
}

func TestUploadImagesFallsBackWhenSelectedHostFails(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	var calls []string
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			calls = append(calls, host)
			if host == "imgbb" {
				return nil, errors.New("imgbb unavailable")
			}
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{
				Host1: "imgbb",
				Host2: "imgbox",
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {},
					"STC": {},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"MTV", "STC"},
	}, "imgbb", images)
	if err != nil {
		t.Fatalf("expected fallback upload to succeed, got %v", err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("expected selected host failure to be recovered by fallback, got %#v", result.Failures)
	}
	if len(result.Links) != 1 || result.Links[0].Host != "imgbox" {
		t.Fatalf("expected fallback imgbox link, got %#v", result)
	}
	if !slices.Equal(calls, []string{"imgbb", "imgbox"}) {
		t.Fatalf("expected selected host then fallback host, got %v", calls)
	}
}

func TestUploadImagesFallsBackFromTrackerConfiguredHostForUnrestrictedTracker(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	var calls []string
	imageService := &stubImageHosting{
		uploadFn: func(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			calls = append(calls, host)
			if host == "pixhost" {
				return nil, errors.New("pixhost unavailable")
			}
			return uploadedImageLinksForHost(meta, host, usageScope, images), nil
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		cfg: config.Config{
			ImageHosting: config.ImageHostingConfig{
				Host1: "pixhost",
				Host2: "imgbb",
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HHD": {ImageHost: "pixhost"},
				},
			},
		},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"HHD"},
	}, "pixhost", images)
	if err != nil {
		t.Fatalf("expected fallback upload to succeed, got %v", err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("expected configured host failure to be recovered by fallback, got %#v", result.Failures)
	}
	if len(result.Links) != 1 || result.Links[0].Host != "imgbb" {
		t.Fatalf("expected fallback imgbb link, got %#v", result)
	}
	if !slices.Equal(calls, []string{"pixhost", "imgbb"}) {
		t.Fatalf("expected configured host then fallback host, got %v", calls)
	}
}

func uploadedImageLinksForHost(meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) []api.UploadedImageLink {
	result := make([]api.UploadedImageLink, 0, len(images))
	for _, image := range images {
		result = append(result, api.UploadedImageLink{
			SourcePath: meta.SourcePath,
			ImagePath:  image.Path,
			Host:       host,
			UsageScope: usageScope,
			RawURL:     "https://" + host + "/raw.png",
			ImgURL:     "https://" + host + "/img.png",
			WebURL:     "https://" + host + "/web.png",
		})
	}
	return result
}
