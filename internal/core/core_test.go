// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func ptr[T any](v T) *T {
	return &v
}

func TestFirstRequestedTracker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		trackers []string
		want     string
	}{
		{name: "nil", trackers: nil, want: ""},
		{name: "empty first", trackers: []string{"", "BHD"}, want: "BHD"},
		{name: "whitespace first", trackers: []string{" \t", " HDB "}, want: "HDB"},
		{name: "all empty", trackers: []string{"", "  "}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := firstRequestedTracker(tt.trackers); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestBuildDescriptionBuilderGroupAddsBHDMediaInfoPreviewOnly(t *testing.T) {
	t.Parallel()

	mediaInfoPath := filepath.Join(t.TempDir(), "MEDIAINFO.txt")
	if err := os.WriteFile(mediaInfoPath, []byte(coreDescriptionBuilderMediaInfoSample()), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	group := buildDescriptionBuilderGroup(api.PreparationDescription{
		GroupKey:        "bhd",
		Trackers:        []string{"BHD"},
		RawDescription:  "",
		Description:     "Generated BHD description",
		DescriptionHTML: descriptionHTMLForTest("Generated BHD description"),
	}, nil, api.PreparedMetadata{MediaInfoTextPath: mediaInfoPath}, api.NopLogger{})

	if group.RawDescription != "Generated BHD description" {
		t.Fatalf("expected raw description unchanged, got %q", group.RawDescription)
	}
	if !strings.Contains(group.RawDescriptionHTML, `class="mediainfo"`) {
		t.Fatalf("expected BHD mediainfo preview html, got %q", group.RawDescriptionHTML)
	}
	if !strings.Contains(group.RawDescriptionHTML, "Generated BHD description") {
		t.Fatalf("expected generated description to remain in preview html, got %q", group.RawDescriptionHTML)
	}
}

func TestBuildDescriptionBuilderGroupDoesNotAugmentOverrides(t *testing.T) {
	t.Parallel()

	mediaInfoPath := filepath.Join(t.TempDir(), "MEDIAINFO.txt")
	if err := os.WriteFile(mediaInfoPath, []byte(coreDescriptionBuilderMediaInfoSample()), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	group := buildDescriptionBuilderGroup(api.PreparationDescription{
		GroupKey:    "hdb",
		Trackers:    []string{"HDB"},
		Description: "Generated HDB description",
	}, map[string]api.DescriptionOverride{
		"hdb": {Description: "custom override"},
	}, api.PreparedMetadata{MediaInfoTextPath: mediaInfoPath}, api.NopLogger{})

	if group.RawDescription != "custom override" {
		t.Fatalf("expected override raw description, got %q", group.RawDescription)
	}
	if strings.Contains(group.RawDescriptionHTML, `class="mediainfo"`) {
		t.Fatalf("did not expect override preview to be augmented, got %q", group.RawDescriptionHTML)
	}
}

func descriptionHTMLForTest(value string) string {
	return value
}

func coreDescriptionBuilderMediaInfoSample() string {
	return `General
Complete name : C:\Media\Movie.2024.1080p.mkv
Format : Matroska
File size : 10.4 GiB
Duration : 1 h 42 min
Overall bit rate : 14.6 Mb/s

Video
Format : AVC
Width : 1 920 pixels
Height : 1 080 pixels`
}

func TestGetHistoryOverviewIncludesGroupedDescriptionOverrides(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "core-history.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		if cerr := repo.Close(); cerr != nil {
			t.Fatalf("close repo: %v", cerr)
		}
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "Example.mkv")
	updatedAt := time.Now().UTC().Add(-time.Hour)
	groupedOverride := db.DescriptionOverride{
		SourcePath:  sourcePath,
		GroupKey:    "unit3d",
		Description: "grouped override",
		UpdatedAt:   time.Now().UTC(),
	}

	if err := repo.Save(context.Background(), db.FileMetadata{
		Path:       sourcePath,
		Title:      "Example",
		Source:     "WEB",
		Resolution: "2160p",
		UpdatedAt:  updatedAt,
	}); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := repo.SaveDescriptionOverride(context.Background(), groupedOverride); err != nil {
		t.Fatalf("save description override: %v", err)
	}

	core := &Core{repo: repo, logger: api.NopLogger{}}

	overview, err := core.GetHistoryOverview(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("get history overview: %v", err)
	}
	if len(overview.DescriptionOverrides) != 1 {
		t.Fatalf("expected 1 grouped description override, got %d", len(overview.DescriptionOverrides))
	}
	if overview.DescriptionOverrides[0].GroupKey != "unit3d" {
		t.Fatalf("expected grouped override key to be preserved, got %q", overview.DescriptionOverrides[0].GroupKey)
	}
	if overview.DescriptionOverride.GroupKey != "unit3d" {
		t.Fatalf("expected preferred description override key to be unit3d, got %q", overview.DescriptionOverride.GroupKey)
	}
	if overview.DescriptionOverride.Description != "grouped override" {
		t.Fatalf("expected preferred description override body to be preserved, got %q", overview.DescriptionOverride.Description)
	}
}

func TestRunUploadMultiplePaths(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	svc := api.ServiceSet{
		Filesystem: &stubFS{},
		Metadata:   &stubMeta{},
		Torrents:   &stubTorrent{},
		Clients:    &stubClient{},
		Trackers:   &stubTrackers{},
	}

	core, err := New(api.CoreDependencies{
		Config:     config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services:   svc,
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})
	core.storeDupeCache("/tmp/b", "", api.PreparedMetadata{SourcePath: "/tmp/b"})

	result, err := core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a", "/tmp/b"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if result.UploadedCount != 2 {
		t.Fatalf("expected 2 uploads, got %d", result.UploadedCount)
	}

	metaSvc, ok := svc.Metadata.(*stubMeta)
	if !ok {
		t.Fatalf("expected metadata service type *stubMeta, got %T", svc.Metadata)
	}
	if metaCalls := metaSvc.calls; metaCalls != 0 {
		t.Fatalf("expected 0 metadata calls, got %d", metaCalls)
	}
	trackerSvc, ok := svc.Trackers.(*stubTrackers)
	if !ok {
		t.Fatalf("expected tracker service type *stubTrackers, got %T", svc.Trackers)
	}
	if trackerCalls := trackerSvc.calls; trackerCalls != 2 {
		t.Fatalf("expected 2 tracker calls, got %d", trackerCalls)
	}
}

func TestResolveGUICachedPreparedMetaReusesRequestRefreshedCache(t *testing.T) {
	t.Parallel()

	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Metadata: metaSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	sourcePath := "/tmp/a"
	req := api.Request{
		Paths: []string{sourcePath},
		Mode:  api.ModeGUI,
	}
	core.storeDupeCache(sourcePath, "", api.PreparedMetadata{SourcePath: sourcePath})

	if _, ok, err := core.resolveGUICachedPreparedMeta(context.Background(), req, sourcePath); err != nil {
		t.Fatalf("resolve cached prepared metadata: %v", err)
	} else if !ok {
		t.Fatal("expected cached metadata")
	}
	if metaSvc.refreshCalls != 1 {
		t.Fatalf("expected first lookup to refresh once, got %d", metaSvc.refreshCalls)
	}

	if _, ok, err := core.resolveGUICachedPreparedMeta(context.Background(), req, sourcePath); err != nil {
		t.Fatalf("resolve cached prepared metadata again: %v", err)
	} else if !ok {
		t.Fatal("expected cached metadata on second lookup")
	}
	if metaSvc.refreshCalls != 1 {
		t.Fatalf("expected second lookup to reuse refreshed cache, got %d refreshes", metaSvc.refreshCalls)
	}

	edition := "Director's Cut"
	editedReq := req
	editedReq.ReleaseNameOverrides = api.ReleaseNameOverrides{Edition: &edition}
	if _, ok, err := core.resolveGUICachedPreparedMeta(context.Background(), editedReq, sourcePath); err != nil {
		t.Fatalf("resolve edited cached prepared metadata: %v", err)
	} else if !ok {
		t.Fatal("expected fallback cache for edited request")
	}
	if metaSvc.refreshCalls != 2 {
		t.Fatalf("expected edited request to refresh once, got %d refreshes", metaSvc.refreshCalls)
	}

	if _, ok, err := core.resolveGUICachedPreparedMeta(context.Background(), editedReq, sourcePath); err != nil {
		t.Fatalf("resolve edited cached prepared metadata again: %v", err)
	} else if !ok {
		t.Fatal("expected exact edited cache on second lookup")
	}
	if metaSvc.refreshCalls != 2 {
		t.Fatalf("expected repeated edited request to reuse refreshed cache, got %d refreshes", metaSvc.refreshCalls)
	}
}

func TestResolveGUICachedPreparedMetaTreatsResolvedTrackerDataAsCacheMatch(t *testing.T) {
	t.Parallel()

	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Metadata: metaSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	sourcePath := "/tmp/a"
	req := api.Request{
		Paths:    []string{sourcePath},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER", "HDB"},
	}
	meta := api.PreparedMetadata{
		SourcePath:     sourcePath,
		Paths:          []string{sourcePath},
		Mode:           api.ModeGUI,
		Trackers:       []string{"AITHER", "HDB"},
		TrackersRemove: []string{"HDB"},
		TrackerIDs:     map[string]string{"hdb": "123"},
	}
	core.storeRefreshedDupeCache(sourcePath, "", meta)

	if _, ok, err := core.resolveGUICachedPreparedMeta(context.Background(), req, sourcePath); err != nil {
		t.Fatalf("resolve cached prepared metadata: %v", err)
	} else if !ok {
		t.Fatal("expected cached metadata")
	}
	if metaSvc.refreshCalls != 0 {
		t.Fatalf("expected resolved tracker data to remain cacheable, got %d refreshes", metaSvc.refreshCalls)
	}
}

func TestResolveGUICachedPreparedMetaAllowsTrackerlessFollowUp(t *testing.T) {
	t.Parallel()

	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Metadata: metaSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	sourcePath := "/tmp/a"
	meta := api.PreparedMetadata{
		SourcePath: sourcePath,
		Paths:      []string{sourcePath},
		Mode:       api.ModeGUI,
		Trackers:   []string{"AITHER", "HDB"},
	}
	core.storeRefreshedDupeCache(sourcePath, "", meta)

	if _, ok, err := core.resolveGUICachedPreparedMeta(context.Background(), api.Request{
		Paths: []string{sourcePath},
		Mode:  api.ModeGUI,
	}, sourcePath); err != nil {
		t.Fatalf("resolve trackerless cached prepared metadata: %v", err)
	} else if !ok {
		t.Fatal("expected cached metadata")
	}
	if metaSvc.refreshCalls != 0 {
		t.Fatalf("expected trackerless follow-up to reuse cache, got %d refreshes", metaSvc.refreshCalls)
	}
}

func TestRunUploadPreparedDryRunSkipsUpload(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	tracker := &stubTrackers{}
	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   tracker,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	result, err := core.RunUploadPrepared(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			DryRun: true,
		},
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if result.UploadedCount != 0 {
		t.Fatalf("expected 0 uploads, got %d", result.UploadedCount)
	}
	if tracker.calls != 0 {
		t.Fatalf("expected tracker not called, got %d", tracker.calls)
	}
	if client.calls != 0 {
		t.Fatalf("expected client not called, got %d", client.calls)
	}
}

func TestRunUploadPreparedSiteCheckForcesDryRun(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	tracker := &stubTrackers{}
	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   tracker,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	result, err := core.RunUploadPrepared(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
		Execution: api.ExecutionOptions{
			SiteCheck: true,
		},
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if result.UploadedCount != 0 {
		t.Fatalf("expected 0 uploads, got %d", result.UploadedCount)
	}
	if tracker.calls != 0 {
		t.Fatalf("expected tracker not called, got %d", tracker.calls)
	}
	if client.calls != 0 {
		t.Fatalf("expected client not called, got %d", client.calls)
	}
}

func TestRunUploadNoSeedSkipsClient(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	tracker := &stubTrackers{}
	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   tracker,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	result, err := core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
		Options: api.UploadOptions{
			NoSeed: true,
		},
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if result.UploadedCount != 1 {
		t.Fatalf("expected 1 upload, got %d", result.UploadedCount)
	}
	if tracker.calls != 1 {
		t.Fatalf("expected tracker called once, got %d", tracker.calls)
	}
	if client.calls != 0 {
		t.Fatalf("expected client not called, got %d", client.calls)
	}
}

func TestRunUploadPreparedSiteUploadTrackerOverridesTrackers(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	tracker := &stubTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   tracker,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUploadPrepared(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
		Execution: api.ExecutionOptions{
			SiteUploadTracker: "BLU",
		},
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if len(tracker.lastMeta.Trackers) != 1 || tracker.lastMeta.Trackers[0] != "BLU" {
		t.Fatalf("expected site upload tracker override, got %#v", tracker.lastMeta.Trackers)
	}
}

func TestRunUploadDefaultsScreens(t *testing.T) {
	t.Parallel()

	meta := &stubMeta{}
	tracker := &stubTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 5}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   meta,
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   tracker,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if tracker.lastMeta.Options.Screens != 5 {
		t.Fatalf("expected screens default 5, got %d", tracker.lastMeta.Options.Screens)
	}
}

func TestSaveFinalScreenshotSelectionsCLI(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	shots := &recordingScreenshots{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem:  &stubFS{},
			Metadata:    &stubMeta{},
			Screenshots: shots,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	images := []api.ScreenshotImage{{Path: "/tmp/a.png"}, {Path: "/tmp/b.png"}}
	if err := core.SaveFinalScreenshotSelections(context.Background(), api.Request{Paths: []string{"/tmp/a"}, Mode: api.ModeCLI}, images); err != nil {
		t.Fatalf("save final selections: %v", err)
	}
	if shots.calls != 1 {
		t.Fatalf("expected 1 call, got %d", shots.calls)
	}
	if len(shots.lastImages) != 2 {
		t.Fatalf("expected 2 images, got %d", len(shots.lastImages))
	}

	if err := core.SaveFinalScreenshotSelections(context.Background(), api.Request{Paths: []string{"/tmp/a"}, Mode: api.ModeCLI}, nil); err != nil {
		t.Fatalf("clear final selections: %v", err)
	}
	if shots.calls != 2 {
		t.Fatalf("expected 2 calls, got %d", shots.calls)
	}
}

func TestNewRejectsZeroScreens(t *testing.T) {
	t.Parallel()

	_, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 0}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err == nil {
		t.Fatalf("expected config validation error")
	}
	if !strings.Contains(err.Error(), "screenshot_handling.screens") {
		t.Fatalf("expected screenshot validation error, got %v", err)
	}
}

func TestRunUploadDedupesPaths(t *testing.T) {
	t.Parallel()

	meta := &stubMeta{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   meta,
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a", "/tmp/a"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if meta.calls != 0 {
		t.Fatalf("expected 0 metadata calls, got %d", meta.calls)
	}
}

func TestRunUploadSkipsPathedSearchWithStoredTrackerData(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   &stubTrackers{},
		},
		Repository: trackerRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if client.searchCalls != 0 {
		t.Fatalf("expected pathed search skipped, got %d calls", client.searchCalls)
	}
}

func TestRunUploadSkipsPathedSearchWithStoredInfoHash(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				StoredDataFresh: true,
				StoredInfoHash:  "hash123",
			}},
			Torrents: &stubTorrent{},
			Clients:  client,
			Trackers: &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if client.searchCalls != 0 {
		t.Fatalf("expected pathed search skipped, got %d calls", client.searchCalls)
	}
}

func TestRunUploadSkipsPathedSearchWithFreshStoredSnapshot(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				StoredDataFresh: true,
			}},
			Torrents: &stubTorrent{},
			Clients:  client,
			Trackers: &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if client.searchCalls != 0 {
		t.Fatalf("expected pathed search skipped, got %d calls", client.searchCalls)
	}
}

func TestFetchMetadataPreviewRunsPathedSearchWithStoredTrackerData(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Clients:    client,
		},
		Repository: trackerRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.FetchMetadataPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("fetch metadata preview: %v", err)
	}
	if client.searchCalls != 1 {
		t.Fatalf("expected pathed search to run once for tracker presence, got %d calls", client.searchCalls)
	}
}

func TestFetchMetadataPreviewSkipAutoTorrentSkipsPathedSearch(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Clients:    client,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.FetchMetadataPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			SkipAutoTorrent: true,
		},
	})
	if err != nil {
		t.Fatalf("fetch metadata preview: %v", err)
	}
	if client.searchCalls != 0 {
		t.Fatalf("expected pathed search skipped, got %d calls", client.searchCalls)
	}
}

func TestFetchMetadataPreviewRunsPathedSearchWithFreshStoredSnapshot(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				StoredDataFresh: true,
			}},
			Clients: client,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.FetchMetadataPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("fetch metadata preview: %v", err)
	}
	if client.searchCalls != 1 {
		t.Fatalf("expected pathed search to run once for tracker presence, got %d calls", client.searchCalls)
	}
}

func TestCheckDupesRunsPathedSearchWithSourceURLOverride(t *testing.T) {
	t.Parallel()

	client := &stubClient{searchResult: api.ClientSearchResult{MatchedTrackers: []string{"BLU"}, FoundTrackerMatch: true}}
	dupes := &stubDupes{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				SourceLookupActive: true,
				SourceLookupMode:   "media",
			}},
			Clients: client,
			Dupes:   dupes,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.CheckDupes(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeCLI,
		Trackers: []string{"BLU", "HDB"},
	})
	if err != nil {
		t.Fatalf("check duplicates: %v", err)
	}
	if client.searchCalls != 1 {
		t.Fatalf("expected pathed search to run once, got %d calls", client.searchCalls)
	}
	for _, tracker := range dupes.lastTrackers {
		if tracker == "BLU" {
			t.Fatalf("expected BLU to be removed from dupe check trackers, got %v", dupes.lastTrackers)
		}
	}
}

func TestCheckDupesSkipAutoTorrentSkipsPathedSearch(t *testing.T) {
	t.Parallel()

	client := &stubClient{}
	dupes := &stubDupes{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Clients:    client,
			Dupes:      dupes,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.CheckDupes(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
		Options: api.UploadOptions{
			SkipAutoTorrent: true,
		},
	})
	if err != nil {
		t.Fatalf("check duplicates: %v", err)
	}
	if client.searchCalls != 0 {
		t.Fatalf("expected pathed search skipped, got %d calls", client.searchCalls)
	}
}

func TestAppendPathedDupeResultsCombinesTrackers(t *testing.T) {
	t.Parallel()

	summary := api.DupeCheckSummary{
		Results: []api.DupeCheckResult{
			{Tracker: "ANT", Status: "completed"},
			{
				Tracker: "BLU",
				Status:  "completed",
				Notes:   []string{"pathed torrent match found; skipping dupe search"},
			},
		},
	}

	updated := appendPathedDupeResults(summary, []string{"AITHER", "BLU", "ANTHELION"})

	if len(updated.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(updated.Results))
	}
	if updated.Results[0].Tracker != "ANT" {
		t.Fatalf("expected non-pathed result preserved, got %q", updated.Results[0].Tracker)
	}

	combined := updated.Results[1]
	if combined.Tracker != "AITHER, ANTHELION, BLU" {
		t.Fatalf("expected combined trackers, got %q", combined.Tracker)
	}
	if !combined.HasDupes {
		t.Fatalf("expected combined result to be marked as dupe")
	}
	if combined.Status != "completed" {
		t.Fatalf("expected combined status completed, got %q", combined.Status)
	}
	if len(combined.Notes) != 1 || combined.Notes[0] != "pathed torrent match found; skipping dupe search" {
		t.Fatalf("expected pathed note preserved, got %v", combined.Notes)
	}
}

func TestFetchMetadataPreviewRunsPathedSearchWithSourceURLOverride(t *testing.T) {
	t.Parallel()

	client := &stubClient{searchResult: api.ClientSearchResult{
		MatchedTrackers:   []string{"BLU"},
		FoundTrackerMatch: true,
		TrackerIDs: map[string]string{
			"blu": "999",
		},
	}}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata: &stubMeta{prepared: api.PreparedMetadata{
				SourceLookupActive:    true,
				SourceLookupMode:      "tracker",
				SourceLookupTracker:   "AITHER",
				SourceLookupTrackerID: "111",
				TrackerIDs:            map[string]string{"aither": "111"},
				MatchedTrackers:       []string{"AITHER"},
				TrackerData: []api.TrackerMetadata{{
					Tracker: "AITHER",
					TMDBID:  100,
				}},
			}},
			Clients: client,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.FetchMetadataPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("fetch metadata preview: %v", err)
	}
	if client.searchCalls != 1 {
		t.Fatalf("expected pathed search to run once, got %d calls", client.searchCalls)
	}

	cached, ok := core.getDupeCache("/tmp/a", "")
	if !ok {
		t.Fatalf("expected metadata cache entry")
	}
	if got := cached.TrackerIDs["aither"]; got != "111" {
		t.Fatalf("expected source lookup tracker id preserved, got %q", got)
	}
	foundBLU := false
	for _, tracker := range cached.MatchedTrackers {
		if tracker == "BLU" {
			foundBLU = true
			break
		}
	}
	if !foundBLU {
		t.Fatalf("expected BLU to be tracked as existing in client, got %v", cached.MatchedTrackers)
	}
}

func TestRunUploadPersistsInfoHash(t *testing.T) {
	t.Parallel()

	repo := &recordingRepo{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrentWithHash{hash: "hash123"},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUpload(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("run upload: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("expected 1 metadata save, got %d", len(repo.saved))
	}
	if repo.saved[0].InfoHash != "hash123" {
		t.Fatalf("expected info hash saved, got %q", repo.saved[0].InfoHash)
	}
}

func TestExportGUICachedPreparedMetaExactSignature(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{
		SourcePath:           "/tmp/a",
		ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: ptr("Exact Match")},
	}
	req := api.Request{
		Paths:                []string{"/tmp/a"},
		Mode:                 api.ModeGUI,
		ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: ptr("Exact Match")},
	}
	if err := core.ImportPreparedMetadataForGUI(context.Background(), req, prepared); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	exported, ok, err := core.ExportGUICachedPreparedMeta(context.Background(), req)
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if !ok {
		t.Fatal("expected cached metadata export to succeed")
	}
	if exported.ReleaseNameOverrides.Edition == nil || *exported.ReleaseNameOverrides.Edition != "Exact Match" {
		t.Fatalf("expected exact-signature metadata, got %#v", exported.ReleaseNameOverrides)
	}
}

func TestExportGUICachedPreparedMetaFallsBackForNonExternalSignedOverrides(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{SourcePath: "/tmp/a"}
	if err := core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, prepared); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	exported, ok, err := core.ExportGUICachedPreparedMeta(context.Background(), api.Request{
		Paths:                []string{"/tmp/a"},
		Mode:                 api.ModeGUI,
		ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: ptr("Later UI edit")},
	})
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if !ok {
		t.Fatal("expected non-external GUI override to fall back to cached metadata")
	}
	if exported.SourcePath != "/tmp/a" {
		t.Fatalf("expected cached prepared metadata on fallback, got %q", exported.SourcePath)
	}
}

func TestCheckDupesGUIFallbackReappliesReleaseOverrides(t *testing.T) {
	t.Parallel()

	dupes := &stubDupes{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      dupes,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	if err := core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Release: api.ReleaseInfo{
			Title:  "Example",
			Source: "WEB",
		},
	}); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	category := "TV"
	edition := "Hybrid"
	if _, err := core.CheckDupes(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			Category: &category,
			Edition:  &edition,
		},
	}); err != nil {
		t.Fatalf("check dupes: %v", err)
	}

	if dupes.lastMeta.ReleaseNameOverrides.Category == nil || *dupes.lastMeta.ReleaseNameOverrides.Category != category {
		t.Fatalf("expected dupe check to receive category override, got %#v", dupes.lastMeta.ReleaseNameOverrides)
	}
	if dupes.lastMeta.ReleaseNameOverrides.Edition == nil || *dupes.lastMeta.ReleaseNameOverrides.Edition != edition {
		t.Fatalf("expected dupe check to receive edition override, got %#v", dupes.lastMeta.ReleaseNameOverrides)
	}
}

func TestExportGUICachedPreparedMetaRequiresExactMatchForExternalOverrides(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	if err := core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{SourcePath: "/tmp/a"}); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	tmdbID := 1234

	_, ok, err := core.ExportGUICachedPreparedMeta(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if ok {
		t.Fatal("expected external ID override to require exact cache signature")
	}
}

func TestExportGUICachedPreparedMetaReturnsIsolatedCopy(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Trackers:   []string{"AITHER", "BLU"},
		TrackerIDs: map[string]string{"AITHER": "111"},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"AITHER": {"season": "1"},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {{Rule: "rule_a", Reason: "bad"}},
		},
		Release: api.ReleaseInfo{
			Codec: []string{"x264"},
		},
		BDInfo: map[string]interface{}{
			"playlists": []interface{}{"00001", "00002"},
		},
	}
	if err := core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, prepared); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	exported, ok, err := core.ExportGUICachedPreparedMeta(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if !ok {
		t.Fatal("expected cached metadata export to succeed")
	}

	exported.Trackers[0] = "HDB"
	exported.TrackerIDs["AITHER"] = "222"
	exported.TrackerQuestionnaireAnswers["AITHER"]["season"] = "2"
	exported.TrackerRuleFailures["AITHER"][0].Rule = "rule_b"
	exported.Release.Codec[0] = "x265"
	exportedPlaylists, ok := exported.BDInfo["playlists"].([]interface{})
	if !ok {
		t.Fatalf("expected exported BDInfo playlists to be []interface{}, got %T", exported.BDInfo["playlists"])
	}
	exportedPlaylists[0] = "99999"

	cached, ok := core.getDupeCache("/tmp/a", "")
	if !ok {
		t.Fatal("expected cached metadata to remain available")
	}
	if cached.Trackers[0] != "AITHER" {
		t.Fatalf("expected cached trackers to remain isolated, got %#v", cached.Trackers)
	}
	if cached.TrackerIDs["AITHER"] != "111" {
		t.Fatalf("expected cached tracker ids to remain isolated, got %#v", cached.TrackerIDs)
	}
	if cached.TrackerQuestionnaireAnswers["AITHER"]["season"] != "1" {
		t.Fatalf("expected cached questionnaire answers to remain isolated, got %#v", cached.TrackerQuestionnaireAnswers)
	}
	if cached.TrackerRuleFailures["AITHER"][0].Rule != "rule_a" {
		t.Fatalf("expected cached rule failures to remain isolated, got %#v", cached.TrackerRuleFailures)
	}
	if cached.Release.Codec[0] != "x264" {
		t.Fatalf("expected cached release info to remain isolated, got %#v", cached.Release)
	}
	cachedPlaylists, ok := cached.BDInfo["playlists"].([]interface{})
	if !ok {
		t.Fatalf("expected cached BDInfo playlists to be []interface{}, got %T", cached.BDInfo["playlists"])
	}
	if cachedPlaylists[0] != "00001" {
		t.Fatalf("expected cached BDInfo to remain isolated, got %#v", cached.BDInfo)
	}
}

func TestExportGUICachedPreparedMetaConcurrentCopiesStayIsolated(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	if err := core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Trackers:   []string{"AITHER"},
		TrackerIDs: map[string]string{"AITHER": "111"},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"AITHER": {"season": "1"},
		},
	}); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	var wg sync.WaitGroup
	for idx := 0; idx < 16; idx++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			exported, ok, exportErr := core.ExportGUICachedPreparedMeta(context.Background(), api.Request{
				Paths: []string{"/tmp/a"},
				Mode:  api.ModeGUI,
			})
			if exportErr != nil || !ok {
				t.Errorf("export gui cached prepared meta: ok=%t err=%v", ok, exportErr)
				return
			}
			exported.Trackers[0] = "TRACKER"
			exported.TrackerIDs["AITHER"] = "mutated"
			exported.TrackerQuestionnaireAnswers["AITHER"]["season"] = strconv.Itoa(i)
		}(idx)
	}
	wg.Wait()

	cached, ok := core.getDupeCache("/tmp/a", "")
	if !ok {
		t.Fatal("expected cached metadata to remain available")
	}
	if cached.Trackers[0] != "AITHER" {
		t.Fatalf("expected cached trackers unchanged, got %#v", cached.Trackers)
	}
	if cached.TrackerIDs["AITHER"] != "111" {
		t.Fatalf("expected cached tracker ids unchanged, got %#v", cached.TrackerIDs)
	}
	if cached.TrackerQuestionnaireAnswers["AITHER"]["season"] != "1" {
		t.Fatalf("expected cached questionnaire answers unchanged, got %#v", cached.TrackerQuestionnaireAnswers)
	}
}

func TestExportGUICachedPreparedMetaHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, ok, err := core.ExportGUICachedPreparedMeta(ctx, api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if ok {
		t.Fatal("expected canceled export to report no cached metadata")
	}
}

func TestImportPreparedMetadataForGUIHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = core.ImportPreparedMetadataForGUI(ctx, api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{SourcePath: "/tmp/a"})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if _, ok := core.getDupeCache("/tmp/a", ""); ok {
		t.Fatal("expected canceled import not to populate cache")
	}
}

func TestExportGUICachedPreparedMetaReturnsFilesystemValidationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("validate boom")
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: stubFS{err: wantErr},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, _, err = core.ExportGUICachedPreparedMeta(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected filesystem validation error, got %v", err)
	}
}

func TestImportPreparedMetadataForGUIReturnsFilesystemValidationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("validate boom")
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: stubFS{err: wantErr},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	err = core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{SourcePath: "/tmp/a"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected filesystem validation error, got %v", err)
	}
}

func TestExportGUICachedPreparedMetaAllowsDuplicatePathsResolvingToOne(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: stubFS{normalized: []string{"/tmp/a", "/tmp/a"}},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	if err := core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{SourcePath: "/tmp/a"}); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}

	_, ok, err := core.ExportGUICachedPreparedMeta(context.Background(), api.Request{
		Paths: []string{"/tmp/a", "/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if !ok {
		t.Fatal("expected duplicate normalized paths to resolve to one cache hit")
	}
}

func TestImportPreparedMetadataForGUIAllowsDuplicatePathsResolvingToOne(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: stubFS{normalized: []string{"/tmp/a", "/tmp/a"}},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	err = core.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a", "/tmp/a"},
		Mode:  api.ModeGUI,
	}, api.PreparedMetadata{SourcePath: "/tmp/a"})
	if err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}
	if _, ok := core.getDupeCache("/tmp/a", ""); !ok {
		t.Fatal("expected duplicate normalized paths to import one cache entry")
	}
}

func TestFetchMetadataPreviewPurgesContentForExternalOverrides(t *testing.T) {
	t.Parallel()

	repo := &recordingRepo{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Clients:    &stubClient{},
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	tmdbID := 123
	_, err = core.FetchMetadataPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &tmdbID,
		},
	})
	if err != nil {
		t.Fatalf("fetch metadata preview: %v", err)
	}
	if repo.purgeCalls != 1 {
		t.Fatalf("expected 1 purge call, got %d", repo.purgeCalls)
	}
	if len(repo.purgedPaths) != 1 || repo.purgedPaths[0] != "/tmp/a" {
		t.Fatalf("unexpected purged paths: %v", repo.purgedPaths)
	}
}

func TestFetchMetadataPreviewWithoutExternalOverridesDoesNotPurge(t *testing.T) {
	t.Parallel()

	repo := &recordingRepo{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Clients:    &stubClient{},
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.FetchMetadataPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("fetch metadata preview: %v", err)
	}
	if repo.purgeCalls != 0 {
		t.Fatalf("expected no purge calls, got %d", repo.purgeCalls)
	}
}

func TestRunUploadPreparedUsesCachedMetadata(t *testing.T) {
	t.Parallel()

	meta := &stubMeta{}
	tracker := &stubTrackers{}
	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   meta,
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   tracker,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{SourcePath: "/tmp/a", Trackers: []string{"AITHER"}}
	core.storeDupeCache("/tmp/a", "", prepared)

	result, err := core.RunUploadPrepared(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"HDB"},
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if result.UploadedCount != 1 {
		t.Fatalf("expected 1 upload, got %d", result.UploadedCount)
	}
	if meta.calls != 0 {
		t.Fatalf("expected no metadata prepare calls, got %d", meta.calls)
	}
	if tracker.calls != 1 {
		t.Fatalf("expected tracker called once, got %d", tracker.calls)
	}
	if client.calls != 1 {
		t.Fatalf("expected client inject called once, got %d", client.calls)
	}
	if len(tracker.lastMeta.Trackers) != 1 || tracker.lastMeta.Trackers[0] != "HDB" {
		t.Fatalf("expected request tracker override applied, got %v", tracker.lastMeta.Trackers)
	}
}

func TestRunUploadPreparedInjectsEachUploadedTrackerURL(t *testing.T) {
	t.Parallel()

	meta := &stubMeta{}
	tracker := &stubTrackers{
		summary: api.UploadSummary{
			Uploaded: 2,
			UploadedTorrents: []api.UploadedTorrent{
				{Tracker: "AITHER", DownloadURL: "https://aither.cc/torrent/download/111"},
				{Tracker: "BLU", DownloadURL: "https://blutopia.cc/torrent/download/222"},
			},
		},
	}
	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   meta,
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   tracker,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{SourcePath: "/tmp/a", Trackers: []string{"AITHER", "BLU"}}
	core.storeDupeCache("/tmp/a", "", prepared)

	result, err := core.RunUploadPrepared(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if result.UploadedCount != 2 {
		t.Fatalf("expected 2 uploads, got %d", result.UploadedCount)
	}
	if client.calls != 2 {
		t.Fatalf("expected client inject called twice, got %d", client.calls)
	}
}

func TestRunUploadPreparedForwardsTrackerTorrentPath(t *testing.T) {
	t.Parallel()

	meta := &stubMeta{}
	tracker := &stubTrackers{
		summary: api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDB",
				DownloadURL: "https://hdbits.org/download.php/file?id=333&passkey=abc",
				TorrentPath: "/tmp/release.hdb.torrent",
			}},
		},
	}
	client := &stubClient{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   meta,
			Torrents:   &stubTorrent{},
			Clients:    client,
			Trackers:   tracker,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{SourcePath: "/tmp/a", Trackers: []string{"HDB"}}
	core.storeDupeCache("/tmp/a", "", prepared)

	result, err := core.RunUploadPrepared(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if result.UploadedCount != 1 {
		t.Fatalf("expected 1 upload, got %d", result.UploadedCount)
	}
	if len(client.injected) != 1 {
		t.Fatalf("expected one injection payload, got %d", len(client.injected))
	}
	if client.injected[0].Path != "/tmp/release.hdb.torrent" {
		t.Fatalf("expected torrent path to be forwarded, got %q", client.injected[0].Path)
	}
	if client.injected[0].URL != "https://hdbits.org/download.php/file?id=333&passkey=abc" {
		t.Fatalf("expected download URL to be forwarded, got %q", client.injected[0].URL)
	}
}

func TestRunUploadPreparedRequiresCachedMetadata(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.RunUploadPrepared(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err == nil {
		t.Fatalf("expected upload-only cache error")
	}
	if !strings.Contains(err.Error(), "requires prepared metadata") {
		t.Fatalf("expected prepared metadata error, got %v", err)
	}
}

func TestFetchTrackerDryRunPreviewUsesCachedMetadata(t *testing.T) {
	t.Parallel()

	meta := &stubMeta{}
	tracker := &stubTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   meta,
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   tracker,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	prepared := api.PreparedMetadata{SourcePath: "/tmp/a", ReleaseName: "Watcher 2160p WEB-DL DD+ 5.1-FLUX"}
	core.storeDupeCache("/tmp/a", "", prepared)

	preview, err := core.FetchTrackerDryRunPreview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("fetch tracker dry-run preview: %v", err)
	}
	if preview.SourcePath != "/tmp/a" {
		t.Fatalf("expected source path /tmp/a, got %q", preview.SourcePath)
	}
	if meta.calls != 0 {
		t.Fatalf("expected no metadata prepare calls, got %d", meta.calls)
	}
	if tracker.dryRunCalls != 1 {
		t.Fatalf("expected 1 dry-run build call, got %d", tracker.dryRunCalls)
	}
}

func TestFetchTrackerDryRunPreviewPassesRuleFailureOverride(t *testing.T) {
	t.Parallel()

	tracker := &stubTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   tracker,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.FetchTrackerDryRunPreview(context.Background(), api.Request{
		Paths:                     []string{"/tmp/a"},
		Mode:                      api.ModeGUI,
		Trackers:                  []string{"AITHER"},
		IgnoreTrackerRuleFailures: true,
	})
	if err != nil {
		t.Fatalf("fetch tracker dry-run preview: %v", err)
	}
	if !tracker.lastMeta.IgnoreTrackerRuleFailures {
		t.Fatalf("expected ignore tracker rule failures flag to be passed to dry-run")
	}
}

func TestFetchTrackerDryRunPreviewRequiresCachedMetadata(t *testing.T) {
	t.Parallel()

	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   &stubTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	_, err = core.FetchTrackerDryRunPreview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err == nil {
		t.Fatalf("expected tracker dry-run cache error")
	}
	if !strings.Contains(err.Error(), "requires prepared metadata") {
		t.Fatalf("expected prepared metadata error, got %v", err)
	}
}

func TestRunUploadPreparedPassesRuleFailureOverride(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	tracker := &stubTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   tracker,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	_, err = core.RunUploadPrepared(context.Background(), api.Request{
		Paths:                     []string{"/tmp/a"},
		Mode:                      api.ModeGUI,
		Trackers:                  []string{"AITHER"},
		IgnoreTrackerRuleFailures: true,
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if !tracker.lastMeta.IgnoreTrackerRuleFailures {
		t.Fatalf("expected ignore tracker rule failures flag to be passed to upload")
	}
}

func TestRunUploadPreparedFiltersRuleFailuresPerTracker(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{}
	tracker := &stubTrackers{}
	core, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Torrents:   &stubTorrent{},
			Clients:    &stubClient{},
			Trackers:   tracker,
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	core.storeDupeCache("/tmp/a", "", api.PreparedMetadata{
		SourcePath: "/tmp/a",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {{Rule: "rule_a", Reason: "a"}},
			"BLU":    {{Rule: "rule_b", Reason: "b"}},
		},
	})

	_, err = core.RunUploadPrepared(context.Background(), api.Request{
		Paths:                        []string{"/tmp/a"},
		Mode:                         api.ModeCLI,
		Trackers:                     []string{"AITHER", "BLU"},
		IgnoreTrackerRuleFailuresFor: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("run upload prepared: %v", err)
	}
	if _, ok := tracker.lastMeta.TrackerRuleFailures["AITHER"]; ok {
		t.Fatalf("expected AITHER rule failure removed from prepared meta")
	}
	if _, ok := tracker.lastMeta.TrackerRuleFailures["BLU"]; !ok {
		t.Fatalf("expected BLU rule failure preserved")
	}
}

type stubRepo struct{}

func (stubRepo) GetByPath(context.Context, string) (db.FileMetadata, error) {
	return db.FileMetadata{}, internalerrors.ErrNotImplemented
}

func (stubRepo) Save(context.Context, db.FileMetadata) error {
	return nil
}

func (stubRepo) GetExternalIDs(context.Context, string) (db.ExternalIDs, error) {
	return db.ExternalIDs{}, internalerrors.ErrNotImplemented
}

func (stubRepo) SaveExternalIDs(context.Context, db.ExternalIDs) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) GetExternalMetadata(context.Context, string) (db.ExternalMetadata, error) {
	return db.ExternalMetadata{}, internalerrors.ErrNotImplemented
}

func (stubRepo) SaveExternalMetadata(context.Context, db.ExternalMetadata) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) GetDVDMediaInfo(context.Context, string) (db.DVDMediaInfo, error) {
	return db.DVDMediaInfo{}, internalerrors.ErrNotFound
}

func (stubRepo) SaveDVDMediaInfo(context.Context, db.DVDMediaInfo) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) GetReleaseNameOverrides(context.Context, string) (db.ReleaseNameOverrides, error) {
	return db.ReleaseNameOverrides{}, internalerrors.ErrNotImplemented
}

func (stubRepo) SaveReleaseNameOverrides(context.Context, string, db.ReleaseNameOverrides) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) DeleteReleaseNameOverrides(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) GetDescriptionOverride(context.Context, string, string) (db.DescriptionOverride, error) {
	return db.DescriptionOverride{}, internalerrors.ErrNotImplemented
}
func (stubRepo) ListDescriptionOverridesByPath(context.Context, string) ([]db.DescriptionOverride, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) SaveDescriptionOverride(context.Context, db.DescriptionOverride) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) DeleteDescriptionOverride(context.Context, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListHistoryEntries(context.Context) ([]db.HistoryEntry, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) ListUploadHistoryByPath(context.Context, string) ([]db.UploadRecord, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) ListPendingUploads(context.Context) ([]db.UploadRecord, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) CreateUploadRecord(context.Context, db.UploadRecord) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) UpdateLatestUploadRecordStatus(context.Context, string, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) SaveTrackerRuleFailures(context.Context, string, string, []db.TrackerRuleFailure) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListTrackerRuleFailuresByPath(context.Context, string) ([]db.TrackerRuleFailure, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) GetTrackerTimestamp(context.Context, string) (time.Time, error) {
	return time.Time{}, internalerrors.ErrNotImplemented
}

func (stubRepo) SaveTrackerTimestamp(context.Context, db.TrackerTimestamp) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) SaveTrackerMetadata(context.Context, db.TrackerMetadata) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListTrackerMetadataByPath(context.Context, string) ([]db.TrackerMetadata, error) {
	return nil, nil
}

func (stubRepo) SaveScreenshot(context.Context, db.Screenshot) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListScreenshotsByPath(context.Context, string) ([]db.Screenshot, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) DeleteScreenshot(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) SaveFinalSelections(context.Context, string, []db.ScreenshotFinalSelection) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListFinalSelections(context.Context, string) ([]db.ScreenshotFinalSelection, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) DeleteFinalSelection(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}
func (stubRepo) ReplaceScreenshotSlots(context.Context, string, []db.ScreenshotSlot) error {
	return internalerrors.ErrNotImplemented
}
func (stubRepo) ListScreenshotSlotsByPath(context.Context, string) ([]db.ScreenshotSlot, error) {
	return nil, internalerrors.ErrNotImplemented
}
func (stubRepo) UpsertScreenshotSlotVariants(context.Context, string, []db.ScreenshotSlotVariant) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) SaveUploadedImages(context.Context, string, string, []db.UploadedImageLink) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListUploadedImagesByPath(context.Context, string) ([]db.UploadedImageLink, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) DeleteUploadedImage(context.Context, string, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) GetPlaylistSelection(context.Context, string) (db.PlaylistSelection, error) {
	return db.PlaylistSelection{}, internalerrors.ErrNotImplemented
}

func (stubRepo) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) DeletePlaylistSelection(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (stubRepo) ListStoredReleasePaths(context.Context) ([]string, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) PurgeContentData(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

type trackerRepo struct{ stubRepo }

func (trackerRepo) ListTrackerMetadataByPath(context.Context, string) ([]db.TrackerMetadata, error) {
	return []db.TrackerMetadata{{
		SourcePath: "/tmp/a",
		Tracker:    "BLU",
		TrackerID:  "123",
		InfoHash:   "hash123",
		Matched:    true,
	}}, nil
}

type recordingRepo struct {
	saved       []db.FileMetadata
	purgeCalls  int
	purgedPaths []string
}

func (r *recordingRepo) GetByPath(context.Context, string) (db.FileMetadata, error) {
	return db.FileMetadata{}, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) Save(_ context.Context, metadata db.FileMetadata) error {
	r.saved = append(r.saved, metadata)
	return nil
}

func (r *recordingRepo) GetExternalIDs(context.Context, string) (db.ExternalIDs, error) {
	return db.ExternalIDs{}, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveExternalIDs(context.Context, db.ExternalIDs) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) GetExternalMetadata(context.Context, string) (db.ExternalMetadata, error) {
	return db.ExternalMetadata{}, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveExternalMetadata(context.Context, db.ExternalMetadata) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) GetDVDMediaInfo(context.Context, string) (db.DVDMediaInfo, error) {
	return db.DVDMediaInfo{}, internalerrors.ErrNotFound
}

func (r *recordingRepo) SaveDVDMediaInfo(context.Context, db.DVDMediaInfo) error {
	return nil
}

func (r *recordingRepo) GetReleaseNameOverrides(context.Context, string) (db.ReleaseNameOverrides, error) {
	return db.ReleaseNameOverrides{}, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveReleaseNameOverrides(context.Context, string, db.ReleaseNameOverrides) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) DeleteReleaseNameOverrides(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) GetDescriptionOverride(context.Context, string, string) (db.DescriptionOverride, error) {
	return db.DescriptionOverride{}, internalerrors.ErrNotImplemented
}
func (r *recordingRepo) ListDescriptionOverridesByPath(context.Context, string) ([]db.DescriptionOverride, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveDescriptionOverride(context.Context, db.DescriptionOverride) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) DeleteDescriptionOverride(context.Context, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListHistoryEntries(context.Context) ([]db.HistoryEntry, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListUploadHistoryByPath(context.Context, string) ([]db.UploadRecord, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListPendingUploads(context.Context) ([]db.UploadRecord, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) CreateUploadRecord(context.Context, db.UploadRecord) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) UpdateLatestUploadRecordStatus(context.Context, string, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveTrackerRuleFailures(context.Context, string, string, []db.TrackerRuleFailure) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListTrackerRuleFailuresByPath(context.Context, string) ([]db.TrackerRuleFailure, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) GetTrackerTimestamp(context.Context, string) (time.Time, error) {
	return time.Time{}, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveTrackerTimestamp(context.Context, db.TrackerTimestamp) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveTrackerMetadata(context.Context, db.TrackerMetadata) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListTrackerMetadataByPath(context.Context, string) ([]db.TrackerMetadata, error) {
	return nil, nil
}

func (r *recordingRepo) SaveScreenshot(context.Context, db.Screenshot) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListScreenshotsByPath(context.Context, string) ([]db.Screenshot, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) DeleteScreenshot(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveFinalSelections(context.Context, string, []db.ScreenshotFinalSelection) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListFinalSelections(context.Context, string) ([]db.ScreenshotFinalSelection, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) DeleteFinalSelection(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}
func (r *recordingRepo) ReplaceScreenshotSlots(context.Context, string, []db.ScreenshotSlot) error {
	return internalerrors.ErrNotImplemented
}
func (r *recordingRepo) ListScreenshotSlotsByPath(context.Context, string) ([]db.ScreenshotSlot, error) {
	return nil, internalerrors.ErrNotImplemented
}
func (r *recordingRepo) UpsertScreenshotSlotVariants(context.Context, string, []db.ScreenshotSlotVariant) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SaveUploadedImages(context.Context, string, string, []db.UploadedImageLink) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListUploadedImagesByPath(context.Context, string) ([]db.UploadedImageLink, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) DeleteUploadedImage(context.Context, string, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) GetPlaylistSelection(context.Context, string) (db.PlaylistSelection, error) {
	return db.PlaylistSelection{}, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) DeletePlaylistSelection(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingRepo) ListStoredReleasePaths(context.Context) ([]string, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (r *recordingRepo) PurgeContentData(_ context.Context, path string) error {
	r.purgeCalls++
	r.purgedPaths = append(r.purgedPaths, path)
	return nil
}

type stubFS struct {
	normalized []string
	err        error
}

func (s stubFS) ValidatePaths(_ context.Context, paths []string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.normalized != nil {
		return append([]string(nil), s.normalized...), nil
	}
	return paths, nil
}

type stubMeta struct {
	calls        int
	refreshCalls int
	resolveCalls int
	options      api.UploadOptions
	prepared     api.PreparedMetadata
	resolved     api.PreparedMetadata
}

func (s *stubMeta) Prepare(_ context.Context, req api.Request) (api.PreparedMetadata, error) {
	s.calls++
	s.options = req.Options
	meta := api.PreparedMetadata{SourcePath: req.Paths[0], Paths: req.Paths, Mode: req.Mode, Options: req.Options}
	if s.prepared.SourcePath == "" {
		s.prepared.SourcePath = req.Paths[0]
	}
	if len(s.prepared.Paths) == 0 {
		s.prepared.Paths = req.Paths
	}
	if s.prepared.Mode == "" {
		s.prepared.Mode = req.Mode
	}
	if s.prepared.Options == (api.UploadOptions{}) {
		s.prepared.Options = req.Options
	}
	if s.prepared.SourcePath != "" {
		meta = s.prepared
	}
	return meta, nil
}

func (s *stubMeta) RefreshPreparedMetadata(_ context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	s.refreshCalls++
	return meta, nil
}

func (s *stubMeta) EnrichTrackerData(_ context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	return meta, nil
}

func (s *stubMeta) ApplyMediaInfoIDs(_ context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	return meta, nil
}

func (s *stubMeta) ApplyArrData(_ context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	return meta, nil
}

func (s *stubMeta) ResolveExternalIDs(_ context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	s.resolveCalls++
	if strings.TrimSpace(s.resolved.SourcePath) != "" {
		return s.resolved, nil
	}
	return meta, nil
}

func (s *stubMeta) ApplyMediaDetails(_ context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	return meta, nil
}

type recordingScreenshots struct {
	calls      int
	lastMeta   api.PreparedMetadata
	lastImages []api.ScreenshotImage
}

func (r *recordingScreenshots) Plan(context.Context, api.PreparedMetadata, int) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{}, internalerrors.ErrNotImplemented
}

func (r *recordingScreenshots) Capture(context.Context, api.PreparedMetadata, []api.ScreenshotSelection, api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	return api.ScreenshotResult{}, internalerrors.ErrNotImplemented
}

func (r *recordingScreenshots) PreviewFrame(context.Context, api.PreparedMetadata, float64) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, internalerrors.ErrNotImplemented
}

func (r *recordingScreenshots) Delete(context.Context, api.PreparedMetadata, string) error {
	return internalerrors.ErrNotImplemented
}

func (r *recordingScreenshots) SaveFinalSelections(_ context.Context, meta api.PreparedMetadata, images []api.ScreenshotImage) error {
	r.calls++
	r.lastMeta = meta
	r.lastImages = images
	return nil
}

type stubTorrent struct{}

func (stubTorrent) Create(context.Context, api.PreparedMetadata) (api.TorrentResult, error) {
	return api.TorrentResult{Path: "/tmp/file.torrent"}, nil
}

type stubTorrentWithHash struct {
	hash string
}

func (s *stubTorrentWithHash) Create(context.Context, api.PreparedMetadata) (api.TorrentResult, error) {
	return api.TorrentResult{Path: "/tmp/file.torrent", InfoHash: s.hash}, nil
}

type stubClient struct {
	calls        int
	searchCalls  int
	searchResult api.ClientSearchResult
	searchErr    error
	injected     []api.TorrentResult
}

func (s *stubClient) Inject(_ context.Context, _ api.PreparedMetadata, torrent api.TorrentResult) error {
	s.calls++
	s.injected = append(s.injected, torrent)
	return nil
}

func (s *stubClient) SearchPathedTorrents(context.Context, api.PreparedMetadata) (api.ClientSearchResult, error) {
	s.searchCalls++
	if s.searchErr != nil {
		return api.ClientSearchResult{}, s.searchErr
	}
	return s.searchResult, nil
}

type stubDupes struct {
	lastTrackers []string
	lastMeta     api.PreparedMetadata
}

func (s *stubDupes) Check(_ context.Context, meta api.PreparedMetadata, trackers []string) (api.DupeCheckSummary, error) {
	s.lastMeta = meta
	s.lastTrackers = append([]string{}, trackers...)
	return api.DupeCheckSummary{}, nil
}

type stubTrackers struct {
	calls       int
	dryRunCalls int
	lastMeta    api.PreparedMetadata
	summary     api.UploadSummary
}

func (s *stubTrackers) Upload(_ context.Context, meta api.PreparedMetadata) (api.UploadSummary, error) {
	s.calls++
	s.lastMeta = meta
	if s.summary.Uploaded == 0 {
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{
				{
					Tracker:     "AITHER",
					TorrentID:   "1234",
					DownloadURL: "https://aither.cc/torrent/download/1234",
					TorrentURL:  "https://aither.cc/torrents/1234",
				},
			},
		}, nil
	}
	return s.summary, nil
}

func (s *stubTrackers) BuildPreparation(context.Context, api.PreparedMetadata, []string) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (s *stubTrackers) BuildUploadDryRun(_ context.Context, meta api.PreparedMetadata, _ []string) ([]api.TrackerDryRunEntry, error) {
	s.dryRunCalls++
	s.lastMeta = meta
	return []api.TrackerDryRunEntry{}, nil
}
