// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubRepo struct {
	mu                  sync.Mutex
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
	overrideGroupKey    string
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
func (s *stubRepo) GetDescriptionOverride(_ context.Context, _ string, groupKey string) (api.DescriptionOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrideCalls++
	if s.descriptionOverride == "" {
		return api.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	expectedGroupKey := strings.TrimSpace(s.overrideGroupKey)
	if expectedGroupKey != "" && !strings.EqualFold(strings.TrimSpace(groupKey), expectedGroupKey) {
		return api.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	if expectedGroupKey == "" && strings.TrimSpace(groupKey) != "" {
		return api.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	return api.DescriptionOverride{SourcePath: "/tmp/source", GroupKey: s.overrideGroupKey, Description: s.descriptionOverride}, nil
}
func (s *stubRepo) ListDescriptionOverridesByPath(context.Context, string) ([]api.DescriptionOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrideCalls++
	if s.descriptionOverride == "" {
		return nil, internalerrors.ErrNotFound
	}
	return []api.DescriptionOverride{{SourcePath: "/tmp/source", GroupKey: s.overrideGroupKey, Description: s.descriptionOverride}}, nil
}
func (s *stubRepo) SaveDescriptionOverride(context.Context, api.DescriptionOverride) error {
	return nil
}
func (s *stubRepo) DeleteDescriptionOverride(context.Context, string, string) error { return nil }
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackerRecordsCalls++
	if s.trackerRecordsErr != nil {
		return nil, s.trackerRecordsErr
	}
	return cloneTrackerMetadata(s.trackerRecords), nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectionsCalls++
	if s.selectionsErr != nil {
		return nil, s.selectionsErr
	}
	return append([]api.ScreenshotFinalSelection(nil), s.selections...), nil
}
func (s *stubRepo) DeleteFinalSelection(context.Context, string) error { return nil }
func (s *stubRepo) ReplaceScreenshotSlots(_ context.Context, _ string, slots []api.ScreenshotSlot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.screenshotSlots = cloneScreenshotSlots(slots)
	return nil
}
func (s *stubRepo) ListScreenshotSlotsByPath(context.Context, string) ([]api.ScreenshotSlot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.screenshotSlotCalls++
	if s.screenshotSlotsErr != nil {
		return nil, s.screenshotSlotsErr
	}
	return cloneScreenshotSlots(s.screenshotSlots), nil
}
func (s *stubRepo) UpsertScreenshotSlotVariants(_ context.Context, _ string, variants []api.ScreenshotSlotVariant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, variant := range variants {
		for idx := range s.screenshotSlots {
			if s.screenshotSlots[idx].SlotOrder != variant.SlotOrder {
				continue
			}
			s.screenshotSlots[idx].Variants = upsertVariant(s.screenshotSlots[idx].Variants, variant)
		}
	}
	return nil
}
func (s *stubRepo) SaveUploadedImages(_ context.Context, _ string, _ string, images []api.UploadedImageLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, images...)
	return nil
}
func (s *stubRepo) ListUploadedImagesByPath(context.Context, string) ([]api.UploadedImageLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploadsCalls++
	if s.uploadsErr != nil {
		return nil, s.uploadsErr
	}
	return append([]api.UploadedImageLink(nil), s.uploads...), nil
}
func (s *stubRepo) DeleteUploadedImage(_ context.Context, _ string, imagePath string, host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	mu      sync.Mutex
	calls   []string
	repo    *stubRepo
}

func (s *stubImageService) ListCandidates(context.Context, api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *stubImageService) Upload(_ context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	s.mu.Lock()
	s.calls = append(s.calls, host)
	err := s.errs[host]
	links, ok := s.uploads[host]
	links = append([]api.UploadedImageLink(nil), links...)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if ok {
		for idx := range links {
			if strings.TrimSpace(links[idx].UsageScope) == "" {
				links[idx].UsageScope = usageScope
			}
		}
		if s.repo != nil {
			s.repo.mu.Lock()
			s.repo.uploads = append(s.repo.uploads, links...)
			s.repo.mu.Unlock()
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
	if s.repo != nil {
		s.repo.mu.Lock()
		s.repo.uploads = append(s.repo.uploads, results...)
		s.repo.mu.Unlock()
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

func TestApplyResolvedDescriptionScreenshotsKeepsMenuImagesSeparate(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/screen1.png", Order: 0, Source: string(api.ScreenshotPurposeFinal)},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/menu1.png", Order: 1, Source: string(api.ScreenshotPurposeMenu)},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/screen2.png", Order: 2, Source: string(api.ScreenshotPurposeFinal)},
		},
	}
	assets := DescriptionAssets{}
	resolved := []api.ScreenshotImage{
		{Path: "/tmp/screen1.png", ImgURL: "https://img.example/screen1.png"},
		{Path: "/tmp/menu1.png", ImgURL: "https://img.example/menu1.png"},
		{Path: "/tmp/screen2.png", ImgURL: "https://img.example/screen2.png"},
	}

	applyResolvedDescriptionScreenshots(context.Background(), meta, repo, nil, &assets, resolved)

	if len(assets.MenuImages) != 1 || !strings.Contains(assets.MenuImages[0].ImgURL, "menu1.png") {
		t.Fatalf("expected menu image to stay separate, got %#v", assets.MenuImages)
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected two normal screenshots, got %#v", assets.Screenshots)
	}
	for _, shot := range assets.Screenshots {
		if strings.Contains(shot.ImgURL, "menu1.png") {
			t.Fatalf("menu image leaked into screenshots: %#v", assets.Screenshots)
		}
	}
}

func TestResolveDescriptionAssetsAppendsMenuSelectionToStoredSlots(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/screen1.png", Order: 0, Source: string(api.ScreenshotPurposeFinal)},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/menu1.png", Order: 1, Source: string(api.ScreenshotPurposeMenu)},
		},
		screenshotSlots: []api.ScreenshotSlot{
			{
				SourcePath:          "/tmp/source",
				SourceKind:          screenshotSlotSourceSelection,
				OriginalKey:         "/tmp/screen1.png",
				ImagePath:           "/tmp/screen1.png",
				SectionKind:         screenshotSectionWrapped,
				RenderInScreenshots: true,
			},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/screen1.png", Host: "imgbb", UsageScope: globalImageUsageScope, ImgURL: "https://img.example/screen1.png"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/menu1.png", Host: "imgbb", UsageScope: globalImageUsageScope, ImgURL: "https://img.example/menu1.png"},
		},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("resolve description assets: %v", err)
	}
	if len(assets.MenuImages) != 1 || !strings.Contains(assets.MenuImages[0].ImgURL, "menu1.png") {
		t.Fatalf("expected stored menu selection to render as menu image, got %#v", assets.MenuImages)
	}
	if len(assets.Screenshots) != 1 || !strings.Contains(assets.Screenshots[0].ImgURL, "screen1.png") {
		t.Fatalf("expected normal screenshot to stay separate, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsUsesOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "unit3d",
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

func TestResolveDescriptionAssetsUsesCompositeGroupOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "unit3d|ptpimg|global",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d|ptpimg|global",
			Trackers:       []string{"AITHER", "BLU"},
			RawDescription: "builder desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "builder desc" {
		t.Fatalf("expected prepared composite group description, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected composite group description to be treated as override")
	}
}

func TestResolveDescriptionAssetsLoadsStoredCompositeGroupOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "unit3d|ptpimg|global",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d|ptpimg|global",
			Trackers:       []string{"AITHER", "BLU"},
			RawDescription: "",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "override desc" {
		t.Fatalf("expected stored composite override, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected stored composite override to be treated as override")
	}
}

func TestResolveDescriptionAssetsDoesNotFallbackToLegacyDefaultGroupOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "legacy default desc",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "HDB", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "db desc" {
		t.Fatalf("expected tracker metadata description when no explicit group override exists, got %q", assets.Description)
	}
	if assets.Override {
		t.Fatalf("expected no implicit override from legacy default group")
	}
}

func TestResolveDescriptionAssetsPrefersCanonicalGroupDescription(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "hdb",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "HDB", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "hdb",
			Trackers:       []string{"HDB"},
			RawDescription: "canonical desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "canonical desc" {
		t.Fatalf("expected canonical description, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected canonical group description to be treated as override")
	}
}

func TestResolveDescriptionAssetsPrefersTrackerScopedCompositeGroupDescription(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "hdb|hdb|tracker:HDB",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "HDB", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{
			{
				GroupKey:       "hdb|ptpimg|global",
				Trackers:       []string{"HDB"},
				RawDescription: "global desc",
			},
			{
				GroupKey:       "hdb|hdb|tracker:HDB",
				Trackers:       []string{"HDB"},
				RawDescription: "tracker desc",
			},
		},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "tracker desc" {
		t.Fatalf("expected tracker-scoped composite group description, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected tracker-scoped composite group description to be treated as override")
	}
}

func TestResolveDescriptionAssetsUsesCanonicalGroupDescriptionWithoutRepo(t *testing.T) {
	meta := api.PreparedMetadata{
		DescriptionOverride: "legacy override desc",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "hdb",
			Trackers:       []string{"HDB"},
			RawDescription: "canonical desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "canonical desc" {
		t.Fatalf("expected canonical description without repo, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected canonical group description to be treated as override")
	}
}

func TestResolveDescriptionAssetsUsesCanonicalGroupDescriptionWithoutSourcePath(t *testing.T) {
	repo := &stubRepo{descriptionOverride: "stored override desc", overrideGroupKey: "hdb"}
	meta := api.PreparedMetadata{
		SourcePath:          "",
		DescriptionOverride: "legacy override desc",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "hdb",
			Trackers:       []string{"HDB"},
			RawDescription: "canonical desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "canonical desc" {
		t.Fatalf("expected canonical description without source path, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected canonical group description to be treated as override")
	}
	if repo.overrideCalls != 0 {
		t.Fatalf("expected no repo description override lookup when source path is blank, got %d calls", repo.overrideCalls)
	}
}

func TestResolveDescriptionAssetsIgnoresAmbiguousTrackerGroupFallback(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "hdb",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "HDB", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{
			{
				GroupKey:       "group-a",
				Trackers:       []string{"HDB"},
				RawDescription: "canonical a",
			},
			{
				GroupKey:       "group-b",
				Trackers:       []string{"HDB"},
				RawDescription: "canonical b",
			},
		},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "override desc" {
		t.Fatalf("expected ambiguous tracker-group fallback to defer to stored override, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected stored override to be treated as override")
	}
}

func TestResolveDescriptionAssetsStripsEmbeddedNFOBlocksFromOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]\n\nCustom body",
		overrideGroupKey:    "ant",
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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

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

func TestResolveDescriptionAssetsSkipsTrackerImagesWhenNotKeepingImages(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 0 {
		t.Fatalf("expected no tracker image fallback when keep_images=false, got %#v", assets.Screenshots)
	}
	if len(assets.Slots) != 0 {
		t.Fatalf("expected no synthesized tracker image slots when keep_images=false, got %#v", assets.Slots)
	}
}

func TestResolveDescriptionAssetsSkipsStoredTrackerSlotsWhenNotKeepingImages(t *testing.T) {
	repo := &stubRepo{
		screenshotSlots: []api.ScreenshotSlot{{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			SourceKind:          screenshotSlotSourceTracker,
			OriginalURL:         "https://imgbb.com/stale.png",
			OriginalHost:        "imgbb",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assets.Screenshots) != 0 {
		t.Fatalf("expected stale stored tracker slots skipped when keep_images=false, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsSkipsTMDBTrackerImages(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://image.tmdb.org/t/p/original/poster.jpg", "https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

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
	meta := api.PreparedMetadata{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

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
		overrideGroupKey: "unit3d",
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

func TestApplyUploadedVariantsToSlotsUsesOrderedFallbackForURLOnlySlots(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			OriginalURL:         "https://ptpimg.me/first.png",
			OriginalHost:        "ptpimg",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			OriginalURL:         "https://ptpimg.me/second.png",
			OriginalHost:        "ptpimg",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
	}
	uploads := []api.UploadedImageLink{
		{SourcePath: "/tmp/source", ImagePath: "/tmp/first-local.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/first.jpg", RawURL: "https://img.hdbits.org/first.jpg", WebURL: "https://img.hdbits.org/first"},
		{SourcePath: "/tmp/source", ImagePath: "/tmp/second-local.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/second.jpg", RawURL: "https://img.hdbits.org/second.jpg", WebURL: "https://img.hdbits.org/second"},
	}

	summary := ApplyUploadedVariantsToSlots(slots, uploads)

	if summary.FallbackMatched != 2 {
		t.Fatalf("expected 2 fallback matches, got %#v", summary)
	}
	if slots[0].ImagePath != "/tmp/first-local.png" || slots[1].ImagePath != "/tmp/second-local.png" {
		t.Fatalf("expected fallback to backfill image paths, got %#v", slots)
	}
	if len(slots[0].Variants) != 1 || slots[0].Variants[0].RawURL != "https://img.hdbits.org/first.jpg" {
		t.Fatalf("expected first slot to receive first uploaded variant, got %#v", slots[0].Variants)
	}
	if len(slots[1].Variants) != 1 || slots[1].Variants[0].RawURL != "https://img.hdbits.org/second.jpg" {
		t.Fatalf("expected second slot to receive second uploaded variant, got %#v", slots[1].Variants)
	}
}

func TestApplyUploadedVariantsToSlotsPrefersDirectPathMatches(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			ImagePath:           "/tmp/first-local.png",
			OriginalKey:         "/tmp/first-local.png",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			ImagePath:           "/tmp/second-local.png",
			OriginalKey:         "/tmp/second-local.png",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
	}
	uploads := []api.UploadedImageLink{
		{SourcePath: "/tmp/source", ImagePath: "/tmp/first-local.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/first.jpg", RawURL: "https://img.hdbits.org/first.jpg", WebURL: "https://img.hdbits.org/first"},
		{SourcePath: "/tmp/source", ImagePath: "/tmp/second-local.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/second.jpg", RawURL: "https://img.hdbits.org/second.jpg", WebURL: "https://img.hdbits.org/second"},
	}

	summary := ApplyUploadedVariantsToSlots(slots, uploads)

	if summary.FallbackMatched != 0 {
		t.Fatalf("expected direct path matches only, got %#v", summary)
	}
	if slots[0].ImagePath != "/tmp/first-local.png" || slots[1].ImagePath != "/tmp/second-local.png" {
		t.Fatalf("expected existing image paths to be preserved, got %#v", slots)
	}
}

func TestApplyUploadedVariantsToSlotsSkipsNonRenderableSlotsDuringFallback(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			OriginalURL:         "https://image.tmdb.org/poster.jpg",
			OriginalHost:        "image.tmdb.org",
			SectionKind:         screenshotSectionInline,
			RenderInScreenshots: false,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			OriginalURL:         "https://ptpimg.me/first.png",
			OriginalHost:        "ptpimg",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           2,
			OriginalURL:         "https://ptpimg.me/second.png",
			OriginalHost:        "ptpimg",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
	}
	uploads := []api.UploadedImageLink{
		{SourcePath: "/tmp/source", ImagePath: "/tmp/first-local.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/first.jpg", RawURL: "https://img.hdbits.org/first.jpg", WebURL: "https://img.hdbits.org/first"},
		{SourcePath: "/tmp/source", ImagePath: "/tmp/second-local.png", Host: "hdb", UsageScope: "tracker:HDB", ImgURL: "https://t.hdbits.org/second.jpg", RawURL: "https://img.hdbits.org/second.jpg", WebURL: "https://img.hdbits.org/second"},
	}

	summary := ApplyUploadedVariantsToSlots(slots, uploads)

	if summary.FallbackMatched != 2 {
		t.Fatalf("expected fallback to match renderable slots only, got %#v", summary)
	}
	if len(slots[0].Variants) != 0 {
		t.Fatalf("expected non-renderable slot to remain untouched, got %#v", slots[0].Variants)
	}
	if slots[1].ImagePath != "/tmp/first-local.png" || slots[2].ImagePath != "/tmp/second-local.png" {
		t.Fatalf("expected uploads assigned by renderable order, got %#v", slots)
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

func TestEnsureDescriptionImageHostFallsBackAfterConfiguredHostFailure(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	images := &stubImageService{
		errs: map[string]error{"ptpimg": errors.New("ptpimg unavailable")},
	}

	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"PTP",
		meta,
		config.Config{},
		config.TrackerConfig{ImageHost: "ptpimg"},
		repo,
		images,
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.blocking {
		t.Fatalf("expected fallback success not to block")
	}
	if resolution.feedback.SelectedHost != "pixhost" {
		t.Fatalf("expected pixhost fallback, got %#v", resolution.feedback)
	}
	if len(resolution.feedback.Warnings) != 1 || resolution.feedback.Warnings[0].Host != "ptpimg" {
		t.Fatalf("expected ptpimg warning, got %#v", resolution.feedback.Warnings)
	}
	if len(images.calls) != 2 || images.calls[0] != "ptpimg" || images.calls[1] != "pixhost" {
		t.Fatalf("expected ptpimg then pixhost calls, got %#v", images.calls)
	}
}

func TestEnsureDescriptionImageHostBlocksWhenAllUploadHostsFail(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	images := &stubImageService{
		errs: map[string]error{
			"ptpimg":  errors.New("ptpimg unavailable"),
			"pixhost": errors.New("pixhost unavailable"),
		},
	}

	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"PTP",
		meta,
		config.Config{},
		config.TrackerConfig{},
		repo,
		images,
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolution.blocking {
		t.Fatalf("expected all failed upload hosts to block")
	}
	if resolution.feedback.Status != "warning" {
		t.Fatalf("expected warning feedback, got %#v", resolution.feedback)
	}
	if len(resolution.feedback.Warnings) != 2 {
		t.Fatalf("expected one warning per failed host, got %#v", resolution.feedback.Warnings)
	}
	if len(resolution.screenshots) != 0 {
		t.Fatalf("expected no screenshots after all hosts fail, got %#v", resolution.screenshots)
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
