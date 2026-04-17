// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type stubFilesystem struct {
	paths []string
	err   error
}

func (s stubFilesystem) ValidatePaths(ctx context.Context, paths []string) ([]string, error) {
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
