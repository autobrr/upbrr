// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubRepo struct {
	trackerRecords      []api.TrackerMetadata
	trackerRecordsErr   error
	trackerRecordsCalls int
	selections          []api.ScreenshotFinalSelection
	selectionsErr       error
	selectionsCalls     int
	screenshotSlots     []api.ScreenshotSlot
	screenshotSlotsErr  error
	screenshotSlotCalls int
	uploads             []api.UploadedImageLink
	uploadsErr          error
	uploadsCalls        int
	deletedUploads      []string
	createdUploads      []api.UploadRecord
	descriptionOverride string
	overrideCalls       int
}

func (s *stubRepo) GetByPath(context.Context, string) (api.FileMetadata, error) {
	return api.FileMetadata{}, nil
}
func (s *stubRepo) Save(context.Context, api.FileMetadata) error { return nil }
func (s *stubRepo) GetExternalIDs(context.Context, string) (api.ExternalIDs, error) {
	return api.ExternalIDs{}, nil
}
func (s *stubRepo) SaveExternalIDs(context.Context, api.ExternalIDs) error { return nil }
func (s *stubRepo) GetExternalMetadata(context.Context, string) (api.ExternalMetadata, error) {
	return api.ExternalMetadata{}, nil
}
func (s *stubRepo) SaveExternalMetadata(context.Context, api.ExternalMetadata) error { return nil }
func (s *stubRepo) GetDVDMediaInfo(context.Context, string) (api.DVDMediaInfo, error) {
	return api.DVDMediaInfo{}, internalerrors.ErrNotFound
}
func (s *stubRepo) SaveDVDMediaInfo(context.Context, api.DVDMediaInfo) error { return nil }
func (s *stubRepo) GetReleaseNameOverrides(context.Context, string) (api.ReleaseNameOverrides, error) {
	return api.ReleaseNameOverrides{}, nil
}
func (s *stubRepo) SaveReleaseNameOverrides(context.Context, string, api.ReleaseNameOverrides) error {
	return nil
}
func (s *stubRepo) DeleteReleaseNameOverrides(context.Context, string) error { return nil }
func (s *stubRepo) GetDescriptionOverride(context.Context, string) (api.DescriptionOverride, error) {
	s.overrideCalls++
	if s.descriptionOverride == "" {
		return api.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	return api.DescriptionOverride{SourcePath: "/tmp/source", Description: s.descriptionOverride}, nil
}
func (s *stubRepo) SaveDescriptionOverride(context.Context, api.DescriptionOverride) error {
	return nil
}
func (s *stubRepo) DeleteDescriptionOverride(context.Context, string) error { return nil }
func (s *stubRepo) ListHistoryEntries(context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}
func (s *stubRepo) ListUploadHistoryByPath(context.Context, string) ([]api.UploadRecord, error) {
	return nil, nil
}
func (s *stubRepo) ListPendingUploads(context.Context) ([]api.UploadRecord, error) {
	return nil, nil
}
func (s *stubRepo) CreateUploadRecord(_ context.Context, record api.UploadRecord) error {
	s.createdUploads = append(s.createdUploads, record)
	return nil
}
func (s *stubRepo) UpdateLatestUploadRecordStatus(context.Context, string, string, string) error {
	return nil
}
func (s *stubRepo) SaveTrackerRuleFailures(context.Context, string, string, []api.TrackerRuleFailure) error {
	return nil
}
func (s *stubRepo) ListTrackerRuleFailuresByPath(context.Context, string) ([]api.TrackerRuleFailure, error) {
	return nil, nil
}
func (s *stubRepo) GetTrackerTimestamp(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
func (s *stubRepo) SaveTrackerTimestamp(context.Context, api.TrackerTimestamp) error { return nil }
func (s *stubRepo) SaveTrackerMetadata(context.Context, api.TrackerMetadata) error   { return nil }
func (s *stubRepo) ListTrackerMetadataByPath(context.Context, string) ([]api.TrackerMetadata, error) {
	s.trackerRecordsCalls++
	if s.trackerRecordsErr != nil {
		return nil, s.trackerRecordsErr
	}
	return s.trackerRecords, nil
}
func (s *stubRepo) SaveScreenshot(context.Context, api.Screenshot) error { return nil }
func (s *stubRepo) ListScreenshotsByPath(context.Context, string) ([]api.Screenshot, error) {
	return nil, nil
}
func (s *stubRepo) DeleteScreenshot(context.Context, string) error { return nil }
func (s *stubRepo) SaveFinalSelections(context.Context, string, []api.ScreenshotFinalSelection) error {
	return nil
}
func (s *stubRepo) ListFinalSelections(context.Context, string) ([]api.ScreenshotFinalSelection, error) {
	s.selectionsCalls++
	if s.selectionsErr != nil {
		return nil, s.selectionsErr
	}
	return s.selections, nil
}
func (s *stubRepo) DeleteFinalSelection(context.Context, string) error { return nil }
func (s *stubRepo) ReplaceScreenshotSlots(_ context.Context, _ string, slots []api.ScreenshotSlot) error {
	s.screenshotSlots = append([]api.ScreenshotSlot(nil), slots...)
	return nil
}
func (s *stubRepo) ListScreenshotSlotsByPath(context.Context, string) ([]api.ScreenshotSlot, error) {
	s.screenshotSlotCalls++
	if s.screenshotSlotsErr != nil {
		return nil, s.screenshotSlotsErr
	}
	return s.screenshotSlots, nil
}
func (s *stubRepo) UpsertScreenshotSlotVariants(context.Context, string, []api.ScreenshotSlotVariant) error {
	return nil
}
func (s *stubRepo) SaveUploadedImages(context.Context, string, string, []api.UploadedImageLink) error {
	return nil
}
func (s *stubRepo) ListUploadedImagesByPath(context.Context, string) ([]api.UploadedImageLink, error) {
	s.uploadsCalls++
	if s.uploadsErr != nil {
		return nil, s.uploadsErr
	}
	return s.uploads, nil
}
func (s *stubRepo) DeleteUploadedImage(_ context.Context, _ string, imagePath string, host string) error {
	s.deletedUploads = append(s.deletedUploads, host+":"+imagePath)
	return nil
}
func (s *stubRepo) GetPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, nil
}
func (s *stubRepo) SavePlaylistSelection(context.Context, string, []string, bool) error { return nil }
func (s *stubRepo) DeletePlaylistSelection(context.Context, string) error               { return nil }
func (s *stubRepo) ListStoredReleasePaths(context.Context) ([]string, error)            { return nil, nil }
func (s *stubRepo) PurgeContentData(context.Context, string) error                      { return nil }

type stubImageService struct {
	uploads map[string][]api.UploadedImageLink
	errs    map[string]error
}

func (s *stubImageService) ListCandidates(context.Context, api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *stubImageService) Upload(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	if err := s.errs[host]; err != nil {
		return nil, err
	}
	if links, ok := s.uploads[host]; ok {
		for idx := range links {
			if strings.TrimSpace(links[idx].UsageScope) == "" {
				links[idx].UsageScope = usageScope
			}
		}
		return links, nil
	}
	results := make([]api.UploadedImageLink, 0, len(images))
	for idx, image := range images {
		results = append(results, api.UploadedImageLink{
			SourcePath: meta.SourcePath,
			ImagePath:  image.Path,
			Host:       host,
			UsageScope: usageScope,
			ImgURL:     fmt.Sprintf("https://%s/%d.png", host, idx),
			RawURL:     fmt.Sprintf("https://%s/%d.png", host, idx),
			WebURL:     fmt.Sprintf("https://%s/%d", host, idx),
		})
	}
	return results, nil
}

func TestResolveDescriptionAssetsPrefersDBDescription(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", TrackerData: []api.TrackerMetadata{{Tracker: "AITHER", Description: "meta desc"}}}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "db desc\n\nmeta desc" {
		t.Fatalf("expected combined description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsUsesOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "override desc" {
		t.Fatalf("expected override description, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected override flag to be true")
	}
}

func TestResolveDescriptionAssetsStripsEmbeddedNFOBlocksFromOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]\n\nCustom body",
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "ANT", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(assets.Description, "scene nfo") {
		t.Fatalf("expected embedded nfo removed, got %q", assets.Description)
	}
	if assets.Description != "Custom body" {
		t.Fatalf("expected cleaned override description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsSelectsMostCommonHost(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", ImgURL: "https://imgbb.com/a.png"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", ImgURL: "https://imgbb.com/b.png"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "ptpimg", ImgURL: "https://ptpimg.me/a.png"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].Path != "/tmp/a.png" || assets.Screenshots[1].Path != "/tmp/b.png" {
		t.Fatalf("unexpected order: %#v", assets.Screenshots)
	}
	if assets.Screenshots[0].Host != "imgbb" || assets.Screenshots[1].Host != "imgbb" {
		t.Fatalf("expected imgbb host, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsFallbackTrackerImages(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png", "https://ptpimg.me/c.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 3 {
		t.Fatalf("expected 3 screenshots, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].ImgURL != "https://imgbb.com/a.png" || assets.Screenshots[1].ImgURL != "https://imgbb.com/b.png" || assets.Screenshots[2].ImgURL != "https://ptpimg.me/c.png" {
		t.Fatalf("unexpected screenshot urls: %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsSkipsTMDBTrackerImages(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://image.tmdb.org/t/p/original/poster.jpg", "https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected tmdb image to be skipped, got %#v", assets.Screenshots)
	}
	for _, screenshot := range assets.Screenshots {
		if strings.Contains(strings.ToLower(screenshot.ImgURL), "tmdb.org") {
			t.Fatalf("expected tmdb images to be filtered, got %#v", assets.Screenshots)
		}
	}
}

func TestResolveDescriptionAssetsFallbackOtherTrackerDescription(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{Tracker: "ULCX", Description: "ulcx desc"}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "ulcx desc" {
		t.Fatalf("expected fallback description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsPrefersMatchingTrackerDescription(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "BHD", Description: "[align=center]bhd[/align]"},
			{Tracker: "AITHER", Description: "[center]unit3d[/center]"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "[center]unit3d[/center]" {
		t.Fatalf("expected tracker-specific description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsStripsEmbeddedNFOBlocksFromTrackerDescriptions(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "ANT", Description: "[hide=FraMeSToR NFO:][pre]frame nfo[/pre][/hide]\n\nTracker body"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "ANT", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(assets.Description, "frame nfo") {
		t.Fatalf("expected tracker nfo block removed, got %q", assets.Description)
	}
	if assets.Description != "Tracker body" {
		t.Fatalf("expected cleaned tracker description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsStripsDefaultSignatureForANT(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "ANT", Description: "[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]\n\nBody"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "ANT", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(assets.Description, "upbrr") {
		t.Fatalf("expected default signature removed for ANT, got %q", assets.Description)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected cleaned ANT description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsStripsDefaultSignatureForNBL(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "NBL", Description: "[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]\n\nBody"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "NBL", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(assets.Description, "upbrr") {
		t.Fatalf("expected default signature removed for NBL, got %q", assets.Description)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected cleaned NBL description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsFallbackOtherTrackerImages(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "ULCX",
			ImageURLs: []string{"https://imgbb.com/a.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected 1 screenshot, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].ImgURL != "https://imgbb.com/a.png" {
		t.Fatalf("unexpected screenshot url: %#v", assets.Screenshots[0])
	}
}

func TestResolveDescriptionAssetsIgnoresTrackerScopedUploadsForOtherTrackers(t *testing.T) {
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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(assets.Screenshots))
	}
	for _, screenshot := range assets.Screenshots {
		if screenshot.Host != "imgbb" {
			t.Fatalf("expected global imgbb screenshots, got %#v", assets.Screenshots)
		}
	}
}

func TestResolveDescriptionAssetsPrefersTrackerScopedUploadsForMatchingTracker(t *testing.T) {
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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(assets.Screenshots))
	}
	for _, screenshot := range assets.Screenshots {
		if screenshot.Host != "hdb" {
			t.Fatalf("expected tracker-scoped hdb screenshots, got %#v", assets.Screenshots)
		}
	}
}

func TestResolveDescriptionAssetsDegradesGracefullyOnScreenshotReadFailure(t *testing.T) {
	repo := &stubRepo{
		selectionsErr: errors.New("database is locked"),
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("expected graceful degradation, got %v", err)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected tracker url fallback screenshots, got %d", len(assets.Screenshots))
	}
}

func TestResolveDescriptionAssetsDegradesGracefullyOnSelectedUploadMismatch(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", ImgURL: "https://imgbb.com/a.png", RawURL: "https://imgbb.com/a.png", WebURL: "https://imgbb.com/a"},
		},
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://ptpimg.me/fallback-a.png", "https://ptpimg.me/fallback-b.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("expected graceful degradation, got %v", err)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected tracker url fallback screenshots, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].ImgURL != "https://ptpimg.me/fallback-a.png" {
		t.Fatalf("expected fallback screenshot urls, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsBackfillsSlotsFromDescriptionOrder(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: strings.TrimSpace(`
[center][img]https://imgbb.com/first.png[/img][/center]
Some text
[comparison=A,B]https://ptpimg.me/second.png https://ptpimg.me/third.png[/comparison]
`),
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.screenshotSlots) != 3 {
		t.Fatalf("expected 3 persisted screenshot slots, got %d", len(repo.screenshotSlots))
	}
	if len(assets.Screenshots) != 3 {
		t.Fatalf("expected 3 screenshots, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].ImgURL != "https://imgbb.com/first.png" {
		t.Fatalf("expected first description image first, got %#v", assets.Screenshots)
	}
	if assets.Screenshots[1].ImgURL != "https://ptpimg.me/second.png" || assets.Screenshots[2].ImgURL != "https://ptpimg.me/third.png" {
		t.Fatalf("expected comparison images in source order, got %#v", assets.Screenshots)
	}
}

func TestResolveTrackerScreenshotsReturnsNilWhenHostsAreInvalid(t *testing.T) {
	screenshots := resolveTrackerScreenshots([]string{
		"not a url",
		"https://",
		"   ",
	})
	if len(screenshots) != 0 {
		t.Fatalf("expected no screenshots for invalid urls, got %#v", screenshots)
	}
}

func TestEnsureDescriptionImageHostReusesAllowedHost(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "ptpimg", ImgURL: "https://ptpimg/a.png", RawURL: "https://ptpimg/a.png", WebURL: "https://ptpimg/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "ptpimg", ImgURL: "https://ptpimg/b.png", RawURL: "https://ptpimg/b.png", WebURL: "https://ptpimg/b"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHost(context.Background(), "OE", meta, config.Config{}, config.TrackerConfig{}, repo, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.SelectedHost != "ptpimg" {
		t.Fatalf("expected ptpimg host, got %q", resolution.feedback.SelectedHost)
	}
	if resolution.feedback.Reuploaded {
		t.Fatal("expected screenshots to be reused")
	}
}

func TestEnsureDescriptionImageHostReuploadsForRequiredTracker(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, &stubImageService{}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.SelectedHost != "ptpimg" {
		t.Fatalf("expected ptpimg host, got %q", resolution.feedback.SelectedHost)
	}
	if !resolution.feedback.Reuploaded {
		t.Fatal("expected screenshots to be reuploaded")
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(resolution.screenshots))
	}
	for _, screenshot := range resolution.screenshots {
		if screenshot.Host != "ptpimg" {
			t.Fatalf("expected all rehosted screenshots to use ptpimg, got %#v", resolution.screenshots)
		}
	}
}

func TestEnsureDescriptionImageHostUsesPreferredOverrideWhenAllowed(t *testing.T) {
	preferredHost := "imgbb"
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "ptpimg", ImgURL: "https://ptpimg/a.png", RawURL: "https://ptpimg/a.png", WebURL: "https://ptpimg/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "ptpimg", ImgURL: "https://ptpimg/b.png", RawURL: "https://ptpimg/b.png", WebURL: "https://ptpimg/b"},
		},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		ImageHostOverrides: api.ImageHostOverrides{
			PreferredHost: &preferredHost,
		},
	}

	resolution, err := ensureDescriptionImageHost(context.Background(), "OE", meta, config.Config{}, config.TrackerConfig{}, repo, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected preferred allowed host imgbb, got %q", resolution.feedback.SelectedHost)
	}
}

func TestEnsureDescriptionImageHostReusesGlobalUploadsInsteadOfOtherTrackerScope(t *testing.T) {
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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHost(context.Background(), "OE", meta, config.Config{}, config.TrackerConfig{}, repo, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected global imgbb host, got %q", resolution.feedback.SelectedHost)
	}
	for _, screenshot := range resolution.screenshots {
		if screenshot.Host != "imgbb" {
			t.Fatalf("expected imgbb screenshots, got %#v", resolution.screenshots)
		}
	}
}

func TestEnsureDescriptionImageHostSkipsAutomaticUploadWhenDisabled(t *testing.T) {
	skipUpload := true
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "imgbb", ImgURL: "https://imgbb/a.png", RawURL: "https://imgbb/a.png", WebURL: "https://imgbb/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "imgbb", ImgURL: "https://imgbb/b.png", RawURL: "https://imgbb/b.png", WebURL: "https://imgbb/b"},
		},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		ImageHostOverrides: api.ImageHostOverrides{
			SkipUpload: &skipUpload,
		},
	}

	resolution, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, &stubImageService{}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.Status != "warning" {
		t.Fatalf("expected warning status, got %#v", resolution.feedback)
	}
	if resolution.feedback.Reuploaded {
		t.Fatal("expected automatic upload to stay disabled")
	}
	if len(resolution.screenshots) != 0 {
		t.Fatalf("expected no rehosted screenshots, got %#v", resolution.screenshots)
	}
	if !strings.Contains(resolution.feedback.Message, "disabled") {
		t.Fatalf("expected disabled message, got %q", resolution.feedback.Message)
	}
}

func TestEnsureDescriptionImageHostErrorsOnMissingSelectedUpload(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "ptpimg", ImgURL: "https://ptpimg/a.png", RawURL: "https://ptpimg/a.png", WebURL: "https://ptpimg/a"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	_, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, nil, api.NopLogger{})
	if err == nil {
		t.Fatal("expected error for missing selected screenshot upload")
	}
	if !strings.Contains(err.Error(), "/tmp/b.png") {
		t.Fatalf("expected missing image path in error, got %v", err)
	}
}

func TestEnsureDescriptionImageHostRollsBackUploadedImagesOnSelectionError(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	images := &stubImageService{
		uploads: map[string][]api.UploadedImageLink{
			"ptpimg": {
				{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "ptpimg", ImgURL: "https://ptpimg/a.png", RawURL: "https://ptpimg/a.png", WebURL: "https://ptpimg/a"},
			},
		},
	}

	_, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, images, api.NopLogger{})
	if err == nil {
		t.Fatal("expected selection error after upload")
	}
	if len(repo.deletedUploads) != 1 {
		t.Fatalf("expected one uploaded image rollback, got %#v", repo.deletedUploads)
	}
	if repo.deletedUploads[0] != "ptpimg:/tmp/a.png" {
		t.Fatalf("unexpected rollback target: %#v", repo.deletedUploads)
	}
}
