// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubFilesystem struct {
	paths []string
	err   error
}

func (s stubFilesystem) ValidatePaths(_ context.Context, paths []string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.paths != nil {
		return append([]string{}, s.paths...), nil
	}
	return append([]string{}, paths...), nil
}

type stubPreparationTrackers struct {
	called   bool
	trackers []string
	meta     api.PreparedMetadata
}

func (s *stubPreparationTrackers) Upload(context.Context, api.PreparedMetadata) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s *stubPreparationTrackers) BuildPreparation(_ context.Context, meta api.PreparedMetadata, trackers []string) (api.PreparationPreview, error) {
	s.called = true
	s.trackers = append([]string{}, trackers...)
	s.meta = meta
	return api.PreparationPreview{
		SourcePath: meta.SourcePath,
		Descriptions: []api.PreparationDescription{
			{Trackers: trackers, RawDescription: meta.DescriptionTemplate, RawDescriptionHTML: "<p>ok</p>"},
		},
	}, nil
}

func (s *stubPreparationTrackers) BuildUploadDryRun(context.Context, api.PreparedMetadata, []string) ([]api.TrackerDryRunEntry, error) {
	return []api.TrackerDryRunEntry{}, nil
}

func TestFetchPreparationPreviewFromCache(t *testing.T) {
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", DescriptionTemplate: "Example"}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{meta.SourcePath}},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache(meta.SourcePath, "", meta)

	preview, err := core.FetchPreparationPreview(context.Background(), api.Request{Paths: []string{meta.SourcePath}, Mode: api.ModeGUI})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !trackerSvc.called {
		t.Fatalf("expected tracker preparation to be called")
	}
	if preview.SourcePath != meta.SourcePath {
		t.Fatalf("expected source path %q, got %q", meta.SourcePath, preview.SourcePath)
	}
}

func TestFetchPreparationPreviewReturnsEmptyWhenCachedSelectedTrackersResolveEmpty(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SourcePath:     "/tmp/source",
		TrackersRemove: []string{"AITHER"},
	}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{meta.SourcePath}},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache(meta.SourcePath, "", meta)

	preview, err := core.FetchPreparationPreview(context.Background(), api.Request{
		Paths:    []string{meta.SourcePath},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if trackerSvc.called {
		t.Fatal("expected tracker preparation to be skipped when selected trackers resolve empty")
	}
	if preview.SourcePath != meta.SourcePath {
		t.Fatalf("expected source path %q, got %q", meta.SourcePath, preview.SourcePath)
	}
}

func TestFetchPreparationPreviewUsesIgnoredMatchedTrackerFromCache(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SourcePath:      "/tmp/source",
		TrackersRemove:  []string{"AITHER", "BLU"},
		MatchedTrackers: []string{"AITHER", "BLU"},
	}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		cfg: config.Config{
			Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
		},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{meta.SourcePath}},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache(meta.SourcePath, "", meta)

	preview, err := core.FetchPreparationPreview(context.Background(), api.Request{
		Paths:          []string{meta.SourcePath},
		Mode:           api.ModeGUI,
		Trackers:       []string{"AITHER"},
		IgnoreDupesFor: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if preview.SourcePath != meta.SourcePath {
		t.Fatalf("expected source path %q, got %q", meta.SourcePath, preview.SourcePath)
	}
	if !slices.Equal(trackerSvc.trackers, []string{"AITHER"}) {
		t.Fatalf("expected ignored matched tracker without default fallback, got %v", trackerSvc.trackers)
	}
	if slices.Contains(trackerSvc.meta.TrackersRemove, "AITHER") || !slices.Contains(trackerSvc.meta.TrackersRemove, "BLU") {
		t.Fatalf("expected only unignored duplicate removal to remain, got %v", trackerSvc.meta.TrackersRemove)
	}
	if slices.Contains(trackerSvc.meta.MatchedTrackers, "AITHER") || !slices.Contains(trackerSvc.meta.MatchedTrackers, "BLU") {
		t.Fatalf("expected only unignored matched tracker to remain, got %v", trackerSvc.meta.MatchedTrackers)
	}
}

func TestFetchPreparationPreviewAddsDefaultsForExplicitTrackers(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		cfg: config.Config{
			Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
		},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{meta.SourcePath}},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache(meta.SourcePath, "", meta)

	_, err := core.FetchPreparationPreview(context.Background(), api.Request{
		Paths:    []string{meta.SourcePath},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !slices.Equal(trackerSvc.trackers, []string{"BLU", "AITHER"}) {
		t.Fatalf("expected default plus explicit trackers, got %v", trackerSvc.trackers)
	}
}

func TestFetchPreparationPreviewDoesNotFallbackToUnsignedCacheWithExternalOverrides(t *testing.T) {
	tmdbID := 321
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", DescriptionTemplate: "Example"}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{meta.SourcePath}},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache(meta.SourcePath, "", meta)

	_, err := core.FetchPreparationPreview(context.Background(), api.Request{
		Paths: []string{meta.SourcePath},
		Mode:  api.ModeGUI,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err == nil {
		t.Fatalf("expected cache miss error when external overrides are present")
	}
	if trackerSvc.called {
		t.Fatalf("expected tracker preparation not to run on unsigned cache fallback")
	}
}

func TestFetchPreparationPreviewUsesBlockedTrackersFromCache(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{meta.SourcePath}},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache(meta.SourcePath, "", meta)

	_, err := core.FetchPreparationPreview(context.Background(), api.Request{Paths: []string{meta.SourcePath}, Mode: api.ModeGUI})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := trackerSvc.meta.BlockedTrackers["HDB"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected blocked tracker metadata to be forwarded, got %#v", trackerSvc.meta.BlockedTrackers)
	}
}

func TestFetchPreparationPreviewCachesMergedExternalIDSelections(t *testing.T) {
	t.Parallel()

	trackerSvc := &stubPreparationTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{TMDBAPI: "x"},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				SourcePath: "/tmp/source",
				Release: api.ReleaseInfo{
					Title: "Example",
				},
			}},
			Trackers: trackerSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	tmdbID := 321
	req := api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
		ExternalIDSelections: map[string]api.ExternalIDSelection{
			"/tmp/source": {
				TMDBID: &tmdbID,
			},
		},
	}

	if _, err := core.FetchPreparationPreview(context.Background(), req); err != nil {
		t.Fatalf("fetch preparation preview: %v", err)
	}

	exported, ok, err := core.ExportGUICachedPreparedMeta(context.Background(), req)
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if !ok {
		t.Fatal("expected preparation preview to cache merged external ID selection")
	}
	if exported.ExternalIDOverrides.TMDBID == nil || *exported.ExternalIDOverrides.TMDBID != tmdbID {
		t.Fatalf("expected exported cached metadata to preserve merged TMDB selection, got %#v", exported.ExternalIDOverrides)
	}
}

func TestFetchPreparationPreviewReturnsEmptyWhenPreparedSelectedTrackersResolveEmpty(t *testing.T) {
	t.Parallel()

	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		cfg: config.Config{
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
			Trackers:           config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
		},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				SourcePath:     "/tmp/source",
				TrackersRemove: []string{"AITHER"},
			}},
			Trackers: trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchPreparationPreview(context.Background(), api.Request{
		Paths:    []string{"/tmp/source"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if trackerSvc.called {
		t.Fatal("expected tracker preparation to be skipped when prepared selected trackers resolve empty")
	}
	if preview.SourcePath != "/tmp/source" {
		t.Fatalf("expected source path to be preserved, got %q", preview.SourcePath)
	}
	if _, ok := core.getDupeCache("/tmp/source", ""); ok {
		t.Fatal("expected explicit-empty preparation not to seed GUI cache")
	}
}
