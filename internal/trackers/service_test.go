// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/paths"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	descriptionhdb "github.com/autobrr/upbrr/internal/services/description/hdb"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadNoTrackers(t *testing.T) {
	t.Parallel()

	svc := NewService(config.Config{}, nil, nil)
	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected 0 uploads, got %d", summary.Uploaded)
	}
}

func TestUploadConfiguredTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"blU", "bhd"}}}
	svc := NewService(cfg, nil, nil)
	_, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
	if !errors.Is(err, internalerrors.ErrNotImplemented) {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestUploadOverrideTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}}}
	svc := NewService(cfg, nil, nil)
	_, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file", Trackers: []string{"aither"}})
	if !errors.Is(err, internalerrors.ErrNotImplemented) {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestUploadRemovesTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD"}}}
	svc := NewService(cfg, nil, nil)
	_, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file", TrackersRemove: []string{"bhd"}})
	if !errors.Is(err, internalerrors.ErrNotImplemented) {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestUploadUnknownTrackers(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"NOPE"}}}
	svc := NewService(cfg, nil, nil)
	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 0 {
		t.Fatalf("expected 0 uploads, got %d", summary.Uploaded)
	}
}

func TestUploadBannedGroup(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"TOS"}}}
	svc := NewService(cfg, nil, nil)
	_, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file", Tag: "-FL3ER"})
	if !errors.Is(err, internalerrors.ErrBannedGroup) {
		t.Fatalf("expected banned group error, got %v", err)
	}
}

func TestNormalizeTrackersDedup(t *testing.T) {
	t.Parallel()

	values := normalizeTrackers([]string{"blu", "BLU", "bhd", ""})
	if len(values) != 2 {
		t.Fatalf("expected 2 trackers, got %d", len(values))
	}
	if values[0] != "BLU" || values[1] != "BHD" {
		t.Fatalf("unexpected tracker list: %v", values)
	}
}

type stubDryRunDefinition struct {
	name string
}

type stubUploadArtifactDefinition struct {
	name string
}

type stubPreparationDefinition struct {
	name        string
	group       string
	description string
}

type trackingUploadDefinition struct {
	name    string
	started chan<- string
}

type blockingUploadDefinition struct {
	name      string
	started   chan<- string
	release   <-chan struct{}
	uploaded  int
	uploadErr error
}

type testHDBPreparationDefinition struct{}

func (b blockingUploadDefinition) Name() string {
	return b.name
}

func (b blockingUploadDefinition) Upload(context.Context, UploadRequest) (api.UploadSummary, error) {
	if b.started != nil {
		b.started <- b.name
	}
	if b.release != nil {
		<-b.release
	}
	if b.uploadErr != nil {
		return api.UploadSummary{}, b.uploadErr
	}
	return api.UploadSummary{Uploaded: b.uploaded}, nil
}

func (s stubUploadArtifactDefinition) Name() string {
	return s.name
}

func (s stubUploadArtifactDefinition) Upload(context.Context, UploadRequest) (api.UploadSummary, error) {
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{
			{
				Tracker:     s.name,
				TorrentID:   "374352",
				DownloadURL: "https://aither.cc/torrent/download/374352.382",
				TorrentURL:  "https://aither.cc/torrents/374352",
			},
		},
	}, nil
}

func (s stubPreparationDefinition) Name() string {
	return s.name
}

func (s stubPreparationDefinition) Upload(context.Context, UploadRequest) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s stubPreparationDefinition) BuildDescription(context.Context, DescriptionRequest) (DescriptionResult, error) {
	return DescriptionResult{Group: s.group, Description: s.description}, nil
}

func (testHDBPreparationDefinition) Name() string {
	return "HDB"
}

func (testHDBPreparationDefinition) Upload(context.Context, UploadRequest) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

func (testHDBPreparationDefinition) BuildDescription(ctx context.Context, req DescriptionRequest) (DescriptionResult, error) {
	assets := DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	}
	description, err := descriptionhdb.BuildDescription(ctx, req.Meta, req.AppConfig, assets.Description, assets.Screenshots)
	if err != nil {
		return DescriptionResult{}, fmt.Errorf("trackers: %w", err)
	}
	return DescriptionResult{Group: "hdb", Description: description}, nil
}

func (t trackingUploadDefinition) Name() string {
	return t.name
}

func (t trackingUploadDefinition) Upload(context.Context, UploadRequest) (api.UploadSummary, error) {
	if t.started != nil {
		t.started <- t.name
	}
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s stubDryRunDefinition) Name() string {
	return s.name
}

func (s stubDryRunDefinition) Upload(context.Context, UploadRequest) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s stubDryRunDefinition) BuildUploadDryRun(context.Context, UploadRequest) (api.TrackerDryRunEntry, error) {
	return api.TrackerDryRunEntry{
		Tracker: s.name,
		Status:  "ready",
		Payload: map[string]string{"category": "movie"},
	}, nil
}

func TestBuildUploadDryRunUsesBuilder(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "BLU"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Tracker != "BLU" {
		t.Fatalf("expected BLU tracker, got %q", entries[0].Tracker)
	}
	if entries[0].Payload["category"] != "movie" {
		t.Fatalf("expected payload category movie, got %q", entries[0].Payload["category"])
	}
}

func TestBuildUploadDryRunBlocksWhenImageHostFallbacksFail(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "PTP"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
	}
	images := &stubImageService{
		errs: map[string]error{
			"ptpimg":  errors.New("ptpimg unavailable"),
			"pixhost": errors.New("pixhost unavailable"),
		},
	}
	svc := NewServiceWithRegistryAndImages(config.Config{}, nil, repo, registry, images)

	entries, err := svc.BuildUploadDryRun(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"}, []string{"PTP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked dry run, got %#v", entry)
	}
	if entry.ImageHost.Status != "warning" || len(entry.ImageHost.Warnings) != 2 {
		t.Fatalf("expected image host warnings to be attached, got %#v", entry.ImageHost)
	}
}

func TestBuildUploadDryRunIncludesRuleFailedTrackersWhenOverrideEnabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDryRunDefinition{name: "BLU"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.PreparedMetadata{
		SourcePath:                "/tmp/file",
		IgnoreTrackerRuleFailures: true,
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"BLU": {{Rule: "require_movie_only", Reason: "movie only"}},
		},
	}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Tracker != "BLU" {
		t.Fatalf("expected BLU tracker, got %q", entries[0].Tracker)
	}
}

func TestBuildUploadDryRunNoBuilderMarkedUnsupported(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "BLU"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"}, []string{"BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Status != "not_supported" {
		t.Fatalf("expected not_supported status, got %q", entries[0].Status)
	}
}

func TestBuildPreparationGroupsExactMatchingUnit3DDescriptions(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "AITHER", group: "unit3d", description: "same description"},
		stubPreparationDefinition{name: "HHD", group: "unit3d", description: "same description"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{}, []string{"AITHER", "HHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected exact matching unit3d descriptions to group, got %d groups", len(preview.Descriptions))
	}
	group := preview.Descriptions[0]
	if group.GroupKey != "unit3d" {
		t.Fatalf("expected canonical unit3d group key, got %q", group.GroupKey)
	}
	if got := strings.Join(group.Trackers, ","); got != "AITHER,HHD" {
		t.Fatalf("expected both trackers in group, got %q", got)
	}
}

func TestBuildPreparationSplitsSameGroupWhenDescriptionDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "AITHER", group: "unit3d", description: "aither description"},
		stubPreparationDefinition{name: "HHD", group: "unit3d", description: "hhd description"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{}, []string{"AITHER", "HHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected mismatched descriptions to split, got %d groups", len(preview.Descriptions))
	}
	if preview.Descriptions[0].GroupKey == preview.Descriptions[1].GroupKey {
		t.Fatalf("expected unique group keys, got %q", preview.Descriptions[0].GroupKey)
	}
	if got := strings.Join(preview.Descriptions[0].Trackers, ","); got != "AITHER" {
		t.Fatalf("expected first group to contain AITHER, got %q", got)
	}
	if got := strings.Join(preview.Descriptions[1].Trackers, ","); got != "HHD" {
		t.Fatalf("expected second group to contain HHD, got %q", got)
	}
}

func TestBuildPreparationSplitsSameGroupWhenRawDescriptionDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "AITHER", group: "unit3d", description: "same final description"},
		stubPreparationDefinition{name: "BLU", group: "unit3d", description: "same final description"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: "aither raw description"},
			{Tracker: "BLU", Description: "blu raw description"},
		},
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{SourcePath: sourcePath}, []string{"AITHER", "BLU"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected raw description mismatch to split, got %d groups", len(preview.Descriptions))
	}
	if preview.Descriptions[0].RawDescription != "aither raw description" {
		t.Fatalf("expected AITHER raw description, got %q", preview.Descriptions[0].RawDescription)
	}
	if preview.Descriptions[1].RawDescription != "blu raw description" {
		t.Fatalf("expected BLU raw description, got %q", preview.Descriptions[1].RawDescription)
	}
}

func TestBuildPreparationSplitsSameGroupWhenImageHostFeedbackDiffers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "AITHER", group: "shared", description: "same description"},
		stubPreparationDefinition{name: "PTP", group: "shared", description: "same description"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{}, []string{"AITHER", "PTP"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected image-host feedback mismatch to split, got %d groups", len(preview.Descriptions))
	}
	if len(preview.Descriptions[0].ImageHost.AllowedHosts) != 0 {
		t.Fatalf("expected AITHER image host policy to be empty, got %#v", preview.Descriptions[0].ImageHost)
	}
	if len(preview.Descriptions[1].ImageHost.AllowedHosts) == 0 {
		t.Fatalf("expected PTP image host policy to be captured")
	}
}

func TestApplyTrackerConfigOverrides(t *testing.T) {
	t.Parallel()

	channel := "spd"
	trueValue := true

	cfg := applyTrackerConfigOverrides(config.TrackerConfig{}, api.TrackerConfigOverrides{
		Anon:    &trueValue,
		Draft:   &trueValue,
		ModQ:    &trueValue,
		Channel: &channel,
	})

	if !cfg.Anon {
		t.Fatalf("expected anon override")
	}
	if !cfg.Draft {
		t.Fatalf("expected draft override")
	}
	if !cfg.ModQ {
		t.Fatalf("expected modq override")
	}
	if cfg.Channel != "spd" {
		t.Fatalf("expected channel override, got %q", cfg.Channel)
	}
}

func TestUploadAggregatesUploadedTorrentArtifacts(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubUploadArtifactDefinition{name: "AITHER"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)
	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 upload, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected 1 uploaded torrent artifact, got %d", len(summary.UploadedTorrents))
	}
	if summary.UploadedTorrents[0].DownloadURL != "https://aither.cc/torrent/download/374352.382" {
		t.Fatalf("unexpected download URL: %q", summary.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadPreflightsMultipleConfiguredImageHostsOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubUploadArtifactDefinition{name: "PTP"},
		stubUploadArtifactDefinition{name: "STC"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
	}
	images := &stubImageService{repo: repo}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			DefaultTrackers: config.CSVList{"PTP", "STC"},
			Trackers: map[string]config.TrackerConfig{
				"PTP": {ImageHost: "ptpimg"},
				"STC": {ImageHost: "imgbox"},
			},
		},
	}
	svc := NewServiceWithRegistryAndImages(cfg, nil, repo, registry, images)

	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 tracker uploads, got %d", summary.Uploaded)
	}
	firstRunCalls := append([]string{}, images.calls...)
	sort.Strings(firstRunCalls)
	if got := strings.Join(firstRunCalls, ","); got != "imgbox,ptpimg" {
		t.Fatalf("expected one upload per configured host, got %q", got)
	}

	summary, err = svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("unexpected second upload error: %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 tracker uploads on second run, got %d", summary.Uploaded)
	}
	secondRunCalls := append([]string{}, images.calls...)
	sort.Strings(secondRunCalls)
	if got := strings.Join(secondRunCalls, ","); got != "imgbox,ptpimg" {
		t.Fatalf("expected existing host variants to be reused on second run, got calls %q", got)
	}
}

func TestUploadIncludesRuleFailedTrackersWhenOverrideEnabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubUploadArtifactDefinition{name: "AITHER"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)
	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{
		SourcePath:                "/tmp/file",
		IgnoreTrackerRuleFailures: true,
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {{Rule: "require_movie_only", Reason: "movie only"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 upload, got %d", summary.Uploaded)
	}
}

func TestUploadRunsTrackersConcurrentlyWithLimit(t *testing.T) {
	t.Parallel()

	started := make(chan string, 3)
	release := make(chan struct{})

	registry := NewRegistry()
	definitions := []Definition{
		blockingUploadDefinition{name: "BLU", started: started, release: release, uploaded: 1},
		blockingUploadDefinition{name: "BHD", started: started, release: release, uploaded: 1},
		blockingUploadDefinition{name: "AITHER", started: started, release: release, uploaded: 1},
	}
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 2},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD", "AITHER"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	type uploadResult struct {
		summary api.UploadSummary
		err     error
	}
	resultCh := make(chan uploadResult, 1)
	go func() {
		summary, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
		resultCh <- uploadResult{summary: summary, err: err}
	}()

	seen := map[string]struct{}{}
	for range 2 {
		select {
		case tracker := <-started:
			seen[tracker] = struct{}{}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for initial tracker starts")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 trackers to start first, got %v", seen)
	}

	select {
	case tracker := <-started:
		t.Fatalf("expected third tracker to wait for worker slot, started early: %s", tracker)
	case <-time.After(120 * time.Millisecond):
	}

	close(release)

	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for third tracker to start")
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("unexpected error: %v", result.err)
		}
		if result.summary.Uploaded != 3 {
			t.Fatalf("expected 3 uploads, got %d", result.summary.Uploaded)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for upload result")
	}
}

func TestUploadBestEffortWithFailures(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploaded: 1}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BHD", uploadErr: errors.New("tracker boom")}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "AITHER", uploaded: 1}); err != nil {
		t.Fatalf("register AITHER: %v", err)
	}

	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 3},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD", "AITHER"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
	if err != nil {
		t.Fatalf("expected nil error for best-effort upload, got %v", err)
	}
	if summary.Uploaded != 2 {
		t.Fatalf("expected 2 successful uploads, got %d", summary.Uploaded)
	}
}

func TestUploadAllFailuresReturnsError(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	errBoom := errors.New("tracker boom")
	if err := registry.Register(blockingUploadDefinition{name: "BLU", uploadErr: errBoom}); err != nil {
		t.Fatalf("register BLU: %v", err)
	}
	if err := registry.Register(blockingUploadDefinition{name: "BHD", uploadErr: errBoom}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}

	cfg := config.Config{
		PostUpload: config.PostUploadConfig{MaxConcurrentTrackers: 2},
		Trackers:   config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU", "BHD"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, registry)

	_, err := svc.Upload(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/file"})
	if err == nil {
		t.Fatalf("expected error when all trackers fail")
	}
}

func TestBuildPreparationSeparatesScopedImageHostGroups(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "HDB", group: "shared", description: "same description"},
		stubPreparationDefinition{name: "AITHER", group: "shared", description: "same description"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://hdb/a.png", RawURL: "https://hdb/a.png", WebURL: "https://hdb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://hdb/b.png", RawURL: "https://hdb/b.png", WebURL: "https://hdb/b"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", UsageScope: "global", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", UsageScope: "global", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
		},
	}
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"HDB": {ImgRehost: true},
			},
		},
	}

	svc := NewServiceWithRegistry(cfg, nil, repo, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"}, []string{"HDB", "AITHER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 2 {
		t.Fatalf("expected 2 grouped descriptions, got %d", len(preview.Descriptions))
	}
}

func TestBuildPreparationRehostsHDBScreenshotsForURLOnlySlots(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(testHDBPreparationDefinition{}); err != nil {
		t.Fatalf("register HDB definition: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"HDB": {ImgRehost: true},
			},
		},
	}
	meta := api.PreparedMetadata{
		SourcePath: `D:\TV\The.Pitt.S02E15.1080p.WEB-DL.mkv`,
		Options:    api.UploadOptions{KeepImages: true},
		TrackerData: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://ptpimg.me/4m092k.png", "https://ptpimg.me/7oj122.png"},
		}},
	}
	tmpRoot, err := dbsvc.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	trackerDir := filepath.Join(tmpDir, "aither")
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		t.Fatalf("tracker dir: %v", err)
	}
	firstPath := filepath.Join(trackerDir, "4m092k_01.png")
	secondPath := filepath.Join(trackerDir, "7oj122_02.png")
	for _, pathValue := range []string{firstPath, secondPath} {
		if err := os.WriteFile(pathValue, []byte("png"), 0o600); err != nil {
			t.Fatalf("write tracker artifact %s: %v", pathValue, err)
		}
	}

	repo := &stubRepo{
		descriptionOverride: strings.TrimSpace(`
[center]
[url=https://ptpimg.me/4m092k.png][img]https://ptpimg.me/4m092k.png[/img][/url]
[url=https://ptpimg.me/7oj122.png][img]https://ptpimg.me/7oj122.png[/img][/url]
[/center]`),
		overrideGroupKey: "hdb",
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://ptpimg.me/4m092k.png", "https://ptpimg.me/7oj122.png"},
		}},
	}
	images := &stubImageService{
		uploads: map[string][]api.UploadedImageLink{
			"hdb": {
				{SourcePath: meta.SourcePath, ImagePath: firstPath, Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/51q8jo2.jpg", RawURL: "https://img.hdbits.org/51q8jo2.jpg", WebURL: "https://img.hdbits.org/51q8jo2"},
				{SourcePath: meta.SourcePath, ImagePath: secondPath, Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/w0S7ltI.jpg", RawURL: "https://img.hdbits.org/w0S7ltI.jpg", WebURL: "https://img.hdbits.org/w0S7ltI"},
			},
		},
	}

	svc := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, registry, images)
	preview, err := svc.BuildPreparation(context.Background(), meta, []string{"HDB"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected 1 description group, got %d", len(preview.Descriptions))
	}
	description := preview.Descriptions[0].Description
	if strings.TrimSpace(description) == "" {
		t.Fatal("expected HDB description to be built")
	}
	if strings.Contains(description, "ptpimg.me/4m092k.png") || strings.Contains(description, "ptpimg.me/7oj122.png") {
		t.Fatalf("expected HDB screenshots to replace original tracker urls, got %q", description)
	}
	if !strings.Contains(description, "img.hdbits.org/51q8jo2") || !strings.Contains(description, "img.hdbits.org/w0S7ltI") {
		t.Fatalf("expected HDB screenshot urls in description, got %q", description)
	}
}

func TestBuildPreparationPreloadsDescriptionAssetQueriesOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "HDB", group: "hdb", description: "same description"},
		stubPreparationDefinition{name: "AITHER", group: "aither", description: "same description"},
		stubPreparationDefinition{name: "BHD", group: "bhd", description: "same description"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", UsageScope: "global", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", UsageScope: "global", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
		},
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)
	_, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"}, []string{"HDB", "AITHER", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.overrideCalls != 1 {
		t.Fatalf("expected 1 description override query, got %d", repo.overrideCalls)
	}
	if repo.trackerRecordsCalls != 1 {
		t.Fatalf("expected 1 tracker metadata query, got %d", repo.trackerRecordsCalls)
	}
	if repo.selectionsCalls != 1 {
		t.Fatalf("expected 1 final selections query, got %d", repo.selectionsCalls)
	}
	if repo.uploadsCalls != 1 {
		t.Fatalf("expected 1 uploaded images query, got %d", repo.uploadsCalls)
	}
}

func TestBuildPreparationSkipsBlockedTrackers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubPreparationDefinition{name: "HDB", group: "hdb", description: "hdb"},
		stubPreparationDefinition{name: "BHD", group: "bhd", description: "bhd"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	preview, err := svc.BuildPreparation(context.Background(), api.PreparedMetadata{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	}, []string{"HDB", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preview.Descriptions) != 1 {
		t.Fatalf("expected 1 description, got %d", len(preview.Descriptions))
	}
	if len(preview.Descriptions[0].Trackers) != 1 || preview.Descriptions[0].Trackers[0] != "BHD" {
		t.Fatalf("expected only BHD in preparation preview, got %#v", preview.Descriptions[0].Trackers)
	}
}

func TestBuildUploadDryRunSkipsBlockedTrackers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDryRunDefinition{name: "HDB"},
		stubDryRunDefinition{name: "BHD"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	entries, err := svc.BuildUploadDryRun(context.Background(), api.PreparedMetadata{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	}, []string{"HDB", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 dry-run entry, got %d", len(entries))
	}
	if entries[0].Tracker != "BHD" {
		t.Fatalf("expected only BHD dry-run entry, got %q", entries[0].Tracker)
	}
}

func TestUploadSkipsBlockedTrackersBeforePendingRecords(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	registry := NewRegistry()
	for _, definition := range []Definition{
		trackingUploadDefinition{name: "HDB", started: started},
		trackingUploadDefinition{name: "AITHER", started: started},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{}
	cfg := config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"HDB", "AITHER"}}}
	svc := NewServiceWithRegistry(cfg, nil, repo, registry)

	summary, err := svc.Upload(context.Background(), api.PreparedMetadata{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected 1 upload, got %d", summary.Uploaded)
	}
	if len(repo.createdUploads) != 1 || repo.createdUploads[0].Tracker != "AITHER" {
		t.Fatalf("expected pending record only for AITHER, got %#v", repo.createdUploads)
	}

	close(started)
	startedTrackers := make([]string, 0, len(started))
	for tracker := range started {
		startedTrackers = append(startedTrackers, tracker)
	}
	if len(startedTrackers) != 1 || startedTrackers[0] != "AITHER" {
		t.Fatalf("expected upload to run only for AITHER, got %#v", startedTrackers)
	}
}

func TestBuildUploadDryRunPreloadsDescriptionAssetQueriesOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDryRunDefinition{name: "HDB"},
		stubDryRunDefinition{name: "AITHER"},
		stubDryRunDefinition{name: "BHD"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register stub: %v", err)
		}
	}

	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", UsageScope: "global", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", UsageScope: "global", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
		},
	}

	svc := NewServiceWithRegistry(config.Config{}, nil, repo, registry)
	_, err := svc.BuildUploadDryRun(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"}, []string{"HDB", "AITHER", "BHD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.overrideCalls != 1 {
		t.Fatalf("expected 1 description override query, got %d", repo.overrideCalls)
	}
	if repo.trackerRecordsCalls != 1 {
		t.Fatalf("expected 1 tracker metadata query, got %d", repo.trackerRecordsCalls)
	}
	if repo.selectionsCalls != 1 {
		t.Fatalf("expected 1 final selections query, got %d", repo.selectionsCalls)
	}
	if repo.uploadsCalls != 1 {
		t.Fatalf("expected 1 uploaded images query, got %d", repo.uploadsCalls)
	}
}
