// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/paths"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
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
	statusUpdates       []uploadStatusUpdate
	descriptionOverride string
	overrideGroupKey    string
	overrideCalls       int
}

type uploadStatusUpdate struct {
	tracker string
	status  string
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
func (s *stubRepo) UpdateLatestUploadRecordStatus(_ context.Context, _ string, tracker string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusUpdates = append(s.statusUpdates, uploadStatusUpdate{tracker: tracker, status: status})
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

func TestResolveDescriptionAssetsDedupesAfterSanitizingBotSignatures(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: "Body\n\n[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]"},
			{Tracker: "AITHER", Description: "Body"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected sanitized duplicate removed, got %q", assets.Description)
	}
}

func TestApplyResolvedDescriptionScreenshotsKeepsMenuImagesSeparate(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/screen1.png", Order: 0, Source: string(api.ScreenshotPurposeFinal)},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/menu1.png", Order: 1, Source: screenshotPurposeMenu},
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
			{SourcePath: "/tmp/source", ImagePath: "/tmp/menu1.png", Order: 1, Source: screenshotPurposeMenu},
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

func TestResolveDescriptionAssetsClearsOverrideWhenSanitizedDescriptionIsEmpty(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]",
		overrideGroupKey:    "unit3d",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "" {
		t.Fatalf("expected empty sanitized description, got %q", assets.Description)
	}
	if assets.Override {
		t.Fatalf("expected empty sanitized override to clear override flag")
	}
}

func TestResolveDescriptionAssetsUsesCompositeGroupOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "unit3d|pixhost|global",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d|pixhost|global",
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
	if !assets.Final {
		t.Fatal("expected prepared composite group description to be final")
	}
}

func TestResolveDescriptionAssetsClearsFinalWhenSanitizedDescriptionIsEmpty(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	meta := api.PreparedMetadata{
		SourcePath: sourcePath,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: "[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "" {
		t.Fatalf("expected empty sanitized description, got %q", assets.Description)
	}
	if assets.Final {
		t.Fatalf("expected empty sanitized final description to clear final flag")
	}
	if assets.Override {
		t.Fatalf("expected empty sanitized final description to clear override flag")
	}

	applyResolvedDescriptionScreenshots(context.Background(), meta, nil, nil, &assets, []api.ScreenshotImage{{
		ImgURL: "https://pixhost.example/1.png",
		RawURL: "https://pixhost.example/raw-1.png",
		Host:   "pixhost",
	}})
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected screenshots preserved for empty final description, got %#v", assets.Screenshots)
	}
}

func TestApplyResolvedDescriptionScreenshotsDoesNotAppendFinalBuilderScreenshots(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	assets := DescriptionAssets{
		Description: "[img]https://old.example/1.png[/img]",
		Final:       true,
		Slots: []api.ScreenshotSlot{{
			SourcePath:          sourcePath,
			SlotOrder:           0,
			OriginalURL:         "https://old.example/1.png",
			RenderInScreenshots: true,
		}},
	}

	applyResolvedDescriptionScreenshots(context.Background(), api.PreparedMetadata{SourcePath: sourcePath}, nil, nil, &assets, []api.ScreenshotImage{{
		ImgURL: "https://pixhost.example/1.png",
		RawURL: "https://pixhost.example/raw-1.png",
		Host:   "pixhost",
	}})

	if !strings.Contains(assets.Description, "https://pixhost.example/raw-1.png") {
		t.Fatalf("expected final description URL rewritten, got %q", assets.Description)
	}
	if len(assets.Screenshots) != 0 {
		t.Fatalf("expected final builder screenshots not to be appendable, got %#v", assets.Screenshots)
	}
}

func TestApplyResolvedDescriptionScreenshotsPreservesFinalNonRenderableImages(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	posterURL := "https://image.tmdb.org/poster.jpg"
	assets := DescriptionAssets{
		Description: strings.Join([]string{
			"[center][img]" + posterURL + "[/img][/center]",
			"[center][img]https://old.example/1.png[/img][/center]",
		}, "\n"),
		Final: true,
		Slots: []api.ScreenshotSlot{
			{
				SourcePath:          sourcePath,
				SlotOrder:           0,
				OriginalURL:         posterURL,
				RenderInScreenshots: false,
				SectionKind:         screenshotSectionInline,
			},
			{
				SourcePath:          sourcePath,
				SlotOrder:           1,
				OriginalURL:         "https://old.example/1.png",
				RenderInScreenshots: true,
				SectionKind:         screenshotSectionWrapped,
			},
		},
	}

	applyResolvedDescriptionScreenshots(context.Background(), api.PreparedMetadata{SourcePath: sourcePath}, nil, nil, &assets, []api.ScreenshotImage{{
		ImgURL: "https://pixhost.example/1.png",
		RawURL: "https://pixhost.example/raw-1.png",
		Host:   "pixhost",
	}})

	if !strings.Contains(assets.Description, posterURL) {
		t.Fatalf("expected final non-renderable image URL preserved, got %q", assets.Description)
	}
	if !strings.Contains(assets.Description, "https://pixhost.example/raw-1.png") {
		t.Fatalf("expected renderable final screenshot URL rewritten, got %q", assets.Description)
	}
	if len(assets.Screenshots) != 0 {
		t.Fatalf("expected final builder screenshots not to be appendable, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsLoadsStoredCompositeGroupOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "unit3d|pixhost|global",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.PreparedMetadata{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d|pixhost|global",
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

func TestResolveDescriptionAssetsUsesTrackerMatchedVariantGroup(t *testing.T) {
	meta := api.PreparedMetadata{
		DescriptionGroups: []api.DescriptionBuilderGroup{
			{
				GroupKey:       "unit3d",
				Trackers:       []string{"AITHER"},
				RawDescription: "aither raw description",
			},
			{
				GroupKey:       "unit3d|variant:2|global",
				Trackers:       []string{"HHD"},
				RawDescription: "hhd raw description",
			},
		},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HHD", meta, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "hhd raw description" {
		t.Fatalf("expected HHD variant group description, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected variant group description to be treated as override")
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
				GroupKey:       "hdb|pixhost|global",
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
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "pixhost", ImgURL: "https://pixhost.to/a.png"},
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
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png", "https://pixhost.to/c.png"},
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
	if assets.Screenshots[0].ImgURL != "https://imgbb.com/a.png" || assets.Screenshots[1].ImgURL != "https://imgbb.com/b.png" || assets.Screenshots[2].ImgURL != "https://pixhost.to/c.png" {
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

func TestResolveDescriptionAssetsFallbackSanitizesByRecordTracker(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "ANT", Description: "[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]\n\nBody"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected fallback description sanitized by source tracker, got %q", assets.Description)
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

func TestResolveDescriptionAssetsStripsKnownBotSignaturesFromTrackerDescriptions(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: strings.Join([]string{
				"Body",
				"[center][b]Uploaded Using [url=https://github.com/HDInnovations/UNIT3D]UNIT3D[/url] Auto Uploader[/b][/center]",
				"[center]Uploaded Using [url=https://github.com/HDInnovations/UNIT3D]UNIT3D[/url] Auto Uploader[/center]",
				"[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]",
				"[center][url=https://github.com/edge20200/Only-Uploader]Powered by Only-Uploader[/url][/center]",
				"[center][url=/torrents?perPage=50&name=Example][/url][/center]",
				"[right]Created by Upload Assistant[/right]",
				"[right][url=https://github.com/Audionut/Upload-Assistant][size=4]Created by Upload Assistant v7.0.2[/size][/url][/right]",
				"[center][url=https://github.com/Audionut/Upload-Assistant]Created by Audionut's Upload Assistant[/url][/center]",
				"[center][url=https://aither.cc/forums/topics/1349]Created by L4G's Upload Assistant[/url][/center]",
				"[center]Created by L4G's Upload Assistant[/center]",
				"[center] Uploaded with [color=red]\u2764[/color] using GG-BOT Upload Assistant[/center]",
				"[center] Uploaded with [color=red]\u2764[/color] using GG-BOT Upload Assistant[/center]",
				"[img=500]https://files.catbox.moe/5izwmx.svg[/img]",
				"[center]Find our uploads [url=https://aither.cc/torrents?name=Kitsune]here[/url][/center]",
			}, "\n")},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected bot signatures removed, got %q", assets.Description)
	}
	if strings.Contains(assets.Description, "uploadrr") || strings.Contains(assets.Description, "Upload Assistant") || strings.Contains(assets.Description, "UNIT3D") || strings.Contains(assets.Description, "GG-BOT") || strings.Contains(assets.Description, "Find our uploads") || strings.Contains(assets.Description, "5izwmx.svg") {
		t.Fatalf("expected all bot text removed, got %q", assets.Description)
	}
}

func TestSanitizeTrackerDescriptionKeepsMalformedUNIT3DBoldTags(t *testing.T) {
	cases := []string{
		"Body\n[center][bUploaded Using [url=https://github.com/HDInnovations/UNIT3D]UNIT3D[/url] Auto Uploader[/b][/center]",
		"Body\n[center][b]Uploaded Using [url=https://github.com/HDInnovations/UNIT3D]UNIT3D[/url] Auto Uploader[/center]",
	}

	for _, value := range cases {
		cleaned := sanitizeTrackerDescription("AITHER", value)
		if !strings.Contains(cleaned, "UNIT3D") {
			t.Fatalf("expected malformed UNIT3D bold tag to remain, got %q", cleaned)
		}
	}
}

func TestResolveDescriptionAssetsStripsKnownBotSignaturesFromDescriptionGroups(t *testing.T) {
	meta := api.PreparedMetadata{
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey: "unit3d",
			Trackers: []string{"AITHER"},
			RawDescription: strings.Join([]string{
				"Body",
				"[center][b][size=20]brush[/size][/b] This is an internal release which was first released exclusively on Aither. Cheers to all the Aither users[/center]",
				"[right]Created by Upload Assistant[/right]",
			}, "\n\n"),
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected bot signatures removed from prepared group, got %q", assets.Description)
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
			ImageURLs: []string{"https://pixhost.to/fallback-a.png", "https://pixhost.to/fallback-b.png"},
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
	if assets.Screenshots[0].ImgURL != "https://pixhost.to/fallback-a.png" {
		t.Fatalf("expected fallback screenshot urls, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsBackfillsSlotsFromDescriptionOrder(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: strings.TrimSpace(`
[center][img]https://imgbb.com/first.png[/img][/center]
Some text
[comparison=A,B]https://pixhost.to/second.png https://pixhost.to/third.png[/comparison]
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
	if assets.Screenshots[1].ImgURL != "https://pixhost.to/second.png" || assets.Screenshots[2].ImgURL != "https://pixhost.to/third.png" {
		t.Fatalf("expected comparison images in source order, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsLimitsDescriptionSlotsToSelectedImages(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: strings.TrimSpace(`
[center][img]https://lostimg.cc/first.png[/img][/center]
[center][img]https://lostimg.cc/second.png[/img][/center]
[center][img]https://lostimg.cc/extra.png[/img][/center]
`),
		overrideGroupKey: "unit3d",
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/first-local.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/second-local.png", Order: 1},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.screenshotSlots) != 3 {
		t.Fatalf("expected 3 persisted screenshot slots, got %d", len(repo.screenshotSlots))
	}
	if repo.screenshotSlots[2].RenderInScreenshots {
		t.Fatalf("expected unmatched description image to be non-renderable, got %#v", repo.screenshotSlots[2])
	}
	if len(assets.Screenshots) != 2 {
		t.Fatalf("expected selected screenshots only, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].Path != "/tmp/first-local.png" || assets.Screenshots[1].Path != "/tmp/second-local.png" {
		t.Fatalf("expected selected screenshot paths, got %#v", assets.Screenshots)
	}
}

func TestRewriteDescriptionSlotURLsReplacesComparisonImages(t *testing.T) {
	description := strings.TrimSpace(`
[center]
[comparison=A,B,C]
https://lostimg.cc/source.png
https://lostimg.cc/encode.png
https://lostimg.cc/extra.png
[/comparison]
[/center]
`)
	slots := parseDescriptionImageSlots("/tmp/source", description)
	if len(slots) != 3 {
		t.Fatalf("expected three comparison slots, got %d", len(slots))
	}
	for idx := range slots {
		slots[idx].ImagePath = fmt.Sprintf("/tmp/comparison-%d.png", idx)
	}
	rewritten := rewriteDescriptionSlotURLs(description, slots, []api.ScreenshotImage{
		{Path: "/tmp/comparison-0.png", Host: "pixhost", RawURL: "https://pixhost.to/show/source.png"},
		{Path: "/tmp/comparison-1.png", Host: "pixhost", RawURL: "https://pixhost.to/show/encode.png"},
		{Path: "/tmp/comparison-2.png", Host: "pixhost", RawURL: "https://pixhost.to/show/extra.png"},
	}, false)

	if strings.Contains(rewritten, "lostimg.cc") {
		t.Fatalf("expected stale comparison URLs replaced, got %q", rewritten)
	}
	expectedOrder := []string{
		"https://pixhost.to/show/source.png",
		"https://pixhost.to/show/encode.png",
		"https://pixhost.to/show/extra.png",
	}
	last := -1
	for _, expected := range expectedOrder {
		pos := strings.Index(rewritten, expected)
		if pos <= last {
			t.Fatalf("expected comparison replacement order %v, got %q", expectedOrder, rewritten)
		}
		last = pos
	}
}

func TestApplyResolvedDescriptionScreenshotsKeepsComparisonOutOfNormalScreenshots(t *testing.T) {
	description := strings.TrimSpace(`
[comparison=Source,Encode]
https://lostimg.cc/source.png
https://lostimg.cc/encode.png
[/comparison]
`)
	slots := parseDescriptionImageSlots("/tmp/source", description)
	appendSourceImageSlots(&slots, "/tmp/source", []api.ScreenshotImage{{Path: "/tmp/encode_01.png"}})
	assets := DescriptionAssets{Description: description, Slots: slots}
	applyResolvedDescriptionScreenshots(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"}, nil, nil, &assets, []api.ScreenshotImage{
		{Path: "/tmp/source-copy.png", RawURL: "https://pixhost/source.png"},
		{Path: "/tmp/encode_01.png", RawURL: "https://pixhost/encode-comparison.png"},
		{Path: "/tmp/encode_01.png", RawURL: "https://pixhost/encode-normal.png"},
	})

	if !strings.Contains(assets.Description, "https://pixhost/source.png") || !strings.Contains(assets.Description, "https://pixhost/encode-comparison.png") {
		t.Fatalf("expected comparison URLs rewritten, got %q", assets.Description)
	}
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected only normal description screenshot, got %#v", assets.Screenshots)
	}
	if assets.Screenshots[0].RawURL != "https://pixhost/encode-normal.png" {
		t.Fatalf("expected normal duplicate image retained separately, got %#v", assets.Screenshots)
	}
}

func TestDetachComparisonSourceImagesKeepsAppendedNormalScreenshots(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			OriginalKey:         "https://lostimg/source.png",
			OriginalURL:         "https://lostimg/source.png",
			OriginalHost:        "lostimg",
			ImagePath:           "/tmp/aither/source_01.png",
			SectionKind:         screenshotSectionComparison,
			RenderInScreenshots: true,
			Variants: []api.ScreenshotSlotVariant{{
				Host:       "pixhost",
				UsageScope: globalImageUsageScope,
				ImagePath:  "/tmp/aither/source_01.png",
				RawURL:     "https://pixhost/stale-comparison.png",
			}},
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			OriginalKey:         "/tmp/aither/source_01.png",
			ImagePath:           "/tmp/aither/source_01.png",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
			Variants: []api.ScreenshotSlotVariant{{
				Host:       "pixhost",
				UsageScope: globalImageUsageScope,
				ImagePath:  "/tmp/aither/source_01.png",
				RawURL:     "https://pixhost/normal.png",
			}},
		},
	}

	if !detachComparisonSourceImagesFromSlots(slots, []api.ScreenshotImage{{Path: "/tmp/aither/source_01.png"}}) {
		t.Fatal("expected comparison slot to detach local tracker image")
	}
	if slots[0].ImagePath != "" {
		t.Fatalf("expected comparison image path cleared, got %#v", slots[0])
	}
	if len(slots[0].Variants) != 0 {
		t.Fatalf("expected stale comparison variants removed, got %#v", slots[0].Variants)
	}
	if slots[1].ImagePath != "/tmp/aither/source_01.png" || len(slots[1].Variants) != 1 {
		t.Fatalf("expected normal tracker image preserved, got %#v", slots[1])
	}
}

func TestApplyUploadedVariantsToSlotsUsesOrderedFallbackForURLOnlySlots(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			OriginalURL:         "https://pixhost.to/first.png",
			OriginalHost:        "pixhost",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			OriginalURL:         "https://pixhost.to/second.png",
			OriginalHost:        "pixhost",
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

func TestApplyUploadedVariantsToSlotsAppliesDuplicatePathToAllSlots(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			ImagePath:           "/tmp/encode.png",
			OriginalURL:         "https://lostimg.cc/encode.png",
			SectionKind:         screenshotSectionComparison,
			RenderInScreenshots: true,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			ImagePath:           "/tmp/encode.png",
			OriginalKey:         "/tmp/encode.png",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
	}
	uploads := []api.UploadedImageLink{
		{SourcePath: "/tmp/source", ImagePath: "/tmp/encode.png", Host: "pixhost", UsageScope: "global", RawURL: "https://pixhost/encode.png", ImgURL: "https://pixhost/encode.png", WebURL: "https://pixhost/view"},
	}

	summary := ApplyUploadedVariantsToSlots(slots, uploads)

	if summary.MatchedUploads != 1 || summary.FallbackMatched != 0 {
		t.Fatalf("expected one direct upload match, got %#v", summary)
	}
	for idx, slot := range slots {
		if len(slot.Variants) != 1 || slot.Variants[0].RawURL != "https://pixhost/encode.png" {
			t.Fatalf("expected slot %d to receive duplicate-path variant, got %#v", idx, slot.Variants)
		}
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
			OriginalURL:         "https://pixhost.to/first.png",
			OriginalHost:        "pixhost",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           2,
			OriginalURL:         "https://pixhost.to/second.png",
			OriginalHost:        "pixhost",
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
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "pixhost", ImgURL: "https://pixhost/a.png", RawURL: "https://pixhost/a.png", WebURL: "https://pixhost/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "pixhost", ImgURL: "https://pixhost/b.png", RawURL: "https://pixhost/b.png", WebURL: "https://pixhost/b"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.SelectedHost != "pixhost" {
		t.Fatalf("expected pixhost host, got %q", resolution.feedback.SelectedHost)
	}
	if resolution.feedback.Reuploaded {
		t.Fatal("expected screenshots to be reused")
	}
}

func TestEnsureDescriptionImageHostReusesUploadedRecordsBeforeUploading(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	meta := api.PreparedMetadata{
		SourcePath: sourcePath,
		TrackerData: []api.TrackerMetadata{{
			Tracker:   "HHD",
			ImageURLs: []string{"https://source.example/screen1.png", "https://source.example/screen2.png"},
		}},
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, sourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	firstPath := filepath.Join(releaseDir, "hhd", buildTrackerArtifactImageName(meta.TrackerData[0].ImageURLs[0], 0))
	secondPath := filepath.Join(releaseDir, "hhd", buildTrackerArtifactImageName(meta.TrackerData[0].ImageURLs[1], 1))
	if err := os.MkdirAll(filepath.Dir(firstPath), 0o700); err != nil {
		t.Fatalf("tracker artifact dir: %v", err)
	}
	for _, pathValue := range []string{firstPath, secondPath} {
		if err := os.WriteFile(pathValue, []byte("image"), 0o600); err != nil {
			t.Fatalf("write local image: %v", err)
		}
	}
	repo := &stubRepo{
		uploads: []api.UploadedImageLink{
			{SourcePath: sourcePath, ImagePath: firstPath, Host: "pixhost", UsageScope: "global", ImgURL: "https://pixhost/1.png", RawURL: "https://pixhost/raw1.png", WebURL: "https://pixhost/view1"},
			{SourcePath: sourcePath, ImagePath: secondPath, Host: "pixhost", UsageScope: "global", ImgURL: "https://pixhost/2.png", RawURL: "https://pixhost/raw2.png", WebURL: "https://pixhost/view2"},
		},
	}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"HHD",
		meta,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		config.TrackerConfig{ImageHost: "pixhost"},
		repo,
		images,
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images.calls) != 0 {
		t.Fatalf("expected existing uploaded records to be reused without upload, got calls %v", images.calls)
	}
	if resolution.feedback.SelectedHost != "pixhost" {
		t.Fatalf("expected pixhost reuse, got %q", resolution.feedback.SelectedHost)
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected two reused screenshots, got %d", len(resolution.screenshots))
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
	if resolution.feedback.SelectedHost != "pixhost" {
		t.Fatalf("expected pixhost host, got %q", resolution.feedback.SelectedHost)
	}
	if !resolution.feedback.Reuploaded {
		t.Fatal("expected screenshots to be reuploaded")
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(resolution.screenshots))
	}
	for _, screenshot := range resolution.screenshots {
		if screenshot.Host != "pixhost" {
			t.Fatalf("expected all rehosted screenshots to use pixhost, got %#v", resolution.screenshots)
		}
	}
}

func TestEnsureDescriptionImageHostReuploadsWhenAllowedHostCoverageIsPartial(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "pixhost", ImgURL: "https://pixhost/a.png", RawURL: "https://pixhost/a.png", WebURL: "https://pixhost/a"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, images, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images.calls) != 1 || images.calls[0] != "pixhost" {
		t.Fatalf("expected partial coverage to trigger pixhost reupload, got calls %v", images.calls)
	}
	if resolution.feedback.SelectedHost != "pixhost" {
		t.Fatalf("expected pixhost host, got %q", resolution.feedback.SelectedHost)
	}
	if !resolution.feedback.Reuploaded {
		t.Fatal("expected screenshots to be reuploaded")
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(resolution.screenshots))
	}
}

func TestEnsureDescriptionImageHostAlignsDescriptionSlotsToLocalTrackerImages(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	meta := api.PreparedMetadata{
		SourcePath: sourcePath,
		TrackerData: []api.TrackerMetadata{{
			Tracker: "AITHER",
			Description: strings.TrimSpace(`
[center][img]https://lostimg.cc/first.png[/img][/center]
[center][img]https://lostimg.cc/second.png[/img][/center]
[center][img]https://lostimg.cc/extra.png[/img][/center]
`),
			ImageURLs: []string{"https://source.example/screen1.png", "https://source.example/screen2.png"},
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, sourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	for index, rawURL := range meta.TrackerData[0].ImageURLs {
		pathValue := filepath.Join(releaseDir, "aither", buildTrackerArtifactImageName(rawURL, index))
		if err := os.MkdirAll(filepath.Dir(pathValue), 0o700); err != nil {
			t.Fatalf("tracker artifact dir: %v", err)
		}
		if err := os.WriteFile(pathValue, []byte("image"), 0o600); err != nil {
			t.Fatalf("write local image: %v", err)
		}
	}
	repo := &stubRepo{}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"PTP",
		meta,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		config.TrackerConfig{},
		repo,
		images,
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images.calls) != 1 || images.calls[0] != "pixhost" {
		t.Fatalf("expected pixhost upload from local tracker images, got calls %v", images.calls)
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected two selected local tracker screenshots, got %d", len(resolution.screenshots))
	}
	if len(repo.screenshotSlots) != 3 {
		t.Fatalf("expected persisted description slots, got %d", len(repo.screenshotSlots))
	}
	if repo.screenshotSlots[2].RenderInScreenshots {
		t.Fatalf("expected unmatched old description image to be non-renderable, got %#v", repo.screenshotSlots[2])
	}
}

func TestEnsureDescriptionImageHostRehostsComparisonAndKeepsMatchedDescriptionImage(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	imageBaseURL := "http://8.8.8.8"
	comparisonURLs := []string{
		imageBaseURL + "/source.png",
		imageBaseURL + "/encode.png",
		imageBaseURL + "/other.png",
	}
	meta := api.PreparedMetadata{
		SourcePath: sourcePath,
		TrackerData: []api.TrackerMetadata{{
			Tracker: "AITHER",
			Description: strings.TrimSpace(fmt.Sprintf(`
[comparison=Source,Encode,Other]
%s
%s
%s
[/comparison]
`, comparisonURLs[0], comparisonURLs[1], comparisonURLs[2])),
			ImageURLs: []string{comparisonURLs[1]},
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, sourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	encodePath := filepath.Join(releaseDir, "aither", buildTrackerArtifactImageName(comparisonURLs[1], 0))
	if err := os.MkdirAll(filepath.Dir(encodePath), 0o700); err != nil {
		t.Fatalf("tracker artifact dir: %v", err)
	}
	if err := os.WriteFile(encodePath, []byte("image"), 0o600); err != nil {
		t.Fatalf("write local image: %v", err)
	}
	descriptionImageDir := filepath.Join(releaseDir, "description-images")
	if err := os.MkdirAll(descriptionImageDir, 0o700); err != nil {
		t.Fatalf("description image dir: %v", err)
	}
	for idx, rawURL := range comparisonURLs {
		imagePath := filepath.Join(descriptionImageDir, buildDescriptionSlotImageName(rawURL, idx))
		if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
			t.Fatalf("write materialized description image: %v", err)
		}
	}

	repo := &stubRepo{}
	images := &stubImageService{}
	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"PTP",
		meta,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		config.TrackerConfig{},
		repo,
		images,
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolution.screenshots) != 4 {
		t.Fatalf("expected three comparison screenshots plus one normal screenshot, got %#v", resolution.screenshots)
	}
	for idx := range 3 {
		if strings.Contains(resolution.screenshots[idx].Path, "aither") {
			t.Fatalf("expected comparison screenshot %d to use materialized comparison image, got %#v", idx, resolution.screenshots[idx])
		}
	}
	if !strings.Contains(resolution.screenshots[3].Path, "aither") {
		t.Fatalf("expected normal screenshot to use extracted tracker image, got %#v", resolution.screenshots[3])
	}
	assets, err := ResolveDescriptionAssets(context.Background(), "PTP", meta, repo, api.NopLogger{})
	if err != nil {
		t.Fatalf("resolve assets: %v", err)
	}
	applyResolvedDescriptionScreenshots(context.Background(), meta, repo, nil, &assets, resolution.screenshots)
	if strings.Contains(assets.Description, imageBaseURL) {
		t.Fatalf("expected comparison source URLs replaced, got %q", assets.Description)
	}
	expectedComparison := []string{"https://pixhost/0.png", "https://pixhost/1.png", "https://pixhost/2.png"}
	last := -1
	for _, expected := range expectedComparison {
		pos := strings.Index(assets.Description, expected)
		if pos <= last {
			t.Fatalf("expected rehosted comparison order %v, got %q", expectedComparison, assets.Description)
		}
		last = pos
	}
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected matched extracted image as normal screenshot, got %#v", assets.Screenshots)
	}
	if assets.Screenshots[0].RawURL != "https://pixhost/3.png" {
		t.Fatalf("expected separate normal screenshot upload, got %#v", assets.Screenshots)
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
		errs: map[string]error{"onlyimage": errors.New("onlyimage unavailable")},
	}

	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"OE",
		meta,
		config.Config{},
		config.TrackerConfig{ImageHost: "onlyimage"},
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
	if resolution.feedback.SelectedHost != "imgbox" {
		t.Fatalf("expected imgbox fallback, got %#v", resolution.feedback)
	}
	if len(resolution.feedback.Warnings) != 1 || resolution.feedback.Warnings[0].Host != "onlyimage" {
		t.Fatalf("expected onlyimage warning, got %#v", resolution.feedback.Warnings)
	}
	if len(images.calls) != 2 || images.calls[0] != "onlyimage" || images.calls[1] != "imgbox" {
		t.Fatalf("expected onlyimage then imgbox calls, got %#v", images.calls)
	}
}

func TestEnsureDescriptionImageHostFallsBackFromConfiguredHostForUnrestrictedTracker(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}
	images := &stubImageService{
		errs: map[string]error{"pixhost": errors.New("pixhost unavailable")},
	}

	resolution, err := ensureDescriptionImageHost(
		context.Background(),
		"HHD",
		meta,
		config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "imgbb"}},
		config.TrackerConfig{ImageHost: "pixhost"},
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
	if resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected imgbb fallback, got %#v", resolution.feedback)
	}
	if len(resolution.feedback.AllowedHosts) != 0 {
		t.Fatalf("expected unrestricted tracker policy, got %#v", resolution.feedback.AllowedHosts)
	}
	if len(resolution.feedback.Warnings) != 1 || resolution.feedback.Warnings[0].Host != "pixhost" {
		t.Fatalf("expected pixhost warning, got %#v", resolution.feedback.Warnings)
	}
	if len(images.calls) != 2 || images.calls[0] != "pixhost" || images.calls[1] != "imgbb" {
		t.Fatalf("expected pixhost then imgbb calls, got %#v", images.calls)
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
	if len(resolution.feedback.Warnings) != 1 {
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
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "pixhost", ImgURL: "https://pixhost/a.png", RawURL: "https://pixhost/a.png", WebURL: "https://pixhost/a"},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Host: "pixhost", ImgURL: "https://pixhost/b.png", RawURL: "https://pixhost/b.png", WebURL: "https://pixhost/b"},
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

func TestEnsureDescriptionImageHostWarnsOnPartialAllowedHostCoverageWithoutUploader(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Order: 0},
			{SourcePath: "/tmp/source", ImagePath: "/tmp/b.png", Order: 1},
		},
		uploads: []api.UploadedImageLink{
			{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "pixhost", ImgURL: "https://pixhost/a.png", RawURL: "https://pixhost/a.png", WebURL: "https://pixhost/a"},
		},
	}
	meta := api.PreparedMetadata{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHost(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, nil, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.Status != "warning" {
		t.Fatalf("expected warning status, got %#v", resolution.feedback)
	}
	if len(resolution.screenshots) != 0 {
		t.Fatalf("expected no screenshots without uploader, got %#v", resolution.screenshots)
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
			"pixhost": {
				{SourcePath: "/tmp/source", ImagePath: "/tmp/a.png", Host: "pixhost", ImgURL: "https://pixhost/a.png", RawURL: "https://pixhost/a.png", WebURL: "https://pixhost/a"},
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
	if repo.deletedUploads[0] != "pixhost:/tmp/a.png" {
		t.Fatalf("unexpected rollback target: %#v", repo.deletedUploads)
	}
}
