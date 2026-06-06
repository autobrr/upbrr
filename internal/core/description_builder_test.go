// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubDescriptionBuilderTrackers struct {
	called      bool
	prepareMeta api.PreparedMetadata
	preview     api.PreparationPreview
	dryRunMeta  api.PreparedMetadata
	uploadMeta  api.PreparedMetadata
	dryRunItems []api.TrackerDryRunEntry
}

func (s *stubDescriptionBuilderTrackers) Upload(_ context.Context, meta api.PreparedMetadata) (api.UploadSummary, error) {
	s.uploadMeta = meta
	return api.UploadSummary{Uploaded: 1}, nil
}

func (s *stubDescriptionBuilderTrackers) BuildPreparation(_ context.Context, meta api.PreparedMetadata, trackers []string) (api.PreparationPreview, error) {
	s.called = true
	s.prepareMeta = meta
	if strings.TrimSpace(s.preview.SourcePath) == "" {
		s.preview.SourcePath = meta.SourcePath
	}
	if len(s.preview.Descriptions) == 0 {
		s.preview.Descriptions = []api.PreparationDescription{
			{Trackers: trackers, RawDescription: meta.DescriptionTemplate, RawDescriptionHTML: "<p>ok</p>"},
		}
	}
	return s.preview, nil
}

func (s *stubDescriptionBuilderTrackers) BuildUploadDryRun(_ context.Context, meta api.PreparedMetadata, _ []string) ([]api.TrackerDryRunEntry, error) {
	s.dryRunMeta = meta
	if len(s.dryRunItems) == 0 {
		return []api.TrackerDryRunEntry{}, nil
	}
	return append([]api.TrackerDryRunEntry(nil), s.dryRunItems...), nil
}

type stubDescriptionRepo struct {
	stubRepo
	override db.DescriptionOverride
	getErr   error
	saved    []db.DescriptionOverride
	deleted  []string
}

func (s *stubDescriptionRepo) GetDescriptionOverride(_ context.Context, _ string, groupKey string) (db.DescriptionOverride, error) {
	if s.getErr != nil {
		return db.DescriptionOverride{}, s.getErr
	}
	if strings.TrimSpace(groupKey) != strings.TrimSpace(s.override.GroupKey) {
		return db.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	if strings.TrimSpace(s.override.Description) == "" {
		return db.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	return s.override, nil
}

func (s *stubDescriptionRepo) ListDescriptionOverridesByPath(context.Context, string) ([]db.DescriptionOverride, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if strings.TrimSpace(s.override.Description) == "" {
		return nil, internalerrors.ErrNotFound
	}
	return []db.DescriptionOverride{s.override}, nil
}

func (s *stubDescriptionRepo) SaveDescriptionOverride(_ context.Context, override db.DescriptionOverride) error {
	s.saved = append(s.saved, override)
	return nil
}

func (s *stubDescriptionRepo) DeleteDescriptionOverride(_ context.Context, path string, groupKey string) error {
	s.deleted = append(s.deleted, path+"|"+groupKey)
	return nil
}

func TestFetchDescriptionBuilderPreviewUsesOverride(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{override: db.DescriptionOverride{SourcePath: "/tmp/source", GroupKey: "aither", Description: "override desc"}}
	trackerSvc := &stubPreparationTrackers{}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{Paths: []string{"/tmp/source"}, Mode: api.ModeGUI})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(preview.Groups) != 1 {
		t.Fatalf("expected one override group, got %d", len(preview.Groups))
	}
	if preview.Groups[0].RawDescription != "override desc" {
		t.Fatalf("expected override description, got %q", preview.Groups[0].RawDescription)
	}
	if !preview.Groups[0].HasOverride {
		t.Fatalf("expected override flag to be true")
	}
	if trackerSvc.called {
		t.Fatalf("expected tracker service not called when override exists")
	}
}

func TestFetchDescriptionBuilderPreviewDoesNotApplyLegacyDefaultOverrideAcrossGroups(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{override: db.DescriptionOverride{SourcePath: "/tmp/source", Description: "legacy default desc"}}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{GroupKey: "hdb", Trackers: []string{"HDB"}, RawDescription: "generated raw", RawDescriptionHTML: "<p>generated raw</p>"},
			},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:   []string{"/tmp/source"},
		Mode:    api.ModeGUI,
		Options: api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(preview.Groups) != 1 {
		t.Fatalf("expected one description group, got %d", len(preview.Groups))
	}
	if preview.Groups[0].RawDescription != "generated raw" {
		t.Fatalf("expected group-specific generated raw description, got %q", preview.Groups[0].RawDescription)
	}
	if preview.Groups[0].HasOverride {
		t.Fatalf("expected legacy default override not to apply implicitly")
	}
}

func TestFetchDescriptionBuilderPreviewFallsBackToPrepareInGUI(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubPreparationTrackers{}
	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:   []string{"/tmp/source"},
		Mode:    api.ModeGUI,
		Options: api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if metaSvc.calls != 1 {
		t.Fatalf("expected metadata prepare to be called once, got %d", metaSvc.calls)
	}
	if !trackerSvc.called {
		t.Fatalf("expected tracker preparation to be called")
	}
	if preview.SourcePath != "/tmp/source" {
		t.Fatalf("expected source path to be set, got %q", preview.SourcePath)
	}
}

func TestFetchDescriptionBuilderPreviewRefreshesMissingLogoMetadata(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubDescriptionBuilderTrackers{}
	prepared := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		Paths:      []string{"/tmp/source"},
		Mode:       api.ModeGUI,
		ExternalIDs: api.ExternalIDs{
			SourcePath: "/tmp/source",
			TMDBID:     42,
			Category:   "MOVIE",
		},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "/tmp/source",
			TMDB:       &api.TMDBMetadata{TMDBID: 42, Title: "Cached without logo"},
		},
	}
	refreshed := prepared
	refreshed.ExternalMetadata.TMDB = &api.TMDBMetadata{TMDBID: 42, Logo: "https://image.tmdb.org/t/p/original/logo.png"}
	metaSvc := &stubMeta{prepared: prepared, resolved: refreshed}
	core := &Core{
		cfg: config.Config{
			Description:        config.DescriptionSettingsConfig{AddLogo: true},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	_, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:   []string{"/tmp/source"},
		Mode:    api.ModeGUI,
		Options: api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if metaSvc.resolveCalls != 1 {
		t.Fatalf("expected description builder to refresh missing logo metadata, got %d calls", metaSvc.resolveCalls)
	}
	if trackerSvc.prepareMeta.ExternalMetadata.TMDB == nil || trackerSvc.prepareMeta.ExternalMetadata.TMDB.Logo == "" {
		t.Fatalf("expected tracker preparation to receive refreshed logo metadata, got %#v", trackerSvc.prepareMeta.ExternalMetadata.TMDB)
	}
}

func TestSaveDescriptionOverrideDeletesOnEmpty(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{GroupKey: "blu", Trackers: []string{"BLU"}, RawDescription: "generated raw", RawDescriptionHTML: "<p>generated raw</p>"},
			},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	group, err := core.SaveDescriptionOverride(context.Background(), api.Request{
		Paths:                    []string{"/tmp/source"},
		Mode:                     api.ModeGUI,
		DescriptionOverrideGroup: "blu",
		Trackers:                 []string{"BLU"},
	}, "  ")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(repo.deleted) != 1 {
		t.Fatalf("expected delete to be called, got %d", len(repo.deleted))
	}
	if group.GroupKey != "blu" {
		t.Fatalf("expected reset group key, got %q", group.GroupKey)
	}
	if group.RawDescription != "generated raw" {
		t.Fatalf("expected generated raw description after reset, got %q", group.RawDescription)
	}
}

func TestSaveDescriptionOverrideDeleteReturnsEmptyGroupWhenPreviewGroupMissing(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath:   "/tmp/source",
			Descriptions: []api.PreparationDescription{},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	group, err := core.SaveDescriptionOverride(context.Background(), api.Request{
		Paths:                    []string{"/tmp/source"},
		Mode:                     api.ModeGUI,
		DescriptionOverrideGroup: "blu",
		Trackers:                 []string{"BLU"},
	}, "  ")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(repo.deleted) != 1 {
		t.Fatalf("expected delete to be called, got %d", len(repo.deleted))
	}
	if group.GroupKey != "blu" {
		t.Fatalf("expected reset group key, got %q", group.GroupKey)
	}
	if len(group.Trackers) != 1 || group.Trackers[0] != "BLU" {
		t.Fatalf("expected trackers to be preserved, got %v", group.Trackers)
	}
	if group.HasOverride {
		t.Fatalf("expected override flag to be false")
	}
	if group.RawDescription != "" {
		t.Fatalf("expected empty raw description when preview group missing, got %q", group.RawDescription)
	}
	if group.RawDescriptionHTML != "" {
		t.Fatalf("expected empty rendered description when preview group missing, got %q", group.RawDescriptionHTML)
	}
}

func TestRenderDescriptionReturnsHTML(t *testing.T) {
	t.Parallel()

	core := &Core{logger: api.NopLogger{}}
	value, err := core.RenderDescription(context.Background(), "[b]Example[/b]")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(value, "Example") {
		t.Fatalf("expected rendered HTML to contain content, got %q", value)
	}
}

func TestFetchDescriptionBuilderPreviewSkipsEmptyPreparationPlaceholder(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{Trackers: []string{"BTN"}, RawDescription: "", RawDescriptionHTML: ""},
				{Trackers: []string{"BLU"}, RawDescription: "final description", RawDescriptionHTML: "<p>final description</p>"},
			},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:   []string{"/tmp/source"},
		Mode:    api.ModeGUI,
		Options: api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(preview.Groups) != 2 {
		t.Fatalf("expected two description groups, got %d", len(preview.Groups))
	}
	if preview.Groups[1].RawDescription != "final description" {
		t.Fatalf("expected non-empty raw description to be selected, got %q", preview.Groups[1].RawDescription)
	}
	if !strings.Contains(preview.Groups[1].RawDescriptionHTML, "final description") {
		t.Fatalf("expected rendered raw description to contain content, got %q", preview.Groups[1].RawDescriptionHTML)
	}
}

func TestFetchDescriptionBuilderPreviewSeedsRawDescriptionFromBuiltGroupText(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{
					GroupKey:        "hdb",
					Trackers:        []string{"HDB"},
					RawDescription:  "",
					Description:     "built grouped text",
					DescriptionHTML: "<p>built grouped text</p>",
				},
			},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:   []string{"/tmp/source"},
		Mode:    api.ModeGUI,
		Options: api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(preview.Groups) != 1 {
		t.Fatalf("expected one description group, got %d", len(preview.Groups))
	}
	if preview.Groups[0].RawDescription != "built grouped text" {
		t.Fatalf("expected raw description to fall back to built grouped text, got %q", preview.Groups[0].RawDescription)
	}
	if !strings.Contains(preview.Groups[0].RawDescriptionHTML, "built grouped text") {
		t.Fatalf("expected rendered raw description to contain built grouped text, got %q", preview.Groups[0].RawDescriptionHTML)
	}
}

func TestFetchDescriptionBuilderPreviewAppliesIgnoredDupesToCachedMeta(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{GroupKey: "hdb", Trackers: []string{"HDB"}, Description: "hdb body"},
				{GroupKey: "bhd", Trackers: []string{"BHD"}, Description: "bhd body"},
			},
		},
	}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeRefreshedDupeCache("/tmp/source", "", api.PreparedMetadata{
		SourcePath: "/tmp/source",
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
			"BHD": {api.TrackerBlockReasonDupe},
		},
	})

	_, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:          []string{"/tmp/source"},
		Mode:           api.ModeGUI,
		Trackers:       []string{"HDB", "BHD"},
		IgnoreDupesFor: []string{"HDB"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := trackerSvc.prepareMeta.BlockedTrackers["HDB"]; ok {
		t.Fatalf("expected ignored HDB dupe block to be cleared, got %#v", trackerSvc.prepareMeta.BlockedTrackers)
	}
	if got := trackerSvc.prepareMeta.BlockedTrackers["BHD"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected BHD dupe block to remain, got %#v", trackerSvc.prepareMeta.BlockedTrackers)
	}
}

func TestFetchDescriptionBuilderPreviewPreservesFinalBuildWithOverrideCaseInsensitively(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{
		override: db.DescriptionOverride{
			SourcePath:  "/tmp/source",
			GroupKey:    "HDB|HDB|TRACKER:HDB",
			Description: "override body",
		},
	}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{
					GroupKey:    "hdb|hdb|tracker:hdb",
					Trackers:    []string{"HDB"},
					Description: "built override body with rehosted images",
					HasOverride: true,
				},
			},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	preview, err := core.FetchDescriptionBuilderPreview(context.Background(), api.Request{
		Paths:   []string{"/tmp/source"},
		Mode:    api.ModeGUI,
		Options: api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(preview.Groups) != 1 {
		t.Fatalf("expected one description group, got %d", len(preview.Groups))
	}
	if preview.Groups[0].GroupKey != "hdb|hdb|tracker:hdb" {
		t.Fatalf("expected normalized group key, got %q", preview.Groups[0].GroupKey)
	}
	if preview.Groups[0].RawDescription != "built override body with rehosted images" {
		t.Fatalf("expected final built override body, got %q", preview.Groups[0].RawDescription)
	}
	if !preview.Groups[0].HasOverride {
		t.Fatalf("expected override flag to be true")
	}
}

func TestFetchDescriptionBuilderGroupPreviewPreservesFinalBuildWithOverrideCaseInsensitively(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{
		override: db.DescriptionOverride{
			SourcePath:  "/tmp/source",
			GroupKey:    "HDB|HDB|TRACKER:HDB",
			Description: "override body",
		},
	}
	trackerSvc := &stubDescriptionBuilderTrackers{
		preview: api.PreparationPreview{
			SourcePath: "/tmp/source",
			Descriptions: []api.PreparationDescription{
				{
					GroupKey:    "hdb|hdb|tracker:hdb",
					Trackers:    []string{"HDB"},
					Description: "built override body with rehosted images",
					HasOverride: true,
				},
			},
		},
	}
	metaSvc := &stubMeta{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Trackers:   trackerSvc,
			Metadata:   metaSvc,
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	group, err := core.FetchDescriptionBuilderGroupPreview(context.Background(), api.Request{
		Paths:                    []string{"/tmp/source"},
		Mode:                     api.ModeGUI,
		Trackers:                 []string{"HDB"},
		DescriptionOverrideGroup: "HDB|HDB|TRACKER:HDB",
		Options:                  api.UploadOptions{Screens: 1},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if group.GroupKey != "hdb|hdb|tracker:hdb" {
		t.Fatalf("expected normalized group key, got %q", group.GroupKey)
	}
	if group.RawDescription != "built override body with rehosted images" {
		t.Fatalf("expected final built override body, got %q", group.RawDescription)
	}
	if !group.HasOverride {
		t.Fatalf("expected override flag to be true")
	}
}

func TestSaveDescriptionOverrideReturnsSavedGroup(t *testing.T) {
	t.Parallel()

	repo := &stubDescriptionRepo{}
	core := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
		},
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}

	group, err := core.SaveDescriptionOverride(context.Background(), api.Request{
		Paths:                    []string{"/tmp/source"},
		Mode:                     api.ModeGUI,
		DescriptionOverrideGroup: "hdb",
		Trackers:                 []string{"HDB"},
	}, "custom body")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if group.GroupKey != "hdb" {
		t.Fatalf("expected hdb group key, got %q", group.GroupKey)
	}
	if group.RawDescription != "custom body" {
		t.Fatalf("expected saved raw description, got %q", group.RawDescription)
	}
	if !group.HasOverride {
		t.Fatalf("expected override flag to be true")
	}
	if len(repo.saved) != 1 {
		t.Fatalf("expected save to be called once, got %d", len(repo.saved))
	}
}

func TestFetchTrackerDryRunPreviewUsesCanonicalDescriptionGroups(t *testing.T) {
	t.Parallel()

	trackerSvc := &stubDescriptionBuilderTrackers{
		dryRunItems: []api.TrackerDryRunEntry{{Tracker: "HDB", Status: "ok"}},
	}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Metadata:   &stubMeta{},
			Torrents:   stubTorrent{},
			Trackers:   trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	signature := overrideSignature(
		api.ExternalIDOverrides{},
		api.ReleaseNameOverrides{},
		api.MetadataOverrides{},
		api.TrackerConfigOverrides{},
		api.TrackerSiteOverrides{},
		api.ClientOverrides{},
		api.TorrentOverrides{},
		api.ImageHostOverrides{},
		api.ScreenshotOverrides{},
	)
	core.storeDupeCache("/tmp/source", signature, api.PreparedMetadata{SourcePath: "/tmp/source"})

	group := api.DescriptionBuilderGroup{
		GroupKey:       "hdb",
		Trackers:       []string{"HDB"},
		RawDescription: "saved canonical body",
	}
	preview, err := core.FetchTrackerDryRunPreview(context.Background(), api.Request{
		Paths:             []string{"/tmp/source"},
		Mode:              api.ModeGUI,
		Trackers:          []string{"HDB"},
		DescriptionGroups: []api.DescriptionBuilderGroup{group},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(preview.Trackers) != 1 {
		t.Fatalf("expected one dry-run tracker entry, got %d", len(preview.Trackers))
	}
	if len(trackerSvc.dryRunMeta.DescriptionGroups) != 1 {
		t.Fatalf("expected one canonical description group, got %d", len(trackerSvc.dryRunMeta.DescriptionGroups))
	}
	if trackerSvc.dryRunMeta.DescriptionGroups[0].RawDescription != "saved canonical body" {
		t.Fatalf("expected dry-run to use canonical description group, got %q", trackerSvc.dryRunMeta.DescriptionGroups[0].RawDescription)
	}
}

func TestExecutePreparedUploadUsesCanonicalDescriptionGroups(t *testing.T) {
	t.Parallel()

	trackerSvc := &stubDescriptionBuilderTrackers{}
	core := &Core{
		cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Torrents: stubTorrent{},
			Trackers: trackerSvc,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	group := api.DescriptionBuilderGroup{
		GroupKey:       "hdb",
		Trackers:       []string{"HDB"},
		RawDescription: "saved canonical upload body",
	}
	uploaded, err := core.executePreparedUpload(context.Background(), api.Request{
		Paths:             []string{"/tmp/source"},
		Mode:              api.ModeGUI,
		Trackers:          []string{"HDB"},
		DescriptionGroups: []api.DescriptionBuilderGroup{group},
	}, api.PreparedMetadata{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if uploaded != 1 {
		t.Fatalf("expected one upload, got %d", uploaded)
	}
	if len(trackerSvc.uploadMeta.DescriptionGroups) != 1 {
		t.Fatalf("expected one canonical upload description group, got %d", len(trackerSvc.uploadMeta.DescriptionGroups))
	}
	if trackerSvc.uploadMeta.DescriptionGroups[0].RawDescription != "saved canonical upload body" {
		t.Fatalf("expected upload to use canonical description group, got %q", trackerSvc.uploadMeta.DescriptionGroups[0].RawDescription)
	}
}
