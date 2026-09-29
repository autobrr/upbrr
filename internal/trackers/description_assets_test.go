// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubRepo struct {
	mu                     sync.Mutex
	trackerRecords         []api.TrackerMetadata
	trackerRecordsErr      error
	provenanceMissing      bool
	trackerTimestamps      map[string]time.Time
	trackerRecordsCalls    int
	selections             []api.ScreenshotFinalSelection
	selectionsErr          error
	selectionsCalls        int
	screenshotSlots        []api.ScreenshotSlot
	screenshotSlotsErr     error
	screenshotSlotCalls    int
	uploads                []api.UploadedImageLink
	uploadsErr             error
	uploadsCalls           int
	deletedUploads         []string
	createdUploads         []api.UploadRecord
	ruleFailureSaves       []ruleFailureSave
	statusUpdates          []uploadStatusUpdate
	descriptionOverride    string
	descriptionOverrideErr error
	overrideGroupKey       string
	overrideCalls          int
}

type uploadStatusUpdate struct {
	tracker string
	status  string
}

type ruleFailureSave struct {
	sourcePath string
	tracker    string
	failures   []api.TrackerRuleFailure
}

type descriptionAssetsTestDefinition struct {
	name   string
	family Family
}

func (d descriptionAssetsTestDefinition) Name() string { return d.name }

func (descriptionAssetsTestDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (descriptionAssetsTestDefinition) DefaultBaseURL() string {
	return "https://tracker.example.invalid"
}

func (d descriptionAssetsTestDefinition) TrackerFamily() Family { return d.family }

func (d descriptionAssetsTestDefinition) UseGenericDescriptionCleanup() bool {
	return d.family == FamilyUnit3D
}

func (descriptionAssetsTestDefinition) Prepare(context.Context, PreparationInput) (TrackerPlan, *PreparationFailure) {
	return TrackerPlan{}, nil
}

func descriptionAssetsTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	policies := map[string]*ImageHostPolicy{
		"HDB": {
			AllowedHosts:         []string{"hdb"},
			OwnedHosts:           []string{"hdb"},
			DisableWithoutRehost: true,
		},
		"BTN": {AllowedHosts: []string{"imgbox", "imgbb"}},
		"OE":  {AllowedHosts: []string{"imgbox", "imgbb", "onlyimage", "ptscreens", "passtheimage"}},
		"PTP": {AllowedHosts: []string{"pixhost", "imgbb", "onlyimage", "ptscreens", "passtheimage"}},
	}
	for _, item := range []descriptionAssetsTestDefinition{
		{name: "AITHER", family: FamilyUnit3D},
		{name: "HHD", family: FamilyUnit3D},
		{name: "ANT", family: FamilyStandalone},
		{name: "HDB", family: FamilyStandalone},
		{name: "BTN", family: FamilyStandalone},
		{name: "NBL", family: FamilyStandalone},
		{name: "OE", family: FamilyUnit3D},
		{name: "PTP", family: FamilyStandalone},
		{name: "RHD", family: FamilyUnit3D},
	} {
		policy := policies[item.name]
		if err := registry.RegisterDescriptor(Descriptor{
			Name:       item.name,
			Definition: item,
			Family:     item.family,
			ImageHost:  policy,
		}); err != nil {
			t.Fatalf("register %s description asset policy: %v", item.name, err)
		}
	}
	return registry
}

func (s *stubRepo) GetByPath(context.Context, string) (api.FileMetadata, error) {
	return api.FileMetadata{}, nil
}
func (s *stubRepo) Save(context.Context, api.FileMetadata) error { return nil }
func (s *stubRepo) GetExternalIdentity(context.Context, string) (api.ExternalIdentity, error) {
	return api.ExternalIdentity{}, nil
}
func (s *stubRepo) SaveExternalIdentity(context.Context, api.ExternalIdentity) error { return nil }
func (s *stubRepo) GetExternalMetadata(context.Context, string) (api.SourceScopedMetadata, error) {
	return api.SourceScopedMetadata{}, nil
}
func (s *stubRepo) SaveExternalMetadata(context.Context, api.SourceScopedMetadata) error { return nil }
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
	return api.DescriptionOverride{
		SourcePath:  "/tmp/source",
		GroupKey:    s.overrideGroupKey,
		Description: s.descriptionOverride,
	}, nil
}
func (s *stubRepo) ListDescriptionOverridesByPath(context.Context, string) ([]api.DescriptionOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrideCalls++
	if s.descriptionOverrideErr != nil {
		return nil, s.descriptionOverrideErr
	}
	if s.descriptionOverride == "" {
		return nil, internalerrors.ErrNotFound
	}
	return []api.DescriptionOverride{{
		SourcePath:  "/tmp/source",
		GroupKey:    s.overrideGroupKey,
		Description: s.descriptionOverride,
	}}, nil
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
func (s *stubRepo) SaveTrackerRuleFailures(_ context.Context, sourcePath string, tracker string, failures []api.TrackerRuleFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ruleFailureSaves = append(s.ruleFailureSaves, ruleFailureSave{
		sourcePath: sourcePath,
		tracker:    tracker,
		failures:   append([]api.TrackerRuleFailure(nil), failures...),
	})
	return nil
}
func (s *stubRepo) ListTrackerRuleFailuresByPath(context.Context, string) ([]api.TrackerRuleFailure, error) {
	return nil, nil
}

func (s *stubRepo) GetTrackerTimestamp(_ context.Context, key string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.trackerTimestamps[key]; ok {
		return value, nil
	}
	if s.provenanceMissing {
		return time.Time{}, internalerrors.ErrNotFound
	}
	return time.Time{}, nil
}
func (s *stubRepo) SaveTrackerTimestamp(_ context.Context, timestamp api.TrackerTimestamp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trackerTimestamps == nil {
		s.trackerTimestamps = make(map[string]time.Time)
	}
	s.trackerTimestamps[timestamp.Tracker] = timestamp.UpdatedAt
	return nil
}
func (s *stubRepo) SaveTrackerMetadata(context.Context, api.TrackerMetadata) error { return nil }
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
func (s *stubRepo) ListScreenshotsByPath(context.Context, api.PreparedMediaBinding) ([]api.Screenshot, error) {
	return nil, nil
}
func (s *stubRepo) DeleteScreenshot(context.Context, string) error { return nil }
func (s *stubRepo) SaveFinalSelections(context.Context, api.PreparedMediaBinding, []api.ScreenshotFinalSelection) error {
	return nil
}
func (s *stubRepo) ListFinalSelections(context.Context, api.PreparedMediaBinding) ([]api.ScreenshotFinalSelection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectionsCalls++
	if s.selectionsErr != nil {
		return nil, s.selectionsErr
	}
	return append([]api.ScreenshotFinalSelection(nil), s.selections...), nil
}
func (s *stubRepo) DeleteFinalSelection(context.Context, string) error { return nil }
func (s *stubRepo) ReplaceScreenshotSlots(_ context.Context, _ api.PreparedMediaBinding, slots []api.ScreenshotSlot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.screenshotSlots = cloneScreenshotSlots(slots)
	return nil
}
func (s *stubRepo) ListScreenshotSlotsByPath(context.Context, api.PreparedMediaBinding) ([]api.ScreenshotSlot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.screenshotSlotCalls++
	if s.screenshotSlotsErr != nil {
		return nil, s.screenshotSlotsErr
	}
	return cloneScreenshotSlots(s.screenshotSlots), nil
}
func (s *stubRepo) UpsertScreenshotSlotVariants(_ context.Context, _ api.PreparedMediaBinding, variants []api.ScreenshotSlotVariant) error {
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
func (s *stubRepo) SaveUploadedImages(_ context.Context, _ api.PreparedMediaBinding, _ string, images []api.UploadedImageLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, images...)
	return nil
}
func (s *stubRepo) ListUploadedImagesByPath(context.Context, api.PreparedMediaBinding) ([]api.UploadedImageLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploadsCalls++
	if s.uploadsErr != nil {
		return nil, s.uploadsErr
	}
	return append([]api.UploadedImageLink(nil), s.uploads...), nil
}
func (s *stubRepo) DeleteUploadedImage(_ context.Context, _ api.PreparedMediaBinding, imagePath string, host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletedUploads = append(s.deletedUploads, host+":"+imagePath)
	s.uploads = slices.DeleteFunc(s.uploads, func(upload api.UploadedImageLink) bool {
		return upload.ImagePath == imagePath && upload.Host == host
	})
	return nil
}
func (s *stubRepo) GetPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, nil
}
func (s *stubRepo) SavePlaylistSelection(context.Context, string, string, []string, bool) error {
	return nil
}
func (s *stubRepo) DeletePlaylistSelection(context.Context, string) error    { return nil }
func (s *stubRepo) ListStoredReleasePaths(context.Context) ([]string, error) { return nil, nil }
func (s *stubRepo) PurgeContentData(context.Context, string) error           { return nil }

type stubImageService struct {
	uploads map[string][]api.UploadedImageLink
	errs    map[string]error
	mu      sync.Mutex
	calls   []string
	repo    *stubRepo
}

func (s *stubImageService) ListCandidates(context.Context, api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *stubImageService) Upload(_ context.Context, meta api.ImageHostingSubject, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
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
	meta := api.UploadSubject{SourcePath: "/tmp/source", TrackerData: []api.TrackerMetadata{{Tracker: "AITHER", Description: "meta desc"}}}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "db desc\n\nmeta desc" {
		t.Fatalf("expected combined description, got %q", assets.Description)
	}
}

func TestAudioAnalysisDescriptionKeepsGraphsOutOfScreenshots(t *testing.T) {
	t.Parallel()
	imagePath := filepath.Join(t.TempDir(), "audio-graph.png")
	exact := &api.ExactMediaAssets{
		AudioAnalysis: &api.AudioAnalysisRef{ID: "analysis-1", Revision: 3},
		AudioTracks: []api.AudioDescriptionTrack{{
			Ordinal: 2,
			Images:  []api.ScreenshotImage{{Path: imagePath, Purpose: api.ScreenshotPurposeAudioAnalysis}},
			Stats:   "Peak: -1.0 dB\n[/code]",
		}},
		AudioUploads: []api.UploadedImageLink{{
			ImagePath:  imagePath,
			Purpose:    api.ScreenshotPurposeAudioAnalysis,
			UsageScope: "global",
			ImgURL:     "https://img.example/audio-thumb.png",
			RawURL:     "https://img.example/audio.png",
		}},
	}
	assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", api.UploadSubject{
		DescriptionTemplate: "Base description", ExactMedia: exact,
	}, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve audio description: %v", err)
	}
	if len(assets.Screenshots) != 0 || len(assets.MenuImages) != 0 || len(assets.Slots) != 0 {
		t.Fatalf("audio graph entered screenshot channels: %#v", assets)
	}
	if !strings.Contains(assets.Description, "[spoiler=source_audio]") ||
		!strings.Contains(assets.Description, "[img]https://img.example/audio.png[/img]") ||
		!strings.Contains(assets.Description, "[code]Audio track 2\nPeak: -1.0 dB\n&#91;/code][/code]") ||
		strings.Index(assets.Description, "[img]") > strings.Index(assets.Description, "[code]") {
		t.Fatalf("audio description order or markup = %q", assets.Description)
	}
	edited, err := ResolveDescriptionAssets(t.Context(), "AITHER", api.UploadSubject{
		DescriptionOverride: "Edited body\n\n" + assets.Description, ExactMedia: exact,
	}, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve edited audio description: %v", err)
	}
	if !strings.Contains(edited.Description, "Edited body") || strings.Count(edited.Description, "[spoiler=source_audio]") != 1 {
		t.Fatalf("edited description duplicated audio section: %q", edited.Description)
	}
	comparison := "[spoiler=Comparisons]\n[spoiler=source_audio]copied audio[/spoiler]\n[/spoiler]"
	editedComparison, err := ResolveDescriptionAssets(t.Context(), "AITHER", api.UploadSubject{
		DescriptionOverride: "Edited body\n\n" + comparison + "\n\n" + assets.Description, ExactMedia: exact,
	}, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve edited comparison audio description: %v", err)
	}
	if !strings.Contains(editedComparison.Description, comparison) || strings.Count(editedComparison.Description, "[spoiler=source_audio]") != 2 {
		t.Fatalf("copied comparison audio block changed: %q", editedComparison.Description)
	}
	exact.AudioUploads[0].Host = "imgbb"
	pixhost := exact.AudioUploads[0]
	pixhost.Host = "pixhost"
	pixhost.ImgURL = "https://pixhost.example.invalid/audio-thumb.png"
	pixhost.RawURL = "https://pixhost.example.invalid/audio.png"
	exact.AudioUploads = append(exact.AudioUploads, pixhost)
	exact.AudioUploadHosts = map[string]string{"AITHER": "imgbb", "PTP": "pixhost"}
	for tracker, expectedURL := range map[string]string{
		"AITHER": "https://img.example/audio.png",
		"PTP":    "https://pixhost.example.invalid/audio.png",
	} {
		resolved, resolveErr := ResolveDescriptionAssets(t.Context(), tracker, api.UploadSubject{ExactMedia: exact}, nil,
			api.NopLogger{}, descriptionAssetsTestRegistry(t))
		if resolveErr != nil {
			t.Fatalf("resolve %s audio host: %v", tracker, resolveErr)
		}
		if !strings.Contains(resolved.Description, expectedURL) {
			t.Fatalf("%s audio host selection = %q, want %q", tracker, resolved.Description, expectedURL)
		}
	}
	exact.AudioUploads = nil
	if _, err := ResolveDescriptionAssets(t.Context(), "AITHER", api.UploadSubject{ExactMedia: exact}, nil,
		api.NopLogger{}, descriptionAssetsTestRegistry(t)); err == nil {
		t.Fatal("missing hosted graph must fail description generation")
	}
}

func TestPersistedAudioGraphDoesNotFillPathlessScreenshotSlot(t *testing.T) {
	t.Parallel()
	repo := &stubRepo{uploads: []api.UploadedImageLink{{
		ImagePath:  "audio-graph.png",
		Purpose:    api.ScreenshotPurposeAudioAnalysis,
		Host:       "imgbb",
		UsageScope: "global",
		RawURL:     "https://images.example.invalid/audio.png",
	}}}
	slots, err := synthesizeScreenshotSlots(t.Context(), "AITHER", api.UploadSubject{
		SourcePath:          "/synthetic/release",
		DescriptionOverride: "[img]https://images.example.invalid/shot.png[/img]",
	}, repo, api.NopLogger{}, nil, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("synthesize screenshot slots: %v", err)
	}
	if len(slots) != 1 || len(slots[0].Variants) != 0 {
		t.Fatalf("audio graph filled screenshot slot: %#v", slots)
	}
}

func TestAudioGraphUploadDoesNotAttachToScreenshotSlot(t *testing.T) {
	t.Parallel()
	slots := []api.ScreenshotSlot{{
		SourcePath:          "/synthetic/release",
		SlotOrder:           0,
		OriginalURL:         "https://images.example.invalid/shot.png",
		RenderInScreenshots: true,
	}}
	result := ApplyUploadedVariantsToSlots(slots, []api.UploadedImageLink{{
		ImagePath:  "audio-graph.png",
		Purpose:    api.ScreenshotPurposeAudioAnalysis,
		Host:       "imgbb",
		UsageScope: "global",
		RawURL:     "https://images.example.invalid/audio.png",
	}})
	if result.MatchedUploads != 0 || result.FallbackMatched != 0 || len(slots[0].Variants) != 0 || slots[0].ImagePath != "" {
		t.Fatalf("audio graph attached to screenshot slot: result=%#v slots=%#v", result, slots)
	}
}

func TestPreparedDescriptionAssetsReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	prepared := DescriptionAssets{
		Description: "prepared description",
		MenuImages:  []api.ScreenshotImage{{Path: filepath.Join(t.TempDir(), "prepared-menu.png")}},
	}
	resolved, err := PreparedDescriptionAssets(&prepared)
	if err != nil {
		t.Fatalf("resolve prepared assets: %v", err)
	}
	if resolved.Description != prepared.Description || len(resolved.MenuImages) != 1 || resolved.MenuImages[0].Path != prepared.MenuImages[0].Path {
		t.Fatalf("resolved assets = %#v, want %#v", resolved, prepared)
	}
	resolved.MenuImages[0].Path = "mutated"
	if resolved.MenuImages[0].Path == prepared.MenuImages[0].Path {
		t.Fatal("prepared assets share mutable menu image storage")
	}
}

func TestAppendManualLanguagesToDescriptionAssets(t *testing.T) {
	t.Parallel()

	assets := &DescriptionAssets{Description: "Generated"}
	got := appendManualLanguagesToDescriptionAssets(assets, api.ManualLanguageFacts{
		Audio:     []string{"English", "Spanish"},
		Subtitles: []string{"Brazilian Portuguese"},
	})
	if got == assets {
		t.Fatal("language annotation reused caller assets")
	}
	if assets.Description != "Generated" {
		t.Fatalf("caller description=%q", assets.Description)
	}
	want := "Generated\n\nAudio Language/s: English, Spanish\nSubtitle Language/s: Brazilian Portuguese"
	if got.Description != want {
		t.Fatalf("description=%q, want %q", got.Description, want)
	}
	if again := appendManualLanguagesToDescriptionAssets(assets, api.ManualLanguageFacts{Audio: []string{"English", "Spanish"}}); again.Description != "Generated\n\nAudio Language/s: English, Spanish" {
		t.Fatalf("repeat description=%q", again.Description)
	}
}

func TestAppendManualLanguagesToDescriptionAssetsPreservesCustomAndFinal(t *testing.T) {
	t.Parallel()

	for _, assets := range []*DescriptionAssets{
		{Description: "[b]Custom[/b]", Override: true},
		{Description: "Reviewed", Final: true},
	} {
		got := appendManualLanguagesToDescriptionAssets(assets, api.ManualLanguageFacts{Audio: []string{"English"}})
		if got.Description != assets.Description || got.Override != assets.Override || got.Final != assets.Final {
			t.Fatalf("assets=%#v, got=%#v", assets, got)
		}
	}
}

func TestResolveDescriptionAssetsDedupesAfterSanitizingBotSignatures(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: "Body\n\n[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]"},
			{Tracker: "AITHER", Description: "Body"},
		},
	}
	meta := api.UploadSubject{MediaBinding: trackerTestMediaBinding("/tmp/source"), SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected sanitized duplicate removed, got %q", assets.Description)
	}
}

func TestApplyResolvedDescriptionScreenshotsKeepsMenuImagesSeparate(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/screen1.png",
				Order:      0,
				Source:     string(api.ScreenshotPurposeFinal),
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/menu1.png",
				Order:      1,
				Source:     api.ScreenshotSelectionSourceDVDMenu,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/screen2.png",
				Order:      2,
				Source:     string(api.ScreenshotPurposeFinal),
			},
		},
	}
	assets := DescriptionAssets{}
	resolved := []api.ScreenshotImage{
		{Path: "/tmp/screen1.png", ImgURL: "https://img.example/screen1.png"},
		{Path: "/tmp/menu1.png", ImgURL: "https://img.example/menu1.png"},
		{Path: "/tmp/screen2.png", ImgURL: "https://img.example/screen2.png"},
	}

	applyResolvedDescriptionScreenshots(context.Background(), "", meta, repo, nil, &assets, resolved)

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

func TestDescriptionAssetsPreserveMenuClassificationAcrossRehost(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourcePath := filepath.Join(root, "Example.Release.2026.DVD-GRP")
	binding := trackerTestMediaBinding(sourcePath)
	paths := []string{
		filepath.Join(root, "auto-menu.png"),
		filepath.Join(root, "manual-menu.png"),
		filepath.Join(root, "normal-screen.png"),
	}
	repo := &stubRepo{selections: []api.ScreenshotFinalSelection{
		{
			SourcePath: sourcePath,
			ImagePath:  paths[0],
			Order:      0,
			Source:     api.ScreenshotSelectionSourceDVDMenu,
		},
		{
			SourcePath: sourcePath,
			ImagePath:  paths[1],
			Order:      1,
			Source:     api.ScreenshotSelectionSourceMenu,
		},
		{
			SourcePath: sourcePath,
			ImagePath:  paths[2],
			Order:      2,
			Source:     string(api.ScreenshotPurposeFinal),
		},
	}}
	images := &stubImageService{repo: repo}
	meta := api.UploadSubject{MediaBinding: binding, SourcePath: sourcePath}

	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"PTP",
		meta,
		config.Config{},
		config.TrackerConfig{},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
	)
	if err != nil {
		t.Fatalf("rehost description assets: %v", err)
	}
	if !resolution.feedback.Reuploaded || resolution.feedback.SelectedHost != "pixhost" {
		t.Fatalf("rehost feedback = %#v", resolution.feedback)
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "PTP", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve rehosted assets: %v", err)
	}
	applyResolvedDescriptionScreenshots(context.Background(), "", meta, repo, nil, &assets, resolution.screenshots)
	assertScreenshotPaths(t, assets.MenuImages, paths[:2])
	assertScreenshotPaths(t, assets.Screenshots, paths[2:])
	for _, image := range append(append([]api.ScreenshotImage(nil), assets.MenuImages...), assets.Screenshots...) {
		if image.Host != "pixhost" || strings.TrimSpace(image.RawURL) == "" {
			t.Fatalf("rehosted image lost hosted variant: %#v", image)
		}
	}
}

func TestPartialMenuRemovalUpdatesResolvedAndRenderedDescription(t *testing.T) {
	t.Parallel()

	repo, err := dbsvc.Open(":memory:")
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

	root := t.TempDir()
	sourcePath := filepath.Join(root, "Example.Release.2026.DVD-GRP")
	binding := trackerTestMediaBinding(sourcePath)
	autoFirst := filepath.Join(root, "auto-menu-01.png")
	autoSecond := filepath.Join(root, "auto-menu-02.png")
	normal := filepath.Join(root, "normal-screen-01.png")
	selections := []api.ScreenshotFinalSelection{
		{
			SourcePath: sourcePath,
			ImagePath:  autoFirst,
			Order:      0,
			Source:     api.ScreenshotSelectionSourceDVDMenu,
		},
		{
			SourcePath: sourcePath,
			ImagePath:  autoSecond,
			Order:      1,
			Source:     api.ScreenshotSelectionSourceDVDMenu,
		},
		{
			SourcePath: sourcePath,
			ImagePath:  normal,
			Order:      2,
			Source:     string(api.ScreenshotPurposeFinal),
		},
	}
	if err := repo.SaveFinalSelections(context.Background(), binding, selections); err != nil {
		t.Fatalf("save selections: %v", err)
	}
	uploads := make([]api.UploadedImageLink, 0, len(selections))
	slots := make([]api.ScreenshotSlot, 0, len(selections))
	for index, selection := range selections {
		rawURL := fmt.Sprintf("https://images.example.invalid/%d.png", index)
		uploads = append(uploads, api.UploadedImageLink{
			SourcePath: sourcePath,
			ImagePath:  selection.ImagePath,
			Host:       "imgbb",
			UsageScope: globalImageUsageScope,
			ImgURL:     rawURL,
			RawURL:     rawURL,
			WebURL:     rawURL,
		})
		slots = append(slots, api.ScreenshotSlot{
			SourcePath:          sourcePath,
			SlotOrder:           index,
			SourceKind:          screenshotSlotSourceSelection,
			OriginalKey:         selection.ImagePath,
			ImagePath:           selection.ImagePath,
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
			Variants: []api.ScreenshotSlotVariant{{
				SourcePath: sourcePath,
				SlotOrder:  index,
				Host:       "imgbb",
				UsageScope: globalImageUsageScope,
				ImagePath:  selection.ImagePath,
				ImgURL:     rawURL,
				RawURL:     rawURL,
				WebURL:     rawURL,
			}},
		})
	}
	if err := repo.SaveUploadedImages(context.Background(), binding, "imgbb", uploads); err != nil {
		t.Fatalf("save uploads: %v", err)
	}
	if err := repo.ReplaceScreenshotSlots(context.Background(), binding, slots); err != nil {
		t.Fatalf("save slots: %v", err)
	}

	meta := api.UploadSubject{MediaBinding: binding, SourcePath: sourcePath}
	before := resolveAssetsForTest(t, meta, repo)
	if len(before.MenuImages) != 2 || len(before.Screenshots) != 1 {
		t.Fatalf("assets before removal = %#v", before)
	}

	if _, err := repo.DeleteDiscMenuScreenshot(context.Background(), binding, autoFirst); err != nil {
		t.Fatalf("delete first menu: %v", err)
	}
	after := resolveAssetsForTest(t, meta, repo)
	assertScreenshotPaths(t, after.MenuImages, []string{autoSecond})
	assertScreenshotPaths(t, after.Screenshots, []string{normal})

	description, err := descriptionunit3d.BuildDescription(
		context.Background(),
		api.NewDescriptionSubject(meta),
		config.Config{Description: config.DescriptionSettingsConfig{
			DiscMenuHeader:   "Disc menu token",
			ScreenshotHeader: "Screenshots token",
			ThumbnailSize:    300,
		}},
		config.TrackerConfig{},
		api.NopLogger{},
		"Body token",
		after.MenuImages,
		after.Screenshots,
	)
	if err != nil {
		t.Fatalf("render description: %v", err)
	}
	assertDescriptionTokensInOrder(t, description, "Body token", "Disc menu token", "1.png", "Screenshots token", "2.png")
	if strings.Contains(description, "0.png") {
		t.Fatalf("removed menu remained in description: %q", description)
	}
}

func trackerTestMediaBinding(sourcePath string) api.PreparedMediaBinding {
	return api.PreparedMediaBinding{
		SourcePath:               sourcePath,
		PreparedMediaFingerprint: "test-prepared-media",
		PreparedGeneration:       1,
	}
}

func resolveAssetsForTest(t *testing.T, meta api.UploadSubject, repo UploadPersistence) DescriptionAssets {
	t.Helper()
	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve description assets: %v", err)
	}
	return assets
}

func assertScreenshotPaths(t *testing.T, images []api.ScreenshotImage, expected []string) {
	t.Helper()
	if len(images) != len(expected) {
		t.Fatalf("image count = %d, want %d: %#v", len(images), len(expected), images)
	}
	for index, pathValue := range expected {
		if images[index].Path != pathValue {
			t.Fatalf("image[%d].Path = %q, want %q", index, images[index].Path, pathValue)
		}
	}
}

func assertDescriptionTokensInOrder(t *testing.T, description string, tokens ...string) {
	t.Helper()
	previous := -1
	for _, token := range tokens {
		position := strings.Index(description, token)
		if position <= previous {
			t.Fatalf("description tokens out of order at %q: %q", token, description)
		}
		previous = position
	}
}

func TestResolveDescriptionAssetsAppendsMenuSelectionToStoredSlots(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/screen1.png",
				Order:      0,
				Source:     string(api.ScreenshotPurposeFinal),
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/menu1.png",
				Order:      1,
				Source:     screenshotPurposeMenu,
			},
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/screen1.png",
				Host:       "imgbb",
				UsageScope: globalImageUsageScope,
				ImgURL:     "https://img.example/screen1.png",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/menu1.png",
				Host:       "imgbb",
				UsageScope: globalImageUsageScope,
				ImgURL:     "https://img.example/menu1.png",
			},
		},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d|pixhost|global",
			Trackers:       []string{"AITHER", "BLU"},
			RawDescription: "builder desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "builder desc" {
		t.Fatalf("expected prepared composite group description, got %q", assets.Description)
	}
	if !assets.Override {
		t.Fatalf("expected composite group description to be treated as override")
	}
	if assets.Final {
		t.Fatal("expected custom composite group description to remain open for tracker composition")
	}
}

func TestResolveDescriptionAssetsPreservesFinalDescriptionThatSanitizerWouldEmpty(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	finalDescription := "[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]"
	meta := api.UploadSubject{
		SourcePath:             sourcePath,
		DescriptionGroupsFinal: true,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: finalDescription,
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != finalDescription {
		t.Fatalf("expected final description preserved, got %q", assets.Description)
	}
	if !assets.Final {
		t.Fatal("expected final description flag preserved")
	}
	if !assets.Override {
		t.Fatal("expected final override flag preserved")
	}

	applyResolvedDescriptionScreenshots(context.Background(), "", meta, nil, nil, &assets, []api.ScreenshotImage{{
		ImgURL: "https://pixhost.example/1.png",
		RawURL: "https://pixhost.example/raw-1.png",
		Host:   "pixhost",
	}})
	if assets.Description != finalDescription || len(assets.Screenshots) != 1 {
		t.Fatalf("expected unchanged final description with separate screenshot assets, got %#v", assets)
	}
}

func TestApplyResolvedDescriptionScreenshotsRetainsFinalBuilderScreenshotAssets(t *testing.T) {
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

	applyResolvedDescriptionScreenshots(context.Background(), "", api.UploadSubject{SourcePath: sourcePath}, nil, nil, &assets, []api.ScreenshotImage{{
		ImgURL: "https://pixhost.example/1.png",
		RawURL: "https://pixhost.example/raw-1.png",
		Host:   "pixhost",
	}})

	if !strings.Contains(assets.Description, "https://pixhost.example/raw-1.png") {
		t.Fatalf("expected final description URL rewritten, got %q", assets.Description)
	}
	if len(assets.Screenshots) != 1 || assets.Screenshots[0].RawURL != "https://pixhost.example/raw-1.png" {
		t.Fatalf("expected screenshot assets retained for separate upload fields, got %#v", assets.Screenshots)
	}
}

func TestPrepareUploadContentKeepsFinalDescriptionAndSeparateMedia(t *testing.T) {
	t.Parallel()

	const body = "[b]Reviewed description[/b]"
	shot := api.ScreenshotImage{RawURL: "https://images.example/screen.png"}
	menu := api.ScreenshotImage{RawURL: "https://images.example/menu.png", Purpose: api.ScreenshotPurposeMenu}
	meta := api.UploadSubject{
		SourcePath:             filepath.Join(t.TempDir(), "Example.Release.2026"),
		DescriptionGroupsFinal: true,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: body,
		}},
		ExactMedia: &api.ExactMediaAssets{DVDMenus: []api.DVDMenuCaptureImage{{ScreenshotImage: menu}}},
	}
	registry := descriptionAssetsTestRegistry(t)
	service := NewServiceWithRegistry(config.Config{}, api.NopLogger{}, &stubRepo{}, registry)
	content := service.prepareUploadContent(t.Context(), "AITHER", meta, config.TrackerConfig{}, nil, imageHostPreflight{
		"AITHER": {screenshots: []api.ScreenshotImage{shot}},
	})
	if content.Failure != nil || content.Assets == nil {
		t.Fatalf("prepare final content: %+v", content)
	}
	assets := content.Assets
	if !assets.Final || assets.Description != body {
		t.Fatalf("final description changed: %+v", assets)
	}
	if len(assets.Screenshots) != 1 || assets.Screenshots[0].RawURL != shot.RawURL ||
		len(assets.MenuImages) != 1 || assets.MenuImages[0].RawURL != menu.RawURL {
		t.Fatalf("separate upload media lost: %+v", assets)
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

	applyResolvedDescriptionScreenshots(context.Background(), "", api.UploadSubject{SourcePath: sourcePath}, nil, nil, &assets, []api.ScreenshotImage{{
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
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected final screenshot assets preserved, got %#v", assets.Screenshots)
	}
}

func TestResolveDescriptionAssetsLoadsStoredCompositeGroupOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "override desc",
		overrideGroupKey:    "unit3d|pixhost|global",
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "db desc"}},
	}
	meta := api.UploadSubject{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d|pixhost|global",
			Trackers:       []string{"AITHER", "BLU"},
			RawDescription: "",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
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

	assets, err := ResolveDescriptionAssets(context.Background(), "HHD", meta, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
		SourcePath: "/tmp/source",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "hdb",
			Trackers:       []string{"HDB"},
			RawDescription: "canonical desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
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

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
		DescriptionOverride: "legacy override desc",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "hdb",
			Trackers:       []string{"HDB"},
			RawDescription: "canonical desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
		SourcePath:          "",
		DescriptionOverride: "legacy override desc",
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "hdb",
			Trackers:       []string{"HDB"},
			RawDescription: "canonical desc",
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
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

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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

func TestResolveDescriptionAssetsPreservesStandaloneMarkup(t *testing.T) {
	const description = "[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]\n\nBody\n[right]Created by Upload Assistant[/right]"
	for _, tracker := range []string{"AR", "BHD", "HDB", "PTP", "CUSTOM"} {
		t.Run(tracker, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.Register(descriptionAssetsTestDefinition{name: tracker, family: FamilyStandalone}); err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{"request", "group", "stored_override", "matching_record", "fallback_record"} {
				t.Run(source, func(t *testing.T) {
					meta := api.UploadSubject{}
					var repo *stubRepo
					switch source {
					case "request":
						meta.DescriptionOverride = description
					case "group":
						meta.DescriptionGroups = []api.DescriptionBuilderGroup{{
							GroupKey:       strings.ToLower(tracker),
							Trackers:       []string{tracker},
							RawDescription: description,
						}}
					case "stored_override":
						meta.SourcePath = t.TempDir()
						repo = &stubRepo{descriptionOverride: description, overrideGroupKey: strings.ToLower(tracker)}
					case "matching_record":
						meta.SourcePath = t.TempDir()
						repo = &stubRepo{trackerRecords: []api.TrackerMetadata{{Tracker: tracker, Description: description}}}
					case "fallback_record":
						meta.SourcePath = t.TempDir()
						repo = &stubRepo{trackerRecords: []api.TrackerMetadata{{Tracker: "AITHER", Description: description}}}
					}
					var persistence UploadPersistence
					if repo != nil {
						persistence = repo
					}
					assets, err := ResolveDescriptionAssets(t.Context(), tracker, meta, persistence, api.NopLogger{}, registry)
					if err != nil {
						t.Fatal(err)
					}
					if assets.Description != description {
						t.Fatalf("standalone builder input = %q, want %q", assets.Description, description)
					}
				})
			}
		})
	}
}

func TestResolveDescriptionAssetsStripsEmbeddedNFOBlocksFromOverride(t *testing.T) {
	repo := &stubRepo{
		descriptionOverride: "[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]\n\nCustom body",
		overrideGroupKey:    "unit3d",
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb.com/a.png",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb.com/b.png",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost.to/a.png",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "ulcx desc" {
		t.Fatalf("expected fallback description, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsFallbackSanitizesForDestinationTracker(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "ANT", Description: "[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]\n\nBody"},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != "Body" {
		t.Fatalf("expected fallback description sanitized for destination tracker, got %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsPrefersMatchingTrackerDescription(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "BHD", Description: "[align=center]bhd[/align]"},
			{Tracker: "AITHER", Description: "[center]unit3d[/center]"},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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

func TestStripDescriptionSignaturesPreservesTrackerMarkup(t *testing.T) {
	const body = "[align=left][spoiler=Scene NFO:][code]release notes[/code][/spoiler][/align]\n[right]Personal notes[/right]"
	for _, signature := range []string{
		"[right][url=https://github.com/autobrr/upbrr][size=4]Uploaded by upbrr[/size][/url][/right]",
		"[right]Created by Upload Assistant[/right]",
		"[center]Powered by Only-Uploader[/center]",
	} {
		// The unrecognized plain-text footer remains author-owned content.
		want := body
		if signature == "[center]Powered by Only-Uploader[/center]" {
			want += "\n\n" + signature
		}
		if got := StripDescriptionSignatures(body + "\n\n" + signature); got != want {
			t.Errorf("signature-only cleanup changed tracker markup: %q, want %q", got, want)
		}
	}
}

func TestStripDefaultDescriptionSignature(t *testing.T) {
	value := "[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]\n\nBody"
	if got := StripDefaultDescriptionSignature(value); got != "Body" {
		t.Fatalf("cleaned description = %q", got)
	}
}

func TestResolveDescriptionAssetsPreservesDefaultSignatureForStandaloneBuilder(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "NBL", Description: "[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]\n\nBody"},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "NBL", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != repo.trackerRecords[0].Description {
		t.Fatalf("expected original NBL description for tracker-local cleanup, got %q", assets.Description)
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
		cleaned := sanitizeTrackerDescription("AITHER", value, descriptionAssetsTestRegistry(t))
		if !strings.Contains(cleaned, "UNIT3D") {
			t.Fatalf("expected malformed UNIT3D bold tag to remain, got %q", cleaned)
		}
	}
}

func TestResolveDescriptionAssetsPreservesFinalDescriptionGroups(t *testing.T) {
	finalDescription := strings.Join([]string{
		"Body",
		"[center][b][size=20]brush[/size][/b] This is an internal release which was first released exclusively on Aither. Cheers to all the Aither users[/center]",
		"[right]Created by Upload Assistant[/right]",
		"[right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/right]",
	}, "\n\n")
	meta := api.UploadSubject{
		DescriptionGroupsFinal: true,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: finalDescription,
		}},
	}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, nil, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assets.Description != finalDescription {
		t.Fatalf("expected final prepared group preserved, got %q", assets.Description)
	}
	if !assets.Final || !assets.Override {
		t.Fatalf("expected final override assets, got %#v", assets)
	}

	meta.DescriptionGroups[0].Description = meta.DescriptionGroups[0].RawDescription
	meta.DescriptionGroups[0].RawDescription = ""
	meta.SourcePath = filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	assets, err = ResolveDescriptionAssets(context.Background(), "AITHER", meta, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve persisted-source assets: %v", err)
	}
	if assets.Description != finalDescription {
		t.Fatalf("expected persisted-source final group preserved, got %q", assets.Description)
	}
	if !assets.Final || !assets.Override {
		t.Fatalf("expected persisted-source final override assets, got %#v", assets)
	}
}

func TestResolveDescriptionAssetsFallbackOtherTrackerImages(t *testing.T) {
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "ULCX",
			ImageURLs: []string{"https://imgbb.com/a.png"},
		}},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/a.png",
				RawURL:     "https://hdb/a.png",
				WebURL:     "https://hdb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/b.png",
				RawURL:     "https://hdb/b.png",
				WebURL:     "https://hdb/b",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/a.png",
				RawURL:     "https://hdb/a.png",
				WebURL:     "https://hdb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/b.png",
				RawURL:     "https://hdb/b.png",
				WebURL:     "https://hdb/b",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "HDB", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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

func TestResolveDescriptionAssetsFailsOnScreenshotReadFailure(t *testing.T) {
	repo := &stubRepo{
		selectionsErr: errors.New("database is locked"),
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://imgbb.com/a.png", "https://imgbb.com/b.png"},
		}},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

	_, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("expected screenshot read failure, got %v", err)
	}
}

func TestResolveDescriptionAssetsFailsOnSelectedUploadMismatch(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb.com/a.png",
				RawURL:     "https://imgbb.com/a.png",
				WebURL:     "https://imgbb.com/a",
			},
		},
		trackerRecords: []api.TrackerMetadata{{
			Tracker:   "AITHER",
			ImageURLs: []string{"https://pixhost.to/fallback-a.png", "https://pixhost.to/fallback-b.png"},
		}},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source", Options: api.UploadOptions{KeepImages: true}}

	_, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "missing screenshot variant for slot 1") {
		t.Fatalf("expected selected upload mismatch, got %v", err)
	}
}

func TestResolveDescriptionAssetsAttachesExactUploadedVariants(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	imagePath := filepath.Join(t.TempDir(), "screen.png")
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		ExactMedia: &api.ExactMediaAssets{
			Screenshots: []api.ScreenshotImage{{
				Path:    imagePath,
				Purpose: api.ScreenshotPurposeFinal,
			}},
			ScreenshotUploads: []api.UploadedImageLink{{
				SourcePath: sourcePath,
				ImagePath:  imagePath,
				Host:       "pixhost",
				UsageScope: "global",
				ImgURL:     "https://img.example.invalid/thumb.png",
				RawURL:     "https://img.example.invalid/screen.png",
				WebURL:     "https://img.example.invalid/view",
			}},
		},
	}
	repo := &stubRepo{}
	registry := descriptionAssetsTestRegistry(t)
	preloaded, err := preloadDescriptionAssetData(context.Background(), meta, repo, registry)
	if err != nil {
		t.Fatalf("preload exact assets: %v", err)
	}
	assets, err := resolveDescriptionAssets(context.Background(), "HHD", meta, repo, api.NopLogger{}, preloaded, registry)
	if err != nil {
		t.Fatalf("resolve exact assets: %v", err)
	}
	if len(assets.Screenshots) != 1 || assets.Screenshots[0].RawURL != "https://img.example.invalid/screen.png" {
		t.Fatalf("exact screenshots = %#v", assets.Screenshots)
	}
	if repo.uploadsCalls != 0 {
		t.Fatalf("exact uploads unexpectedly queried broad repository state %d time(s)", repo.uploadsCalls)
	}
}

func TestResolveDescriptionAssetsUsesOnlyExactUploadedVariants(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "Example.Release.2026.1080p-GRP.mkv")
	screenshots := make([]api.ScreenshotImage, 3)
	for index := range screenshots {
		screenshots[index] = api.ScreenshotImage{
			Path:    filepath.Join(tempDir, fmt.Sprintf("screen-%d.png", index)),
			Purpose: api.ScreenshotPurposeFinal,
		}
	}
	upload := api.UploadedImageLink{
		SourcePath: sourcePath,
		ImagePath:  screenshots[0].Path,
		Host:       "pixhost",
		UsageScope: globalImageUsageScope,
		ImgURL:     "https://img.example.invalid/thumb.png",
		RawURL:     "https://img.example.invalid/screen.png",
		WebURL:     "https://img.example.invalid/view",
	}
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		ExactMedia: &api.ExactMediaAssets{
			Screenshots:       screenshots,
			ScreenshotUploads: []api.UploadedImageLink{upload},
		},
	}
	repo := &stubRepo{uploads: []api.UploadedImageLink{{
		SourcePath: sourcePath,
		ImagePath:  screenshots[1].Path,
		Host:       "imgbb",
		RawURL:     "https://ambient.example.invalid/screen.png",
	}}}

	assets, err := ResolveDescriptionAssets(context.Background(), "HHD", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve partial exact assets: %v", err)
	}
	if len(assets.Slots) != len(screenshots) || len(renderableSlots(assets.Slots)) != 1 {
		t.Fatalf("exact slots = %#v", assets.Slots)
	}
	if len(assets.Screenshots) != 1 || assets.Screenshots[0].Path != upload.ImagePath || assets.Screenshots[0].RawURL != upload.RawURL {
		t.Fatalf("exact screenshots = %#v", assets.Screenshots)
	}
	if repo.uploadsCalls != 0 {
		t.Fatalf("partial exact assets queried ambient uploads %d time(s)", repo.uploadsCalls)
	}
}

func TestResolveDescriptionAssetsExactEmptyUploadsDoNotReadAmbientRepositoryState(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	imagePath := filepath.Join(t.TempDir(), "screen.png")
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		ExactMedia: &api.ExactMediaAssets{Screenshots: []api.ScreenshotImage{{
			Path:    imagePath,
			Purpose: api.ScreenshotPurposeFinal,
		}}},
	}
	repo := &stubRepo{uploads: []api.UploadedImageLink{{
		SourcePath: sourcePath,
		ImagePath:  imagePath,
		Host:       "pixhost",
		RawURL:     "https://img.example.invalid/ambient.png",
	}}}
	preloaded, err := preloadDescriptionAssetData(
		context.Background(),
		meta,
		repo,
		descriptionAssetsTestRegistry(t),
	)
	if err != nil {
		t.Fatalf("preload exact empty uploads: %v", err)
	}
	if repo.uploadsCalls != 0 {
		t.Fatalf("exact empty uploads queried broad repository state %d time(s)", repo.uploadsCalls)
	}
	if len(preloaded.uploads) != 0 {
		t.Fatalf("exact empty uploads retained ambient variants: %#v", preloaded.uploads)
	}
}

func TestResolveDescriptionAssetsKeepsExactScreenshotsAndMenusSeparate(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.DVD-GRP")
	exact := &api.ExactMediaAssets{}
	for index := range 4 {
		imagePath := filepath.Join(t.TempDir(), fmt.Sprintf("screen-%d.png", index))
		exact.Screenshots = append(exact.Screenshots, api.ScreenshotImage{
			Path:    imagePath,
			Purpose: api.ScreenshotPurposeFinal,
		})
		exact.ScreenshotUploads = append(exact.ScreenshotUploads, api.UploadedImageLink{
			ImagePath:  imagePath,
			UsageScope: globalImageUsageScope,
			RawURL:     fmt.Sprintf("https://img.example.invalid/screen-%d.png", index),
		})
	}
	for index := range 2 {
		imagePath := filepath.Join(t.TempDir(), fmt.Sprintf("menu-%d.png", index))
		exact.DVDMenus = append(exact.DVDMenus, api.DVDMenuCaptureImage{
			Path:    imagePath,
			Purpose: api.ScreenshotPurposeMenu})
		exact.DVDMenuUploads = append(exact.DVDMenuUploads, api.UploadedImageLink{
			ImagePath:  imagePath,
			UsageScope: globalImageUsageScope,
			RawURL:     fmt.Sprintf("https://img.example.invalid/menu-%d.png", index),
		})
	}
	meta := api.UploadSubject{SourcePath: sourcePath, ExactMedia: exact}
	registry := descriptionAssetsTestRegistry(t)
	preloaded, err := preloadDescriptionAssetData(context.Background(), meta, nil, registry)
	if err != nil {
		t.Fatalf("preload exact media: %v", err)
	}
	assets, err := resolveDescriptionAssets(context.Background(), "HHD", meta, nil, api.NopLogger{}, preloaded, registry)
	if err != nil {
		t.Fatalf("resolve exact media: %v", err)
	}
	if len(assets.Screenshots) != 4 || len(assets.Slots) != 4 || len(assets.MenuImages) != 2 {
		t.Fatalf("exact channels screenshots=%d slots=%d menus=%d", len(assets.Screenshots), len(assets.Slots), len(assets.MenuImages))
	}
	for _, slot := range assets.Slots {
		if slot.SourceKind == api.ScreenshotSelectionSourceMenu || slot.SourceKind == api.ScreenshotSelectionSourceDVDMenu {
			t.Fatalf("menu entered normal screenshot slot: %#v", slot)
		}
	}
	for index, menu := range assets.MenuImages {
		if menu.Purpose != api.ScreenshotPurposeMenu || menu.RawURL != exact.DVDMenuUploads[index].RawURL {
			t.Fatalf("exact menu %d = %#v", index, menu)
		}
	}
}

func TestResolveDescriptionAssetsExactEmptyIsAuthoritative(t *testing.T) {
	t.Parallel()

	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/ambient.png",
			Source:     string(api.ScreenshotPurposeFinal),
		}},
		uploads: []api.UploadedImageLink{{
			ImagePath: "/tmp/ambient.png",
			RawURL:    "https://img.example.invalid/ambient.png",
		}},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source", ExactMedia: &api.ExactMediaAssets{}}
	registry := descriptionAssetsTestRegistry(t)
	preloaded, err := preloadDescriptionAssetData(context.Background(), meta, repo, registry)
	if err != nil {
		t.Fatalf("preload exact empty media: %v", err)
	}
	assets, err := resolveDescriptionAssets(context.Background(), "HHD", meta, repo, api.NopLogger{}, preloaded, registry)
	if err != nil {
		t.Fatalf("resolve exact empty media: %v", err)
	}
	if len(assets.Screenshots) != 0 || len(assets.Slots) != 0 || len(assets.MenuImages) != 0 {
		t.Fatalf("exact empty media fell back to ambient state: %#v", assets)
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
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.screenshotSlots) != 1 {
		t.Fatalf("expected only the ordinary image as a persisted screenshot slot, got %d", len(repo.screenshotSlots))
	}
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected only the ordinary screenshot, got %d", len(assets.Screenshots))
	}
	if assets.Screenshots[0].ImgURL != "https://imgbb.com/first.png" {
		t.Fatalf("expected first description image first, got %#v", assets.Screenshots)
	}
	if !strings.Contains(assets.Description, "[comparison=A,B]https://pixhost.to/second.png https://pixhost.to/third.png[/comparison]") {
		t.Fatalf("comparison block changed during override resolution: %q", assets.Description)
	}
}

func TestResolveDescriptionAssetsKeepsImportedComparisonsOutOfScreenshotSlots(t *testing.T) {
	comparison := "[spoiler=Comparisons]\n[center][url=https://imagebam.example/view/one]" +
		"[img]https://thumbs.imagebam.example/one.jpg[/img][/url][/center]\n[/spoiler]"
	repo := &stubRepo{trackerRecords: []api.TrackerMetadata{{
		SourcePath:  "/tmp/source",
		Tracker:     "AITHER",
		Description: "Release notes\n\n" + comparison,
		ImageURLs:   []string{"https://i.ibb.co/example/full.png"},
	}}}
	assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", api.UploadSubject{
		SourcePath: "/tmp/source",
		Options:    api.UploadOptions{KeepImages: true},
	}, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve imported tracker assets: %v", err)
	}
	if !strings.Contains(assets.Description, comparison) {
		t.Fatalf("imported comparison changed: %q", assets.Description)
	}
	if len(assets.Screenshots) != 1 || assets.Screenshots[0].ImgURL != "https://i.ibb.co/example/full.png" ||
		len(assets.Slots) != 1 {
		t.Fatalf("comparison was imported as screenshots: screenshots=%#v slots=%#v", assets.Screenshots, assets.Slots)
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/first-local.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/second-local.png",
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	assets, err := ResolveDescriptionAssets(context.Background(), "AITHER", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
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

func TestRewriteDescriptionSlotURLsPreservesComparisonImages(t *testing.T) {
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
		{
			Path:   "/tmp/comparison-0.png",
			Host:   "pixhost",
			RawURL: "https://pixhost.to/show/source.png",
		},
		{
			Path:   "/tmp/comparison-1.png",
			Host:   "pixhost",
			RawURL: "https://pixhost.to/show/encode.png",
		},
		{
			Path:   "/tmp/comparison-2.png",
			Host:   "pixhost",
			RawURL: "https://pixhost.to/show/extra.png",
		},
	}, false)

	if rewritten != description {
		t.Fatalf("comparison block changed during screenshot rewrite: %q", rewritten)
	}
}

func TestImportedComparisonMarkupStaysExactThroughCleanupAndSlotRewrite(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[align=left]Source &amp; Encode[/align]\r\n\r\n\r\n" +
		"[center][img width=320]https://img.example/comparison.png[/img][/center]\r\n[/spoiler]"
	oldURL := "https://img.example/old-screen.png"
	description := "Body\n\n" + comparison + "\n\n[img]" + oldURL + "[/img]"
	cleaned := sanitizeTrackerDescription("AITHER", description, descriptionAssetsTestRegistry(t))
	if !strings.Contains(cleaned, "Body\n\n"+comparison+"\n\n[img]"+oldURL+"[/img]") {
		t.Fatalf("tracker cleanup changed imported comparison or its surrounding spacing: %q", cleaned)
	}
	rewritten := rewriteDescriptionSlotURLs(cleaned, []api.ScreenshotSlot{{
		OriginalURL:         oldURL,
		SourceKind:          screenshotSlotSourceTracker,
		RenderInScreenshots: true,
	}}, []api.ScreenshotImage{{RawURL: "https://img.example/new-screen.png"}}, false)
	if !strings.Contains(rewritten, comparison) || strings.Contains(rewritten, oldURL) ||
		!strings.Contains(rewritten, "https://img.example/new-screen.png") {
		t.Fatalf("slot rewrite changed imported comparison or missed separate screenshot: %q", rewritten)
	}
}

func TestStoredImportedComparisonSlotIsExcludedAndCannotRewriteItsBlock(t *testing.T) {
	comparisonURL := "https://img.example/compare.png"
	screenshotURL := "https://img.example/screen.png"
	comparison := "[spoiler=Comparisons]\r\n[center][img]" + comparisonURL + "[/img][/center]\r\n[/spoiler]"
	description := comparison + "\n\n[center][img]" + screenshotURL + "[/img][/center]"
	meta := api.UploadSubject{
		SourcePath:  "/tmp/source",
		TrackerData: []api.TrackerMetadata{{Tracker: "AITHER", Description: description}},
		Options:     api.UploadOptions{KeepImages: true},
	}
	stored := parseDescriptionImageSlots(meta.SourcePath, description)
	if len(stored) != 2 {
		t.Fatalf("expected legacy comparison and normal slots, got %#v", stored)
	}
	preloaded := &preloadedDescriptionAssetData{
		registry:              descriptionAssetsTestRegistry(t),
		screenshotSlots:       stored,
		screenshotSlotsLoaded: true,
	}
	filtered, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, nil, api.NopLogger{}, preloaded, preloaded.registry)
	if err != nil {
		t.Fatalf("load stored slots: %v", err)
	}
	if len(filtered) != 1 || filtered[0].OriginalURL != screenshotURL {
		t.Fatalf("imported comparison stayed in stored screenshot slots: %#v", filtered)
	}
	if filtered[0].SlotOrder != 0 {
		t.Fatalf("ordinary screenshot was not rebuilt after removing comparison slot: %#v", filtered)
	}
	rewritten := rewriteDescriptionSlotURLs(description, stored, []api.ScreenshotImage{
		{RawURL: "https://img.example/rehosted-comparison.png"},
		{RawURL: "https://img.example/rehosted-screen.png"},
	}, false)
	if !strings.Contains(rewritten, comparison) {
		t.Fatalf("persisted comparison slot rewrote imported BBCode: %q", rewritten)
	}
}

func TestStoredImportedComparisonKeepsLaterRepeatedNormalImage(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	comparison := "[spoiler=Comparisons][center][img]" + imageURL + "[/img][/center][/spoiler]"
	meta := api.UploadSubject{
		SourcePath:  "/tmp/source",
		TrackerData: []api.TrackerMetadata{{Tracker: "BHD", Description: comparison + "\n\n[center][img]" + imageURL + "[/img][/center]"}},
		Options:     api.UploadOptions{KeepImages: true},
	}
	stored := []api.ScreenshotSlot{
		{
			SourcePath:  meta.SourcePath,
			SourceKind:  screenshotSlotSourceDescription,
			SlotOrder:   0,
			OriginalURL: imageURL,
			ImagePath:   "/tmp/comparison.png",
		},
		{
			SourcePath:  meta.SourcePath,
			SourceKind:  screenshotSlotSourceDescription,
			SlotOrder:   1,
			OriginalURL: imageURL,
			ImagePath:   "/tmp/screenshot.png",
		},
	}
	preloaded := &preloadedDescriptionAssetData{
		registry:              descriptionAssetsTestRegistry(t),
		screenshotSlots:       stored,
		screenshotSlotsLoaded: true,
	}
	filtered, err := screenshotSlotsFromSource(t.Context(), "BHD", meta, nil, api.NopLogger{}, preloaded, preloaded.registry)
	if err != nil {
		t.Fatalf("load stored slots: %v", err)
	}
	if len(filtered) != 1 || filtered[0].OriginalURL != imageURL || filtered[0].SlotOrder != 0 || !filtered[0].RenderInScreenshots {
		t.Fatalf("later normal screenshot was not rebuilt from current description: %#v", filtered)
	}
}

func TestEditedComparisonRebuildsStoredSlotsAndPreservesSelections(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	secondURL := "https://img.example/second.png"
	comparison := "[spoiler=Comparisons][center][img]" + imageURL + "[/img][/center][/spoiler]"
	normal := "[center][img]" + imageURL + "[/img][/center]"
	oldDescription := comparison + "\n\n" + normal
	meta := api.UploadSubject{
		SourcePath:          "/tmp/source",
		DescriptionOverride: normal + "\n\n" + comparison + "\n\n[center][img]" + secondURL + "[/img][/center]",
		TrackerData:         []api.TrackerMetadata{{Tracker: "AITHER", Description: oldDescription}},
		Options:             api.UploadOptions{KeepImages: true},
	}
	stored := []api.ScreenshotSlot{
		{
			SourcePath:  meta.SourcePath,
			SourceKind:  screenshotSlotSourceDescription,
			SlotOrder:   0,
			OriginalURL: imageURL,
			SectionKind: screenshotSectionComparison,
		},
		{
			SourcePath:          meta.SourcePath,
			SourceKind:          screenshotSlotSourceDescription,
			SlotOrder:           1,
			OriginalURL:         imageURL,
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
	}
	newRepo := func() *stubRepo {
		return &stubRepo{
			screenshotSlots: cloneScreenshotSlots(stored),
			selections: []api.ScreenshotFinalSelection{
				{
					SourcePath: meta.SourcePath,
					ImagePath:  "/tmp/normal.png",
					Order:      0,
				},
				{
					SourcePath: meta.SourcePath,
					ImagePath:  "/tmp/second.png",
					Order:      1,
				},
				{
					SourcePath: meta.SourcePath,
					ImagePath:  "/tmp/extra.png",
					Order:      2,
				},
			},
			uploads: []api.UploadedImageLink{
				{
					SourcePath: meta.SourcePath,
					ImagePath:  "/tmp/normal.png",
					Host:       "imgbb",
					RawURL:     "https://img.example/normal-upload.png",
				},
				{
					SourcePath: meta.SourcePath,
					ImagePath:  "/tmp/second.png",
					Host:       "imgbb",
					RawURL:     "https://img.example/second-upload.png",
				},
				{
					SourcePath: meta.SourcePath,
					ImagePath:  "/tmp/extra.png",
					Host:       "imgbb",
					RawURL:     "https://img.example/extra-upload.png",
				},
			},
		}
	}
	assertRebuilt := func(t *testing.T, slots []api.ScreenshotSlot) {
		t.Helper()
		if len(slots) != 3 || slots[0].SourceKind != screenshotSlotSourceDescription || slots[0].OriginalURL != imageURL ||
			slots[0].ImagePath != "/tmp/normal.png" || slots[0].SlotOrder != 0 || !slots[0].RenderInScreenshots ||
			len(slots[0].Variants) != 1 || slots[0].Variants[0].RawURL != "https://img.example/normal-upload.png" ||
			slots[1].SourceKind != screenshotSlotSourceDescription || slots[1].OriginalURL != secondURL ||
			slots[1].ImagePath != "/tmp/second.png" || slots[1].SlotOrder != 1 || !slots[1].RenderInScreenshots ||
			len(slots[1].Variants) != 1 || slots[1].Variants[0].RawURL != "https://img.example/second-upload.png" ||
			slots[2].SourceKind != screenshotSlotSourceSelection || slots[2].ImagePath != "/tmp/extra.png" ||
			slots[2].SlotOrder != 2 || len(slots[2].Variants) != 1 ||
			slots[2].Variants[0].RawURL != "https://img.example/extra-upload.png" {
			t.Fatalf("reordered comparison lost normal screenshots or selections: %#v", slots)
		}
	}
	registry := descriptionAssetsTestRegistry(t)
	t.Run("persistent", func(t *testing.T) {
		repo := newRepo()
		slots, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry)
		if err != nil {
			t.Fatalf("load slots: %v", err)
		}
		assertRebuilt(t, slots)
		assertRebuilt(t, repo.screenshotSlots)
	})
	t.Run("without persistence", func(t *testing.T) {
		repo := newRepo()
		slots, err := screenshotSlotsFromSourceWithoutPersist(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry)
		if err != nil {
			t.Fatalf("load slots: %v", err)
		}
		assertRebuilt(t, slots)
		if len(repo.screenshotSlots) != 2 || repo.screenshotSlots[0].SectionKind != screenshotSectionComparison {
			t.Fatalf("non-persistent lookup replaced stored slots: %#v", repo.screenshotSlots)
		}
	})
}

func TestEditedComparisonDropsStaleStoredImage(t *testing.T) {
	oldComparisonURL := "https://img.example/old-comparison.png"
	newComparisonURL := "https://img.example/new-comparison.png"
	screenshotURL := "https://img.example/screen.png"
	normal := "[center][img]" + screenshotURL + "[/img][/center]"
	meta := api.UploadSubject{
		SourcePath:          "/tmp/source",
		DescriptionOverride: "[spoiler=Comparisons][img]" + newComparisonURL + "[/img][/spoiler]\n\n" + normal,
		Options:             api.UploadOptions{KeepImages: true},
	}
	repo := &stubRepo{screenshotSlots: []api.ScreenshotSlot{
		{
			SourcePath:  meta.SourcePath,
			SourceKind:  screenshotSlotSourceDescription,
			SlotOrder:   0,
			OriginalURL: oldComparisonURL,
		},
		{
			SourcePath:          meta.SourcePath,
			SourceKind:          screenshotSlotSourceDescription,
			SlotOrder:           1,
			OriginalURL:         screenshotURL,
			ImagePath:           "/tmp/screen.png",
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
			Variants: []api.ScreenshotSlotVariant{{
				SlotOrder: 1,
				Host:      "imgbb",
				ImagePath: "/tmp/screen.png",
				RawURL:    "https://img.example/rehosted.png",
			}},
		},
	}}
	slots, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("load edited comparison slots: %v", err)
	}
	if len(slots) != 1 || slots[0].OriginalURL != screenshotURL || slots[0].SlotOrder != 0 ||
		slots[0].ImagePath != "/tmp/screen.png" || len(slots[0].Variants) != 1 ||
		slots[0].Variants[0].SlotOrder != 0 || slots[0].Variants[0].RawURL != "https://img.example/rehosted.png" ||
		len(repo.screenshotSlots) != 1 || repo.screenshotSlots[0].OriginalURL != screenshotURL {
		t.Fatalf("stale comparison image survived slot reconciliation: result=%#v stored=%#v", slots, repo.screenshotSlots)
	}
}

func TestRemovedComparisonDropsStoredImage(t *testing.T) {
	screenshotURL := "https://img.example/screen.png"
	meta := api.UploadSubject{
		SourcePath:          "/tmp/source",
		DescriptionOverride: "Notes\n\n[center][img]" + screenshotURL + "[/img][/center]",
		Options:             api.UploadOptions{KeepImages: true},
	}
	repo := &stubRepo{screenshotSlots: []api.ScreenshotSlot{
		{
			SourcePath:  meta.SourcePath,
			SourceKind:  screenshotSlotSourceDescription,
			SlotOrder:   0,
			OriginalURL: "https://img.example/removed-comparison.png",
		},
		{
			SourcePath:          meta.SourcePath,
			SourceKind:          screenshotSlotSourceDescription,
			SlotOrder:           1,
			OriginalURL:         screenshotURL,
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		},
	}}
	slots, err := screenshotSlotsFromSource(t.Context(), "BHD", meta, repo, api.NopLogger{}, nil, descriptionAssetsTestRegistry(t))
	if err != nil || len(slots) != 1 || slots[0].OriginalURL != screenshotURL ||
		len(repo.screenshotSlots) != 1 || repo.screenshotSlots[0].OriginalURL != screenshotURL {
		t.Fatalf("removed comparison retained stale image: slots=%#v stored=%#v err=%v", slots, repo.screenshotSlots, err)
	}
}

func TestUnverifiedCachedTrackerImageSlotIsDropped(t *testing.T) {
	meta := api.UploadSubject{
		SourcePath: "/tmp/source",
		TrackerData: []api.TrackerMetadata{{
			Tracker:     "BHD",
			Description: "Legacy notes without image provenance",
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	repo := &stubRepo{
		trackerRecords: meta.TrackerData,
		screenshotSlots: []api.ScreenshotSlot{{
			SourcePath:          meta.SourcePath,
			SourceKind:          screenshotSlotSourceTracker,
			OriginalURL:         "https://img.example/legacy-comparison.png",
			RenderInScreenshots: true,
		}},
	}
	slots, err := screenshotSlotsFromSource(t.Context(), "BHD", meta, repo, api.NopLogger{}, nil, descriptionAssetsTestRegistry(t))
	if err != nil || len(slots) != 0 || len(repo.screenshotSlots) != 0 {
		t.Fatalf("unverified cached tracker image survived slot reconciliation: slots=%#v stored=%#v err=%v", slots, repo.screenshotSlots, err)
	}
}

func TestEditedOverrideKeepsComparisonOutOfStoredSlots(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	comparisonBlock := "[spoiler=Comparisons][center][img]" + imageURL + "[/img][/center][/spoiler]"
	description := comparisonBlock + "\n\n[center][img]" + imageURL + "[/img][/center]"
	meta := api.UploadSubject{
		SourcePath:          "/tmp/source",
		DescriptionOverride: description,
		Options:             api.UploadOptions{KeepImages: true},
	}
	stored := []api.ScreenshotSlot{
		{
			SourcePath:  meta.SourcePath,
			SourceKind:  screenshotSlotSourceDescription,
			SlotOrder:   0,
			OriginalURL: imageURL,
		},
		{
			SourcePath:          meta.SourcePath,
			SourceKind:          screenshotSlotSourceDescription,
			SlotOrder:           1,
			OriginalURL:         imageURL,
			ImagePath:           "/tmp/screen.png",
			RenderInScreenshots: true,
		},
	}
	preloaded := &preloadedDescriptionAssetData{
		registry:              descriptionAssetsTestRegistry(t),
		screenshotSlots:       stored,
		screenshotSlotsLoaded: true,
	}
	filtered, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, nil, api.NopLogger{}, preloaded, preloaded.registry)
	if err != nil || len(filtered) != 1 || filtered[0].SlotOrder != 0 || !filtered[0].RenderInScreenshots {
		t.Fatalf("edited override retained comparison slot: slots=%#v err=%v", filtered, err)
	}
	assets := DescriptionAssets{
		Description: description,
		Slots:       filtered,
		Override:    true,
	}
	applyResolvedDescriptionScreenshots(t.Context(), "AITHER", meta, nil, nil, &assets, []api.ScreenshotImage{{Path: "/tmp/screen.png", RawURL: "https://img.example/rehosted.png"}})
	if !strings.Contains(assets.Description, comparisonBlock) || !strings.Contains(assets.Description, "https://img.example/rehosted.png") {
		t.Fatalf("edited override rewrote comparison or missed ordinary screenshot: %q", assets.Description)
	}
}

func TestStoredComparisonDoesNotCountDeduplicatedOutsideImage(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	description := "[center][img]" + imageURL + "[/img][/center]\n" +
		"[comparison=Source, Encode]" + imageURL + "[/comparison]\n" +
		"[center][img]" + imageURL + "[/img][/center]"
	meta := api.UploadSubject{
		SourcePath:  "/tmp/source",
		TrackerData: []api.TrackerMetadata{{Tracker: "AITHER", Description: description}},
		Options:     api.UploadOptions{KeepImages: true},
	}
	stored := parseDescriptionImageSlots(meta.SourcePath, description)
	if len(stored) != 2 {
		t.Fatalf("expected one normal and one comparison slot, got %#v", stored)
	}
	preloaded := &preloadedDescriptionAssetData{
		registry:              descriptionAssetsTestRegistry(t),
		screenshotSlots:       stored,
		screenshotSlotsLoaded: true,
	}
	filtered, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, nil, api.NopLogger{}, preloaded, preloaded.registry)
	if err != nil || len(filtered) != 1 || filtered[0].SectionKind != screenshotSectionWrapped || filtered[0].SlotOrder != 0 {
		t.Fatalf("comparison slot survived deduplicated normal URL: slots=%#v err=%v", filtered, err)
	}
}

func TestLegacyComparisonOnlyCachedImageIsNotRehosted(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	imageURL := "https://img.example/compare.png"
	record := api.TrackerMetadata{
		Tracker:     "AITHER",
		Description: "[spoiler=Comparisons][img]" + imageURL + "[/img][/spoiler]",
		ImageURLs:   []string{imageURL},
	}
	meta := api.UploadSubject{
		SourcePath:  sourcePath,
		TrackerData: []api.TrackerMetadata{record},
		Options:     api.UploadOptions{KeepImages: true},
	}
	if urls := collectImageURLs(meta.TrackerData, nil); len(urls) != 0 {
		t.Fatalf("comparison-only cached URLs should be excluded: %#v", urls)
	}
	registry := descriptionAssetsTestRegistry(t)
	slots, err := synthesizeScreenshotSlots(t.Context(), "AITHER", meta, &stubRepo{provenanceMissing: true}, api.NopLogger{}, nil, registry)
	if err != nil || len(slots) != 0 {
		t.Fatalf("comparison-only cached image became a slot: slots=%#v err=%v", slots, err)
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, meta.Release)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	artifactPath := filepath.Join(releaseDir, "aither", legacyTrackerArtifactImageName(imageURL, 0))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatalf("artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("image"), 0o600); err != nil {
		t.Fatalf("write legacy artifact: %v", err)
	}
	if local := resolveLocalTrackerScreenshots(t.Context(), meta, config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}, "AITHER", nil, nil, api.NopLogger{}); len(local) != 0 {
		t.Fatalf("comparison-only legacy artifact selected for rehost: %#v", local)
	}
	record.Description += "\n\n[img]" + imageURL + "[/img]"
	if urls := collectImageURLs([]api.TrackerMetadata{record}, nil); len(urls) != 1 || urls[0] != imageURL {
		t.Fatalf("normal screenshot reusing comparison URL was removed: %#v", urls)
	}
}

func TestLegacyLooseBHDComparisonURLIsNotRehosted(t *testing.T) {
	imageURL := "https://img.example/compare.png?width=100"
	record := api.TrackerMetadata{
		Tracker:     "BHD",
		Description: "[spoiler=Comparisons]" + imageURL + "[/spoiler]",
		ImageURLs:   []string{imageURL},
	}
	if urls := collectImageURLs([]api.TrackerMetadata{record}, nil); len(urls) != 0 {
		t.Fatalf("loose comparison URL became a cached screenshot: %#v", urls)
	}
	record.Description += "\n\n" + imageURL
	if urls := collectImageURLs([]api.TrackerMetadata{record}, nil); len(urls) != 1 || urls[0] != imageURL {
		t.Fatalf("separate loose screenshot sharing comparison URL was lost: %#v", urls)
	}
}

func TestLegacyLinkedComparisonOriginalIsNotRehosted(t *testing.T) {
	fullURL := "https://img.example/full.png"
	thumbURL := "https://img.example/thumb.png"
	comparisonBlock := "[spoiler=Comparisons][url=" + fullURL + "][img]" + thumbURL + "[/img][/url][/spoiler]"
	record := api.TrackerMetadata{
		Tracker:     "AITHER",
		Description: comparisonBlock,
		ImageURLs:   []string{fullURL},
	}
	if urls := collectImageURLs([]api.TrackerMetadata{record}, nil); len(urls) != 0 {
		t.Fatalf("linked original in comparison became a cached screenshot: %#v", urls)
	}
	meta := api.UploadSubject{
		SourcePath:  filepath.Join(t.TempDir(), "source.mkv"),
		TrackerData: []api.TrackerMetadata{record},
		Options:     api.UploadOptions{KeepImages: true},
	}
	slots, err := synthesizeScreenshotSlots(t.Context(), "AITHER", meta, &stubRepo{provenanceMissing: true}, api.NopLogger{}, nil, descriptionAssetsTestRegistry(t))
	if err != nil || len(slots) != 0 {
		t.Fatalf("linked comparison original became a screenshot slot: slots=%#v err=%v", slots, err)
	}
	record.Description += "\n\n[url=" + fullURL + "][img]" + thumbURL + "[/img][/url]"
	if urls := collectImageURLs([]api.TrackerMetadata{record}, nil); len(urls) != 1 || urls[0] != fullURL {
		t.Fatalf("linked original reused outside comparison was excluded: %#v", urls)
	}
}

func TestResolveLocalTrackerScreenshotsFindsShiftedHashedImage(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	imageURL := "https://img.example/my%20shot.png"
	meta := api.UploadSubject{
		SourcePath:  sourcePath,
		TrackerData: []api.TrackerMetadata{{Tracker: "AITHER", ImageURLs: []string{imageURL}}},
		Options:     api.UploadOptions{KeepImages: true},
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, meta.Release)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	digest := sha256.Sum256([]byte(imageURL))
	artifactPath := filepath.Join(releaseDir, "aither", fmt.Sprintf("my_shot_02_%x.png", digest[:6]))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatalf("artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("image"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	local := resolveLocalTrackerScreenshots(t.Context(), meta, config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}, "AITHER", nil, nil, api.NopLogger{})
	if len(local) != 1 || local[0].Path != artifactPath {
		t.Fatalf("shifted hashed artifact was not found: %#v", local)
	}
}

func TestLegacyPreparedTrackerImagesRequireProvenance(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	imageURL := "https://img.example/legacy-comparison.png"
	record := api.TrackerMetadata{
		SourcePath:  sourcePath,
		Tracker:     "AITHER",
		TrackerID:   "101",
		Description: "Legacy notes without comparison markup",
		ImageURLs:   []string{imageURL},
	}
	repo := &stubRepo{trackerRecords: []api.TrackerMetadata{record}, provenanceMissing: true}
	registry := descriptionAssetsTestRegistry(t)
	meta := api.UploadSubject{
		SourcePath:  sourcePath,
		TrackerData: []api.TrackerMetadata{record},
		Options:     api.UploadOptions{KeepImages: true},
	}
	if urls := resolveTrackerImageURLs(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry); len(urls) != 0 {
		t.Fatalf("unverified legacy URL was reused: %#v", urls)
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, meta.Release)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	artifactPath := filepath.Join(releaseDir, "aither", buildTrackerArtifactImageName(imageURL, 0))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatalf("artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}
	if local := resolveLocalTrackerScreenshots(t.Context(), meta, cfg, "AITHER", repo, registry, api.NopLogger{}); len(local) != 0 {
		t.Fatalf("unverified local artifact was reused: %#v", local)
	}
}

func TestSynthesizedSlotsExcludeSavedComparisonArtifacts(t *testing.T) {
	registry := descriptionAssetsTestRegistry(t)
	comparisonURL := "https://img.example/comparison.png"
	normalURL := "https://img.example/normal.png"
	for _, test := range []struct {
		name        string
		description string
		urls        []string
		verified    bool
		wantPath    string
	}{
		{
			name:        "legacy comparison only",
			description: "[spoiler=Comparisons][img]" + comparisonURL + "[/img][/spoiler]",
			urls:        []string{comparisonURL},
		},
		{
			name:        "comparison before normal screenshot",
			description: "[spoiler=Comparisons][img]" + comparisonURL + "[/img][/spoiler]\n[center][img]" + normalURL + "[/img][/center]",
			urls:        []string{normalURL},
			verified:    true,
			wantPath:    "normal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "source.mkv")
			trackerDir := filepath.Join(t.TempDir(), "aither")
			comparisonPath := filepath.Join(trackerDir, buildTrackerArtifactImageName(comparisonURL, 0))
			normalPath := filepath.Join(trackerDir, buildTrackerArtifactImageName(normalURL, 0))
			record := api.TrackerMetadata{
				SourcePath:  sourcePath,
				Tracker:     "AITHER",
				TrackerID:   "101",
				Description: test.description,
				ImageURLs:   test.urls,
			}
			repo := &stubRepo{
				trackerRecords:    []api.TrackerMetadata{record},
				provenanceMissing: !test.verified,
				selections: []api.ScreenshotFinalSelection{{
					SourcePath: sourcePath,
					ImagePath:  comparisonPath,
					Order:      0,
				}},
			}
			if test.wantPath != "" {
				repo.selections = append(repo.selections, api.ScreenshotFinalSelection{
					SourcePath: sourcePath,
					ImagePath:  normalPath,
					Order:      1,
				})
			}
			meta := api.UploadSubject{
				SourcePath: sourcePath,
				Options:    api.UploadOptions{KeepImages: true},
			}
			slots, err := synthesizeScreenshotSlots(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry)
			if err != nil {
				t.Fatalf("synthesize slots: %v", err)
			}
			if test.wantPath == "" {
				if len(slots) != 0 {
					t.Fatalf("comparison-only selection became a slot: %#v", slots)
				}
				return
			}
			if len(slots) != 1 || slots[0].ImagePath != normalPath || slots[0].OriginalURL != normalURL {
				t.Fatalf("normal screenshot was paired with comparison artifact: %#v", slots)
			}
		})
	}
}

func TestStoredSharedComparisonSlotPreservesSelectedPathAndVariantsOnce(t *testing.T) {
	registry := descriptionAssetsTestRegistry(t)
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	imageURL := "https://img.example/shared.png"
	description := "[spoiler=Comparisons][center][img]" + imageURL + "[/img][/center][/spoiler]\n" +
		"[center][img]" + imageURL + "[/img][/center]"
	stored := parseDescriptionImageSlots(sourcePath, description)
	if len(stored) != 1 || stored[0].OriginalURL != imageURL || stored[0].SectionKind != screenshotSectionWrapped {
		t.Fatalf("legacy duplicate parser shape changed: %#v", stored)
	}
	oldPath := filepath.Join(t.TempDir(), "description-images", "slot_001_shared.png")
	stored[0].ImagePath = oldPath
	stored[0].Variants = []api.ScreenshotSlotVariant{{
		Host:      "imgbox",
		ImagePath: oldPath,
		ImgURL:    "https://img.example/old-host.png",
	}}
	repo := &stubRepo{
		provenanceMissing: true,
		screenshotSlots:   stored,
		selections:        []api.ScreenshotFinalSelection{{ImagePath: oldPath}},
		uploads: []api.UploadedImageLink{{
			ImagePath: oldPath,
			Host:      "imgbox",
			ImgURL:    "https://img.example/old-host.png",
		}},
	}
	meta := api.UploadSubject{
		SourcePath:             sourcePath,
		DescriptionGroupsFinal: true,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: description,
			HasOverride:    true,
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	slots, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry)
	if err != nil {
		t.Fatalf("reconcile legacy shared slot: %v", err)
	}
	if len(slots) != 1 || slots[0].OriginalURL != imageURL || slots[0].ImagePath != oldPath || len(slots[0].Variants) != 1 {
		t.Fatalf("selected ordinary screenshot was lost: %#v", slots)
	}
	if len(repo.selections) != 1 || len(repo.uploads) != 1 {
		t.Fatalf("selected screenshot references were deleted: selections=%#v uploads=%#v", repo.selections, repo.uploads)
	}
	if _, err := repo.GetTrackerTimestamp(t.Context(), comparisonSlotProvenanceKey(sourcePath, "AITHER", imageURL)); err != nil {
		t.Fatalf("comparison slot migration marker missing: %v", err)
	}
	freshPath := filepath.Join(t.TempDir(), "normal.png")
	repo.screenshotSlots[0].ImagePath = freshPath
	repo.screenshotSlots[0].Variants = []api.ScreenshotSlotVariant{{
		Host:      "imgbox",
		ImagePath: freshPath,
		ImgURL:    "https://img.example/new-host.png",
	}}
	meta.DescriptionGroups[0].RawDescription = "Edited notes\n" + description
	slots, err = screenshotSlotsFromSource(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry)
	if err != nil || len(slots) != 2 || slots[0].ImagePath != freshPath || len(slots[0].Variants) != 1 ||
		slots[1].ImagePath != oldPath {
		t.Fatalf("fresh normal screenshot was cleared after migration: slots=%#v err=%v", slots, err)
	}
}

func TestStoredSharedComparisonSlotClearsUnselectedLegacyAssets(t *testing.T) {
	registry := descriptionAssetsTestRegistry(t)
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	imageURL := "https://img.example/shared.png"
	description := "[spoiler=Comparisons][img]" + imageURL + "[/img][/spoiler]\n[img]" + imageURL + "[/img]"
	stored := parseDescriptionImageSlots(sourcePath, description)
	oldPath := filepath.Join(t.TempDir(), "description-images", "slot_001_shared.png")
	stored[0].ImagePath = oldPath
	stored[0].Variants = []api.ScreenshotSlotVariant{{ImagePath: oldPath, Host: "imgbox"}}
	repo := &stubRepo{provenanceMissing: true, screenshotSlots: stored}
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: description,
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	slots, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, repo, api.NopLogger{}, nil, registry)
	if err != nil || len(slots) != 1 || slots[0].ImagePath != "" || len(slots[0].Variants) != 0 {
		t.Fatalf("unselected comparison asset remained attached: slots=%#v err=%v", slots, err)
	}
}

func TestValidatedNormalImageSharedWithComparisonRemainsAvailable(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	raw := "[spoiler=Comparisons][img]" + imageURL + "[/img][/spoiler]\n[img]" + imageURL + "[/img]"
	cleaned := descriptionunit3d.CleanDescription(raw, "https://aither.cc")
	if len(cleaned.Images) != 1 || cleaned.Images[0].RawURL != imageURL || strings.Count(cleaned.Description, imageURL) != 1 {
		t.Fatalf("producer did not retain one ordinary image and copied comparison: %#v", cleaned)
	}
	registry := descriptionAssetsTestRegistry(t)
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "BHD",
		Definition: descriptionAssetsTestDefinition{name: "BHD", family: FamilyStandalone},
		Family:     FamilyStandalone,
		DataPolicy: &DataLookupPolicy{LegacyImageAssetsNeedProvenance: true},
	}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}
	for _, tracker := range []string{"AITHER", "BHD"} {
		record := api.TrackerMetadata{
			SourcePath:  "/tmp/source",
			Tracker:     tracker,
			Description: cleaned.Description,
			ImageURLs:   []string{imageURL},
		}
		repo := &stubRepo{
			provenanceMissing: true,
			trackerTimestamps: map[string]time.Time{
				TrackerAssetProvenanceKey(record): time.Now().UTC(),
			},
		}
		verified := FilterUnverifiedTrackerImages(t.Context(), repo, registry, []api.TrackerMetadata{record}, api.NopLogger{})
		urls := collectImageURLs(verified, registry)
		if len(urls) != 1 || urls[0] != imageURL {
			t.Fatalf("%s lost validated normal image shared with comparison: %v", tracker, urls)
		}
	}
}

func TestTrackerSpecificSlotViewsPreserveSharedStoredSlots(t *testing.T) {
	registry := descriptionAssetsTestRegistry(t)
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "BHD",
		Definition: descriptionAssetsTestDefinition{name: "BHD", family: FamilyStandalone},
		Family:     FamilyStandalone,
	}); err != nil {
		t.Fatalf("register BHD: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	aitherURL := "https://img.example/aither.png"
	bhdURL := "https://img.example/bhd.png"
	aitherDescription := "[center][img]" + aitherURL + "[/img][/center]"
	bhdDescription := "[center][img]" + bhdURL + "[/img][/center]"
	stored := parseDescriptionImageSlots(sourcePath, aitherDescription)
	stored[0].ImagePath = filepath.Join(t.TempDir(), "selected.png")
	stored[0].Variants = []api.ScreenshotSlotVariant{{
		ImagePath: stored[0].ImagePath,
		Host:      "imgbox",
		ImgURL:    "https://img.example/hosted.png",
	}}
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: aitherDescription},
			{Tracker: "BHD", Description: bhdDescription},
		},
		screenshotSlots: cloneScreenshotSlots(stored),
	}
	meta := api.UploadSubject{SourcePath: sourcePath, Options: api.UploadOptions{KeepImages: true}}
	preloaded, err := preloadDescriptionAssetData(t.Context(), meta, repo, registry)
	if err != nil {
		t.Fatalf("preload description assets: %v", err)
	}
	if !reflect.DeepEqual(repo.screenshotSlots, stored) {
		t.Fatalf("preload changed shared slots: %#v", repo.screenshotSlots)
	}

	type result struct {
		tracker string
		slots   []api.ScreenshotSlot
		err     error
	}
	results := make(chan result, 2)
	for _, tracker := range []string{"AITHER", "BHD"} {
		go func() {
			preloadedCopy := clonePreloadedDescriptionAssetData(preloaded)
			slots, loadErr := screenshotSlotsFromSource(t.Context(), tracker, meta, repo, api.NopLogger{}, preloadedCopy, registry)
			results <- result{
				tracker: tracker,
				slots:   slots,
				err:     loadErr,
			}
		}()
	}
	for range 2 {
		got := <-results
		if got.err != nil || len(got.slots) != 1 {
			t.Fatalf("%s slots: %#v, err=%v", got.tracker, got.slots, got.err)
		}
		wantURL := aitherURL
		if got.tracker == "BHD" {
			wantURL = bhdURL
		}
		if got.slots[0].OriginalURL != wantURL {
			t.Fatalf("%s received another tracker's slot: %#v", got.tracker, got.slots)
		}
	}
	if !reflect.DeepEqual(repo.screenshotSlots, stored) {
		t.Fatalf("tracker-specific view replaced shared slots: %#v", repo.screenshotSlots)
	}
}

func TestScreenshotPreloadKeepsSavedOverrideSlotAssets(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	overrideURL := "https://img.example/override.png"
	override := "[center][img]" + overrideURL + "[/img][/center]"
	stored := parseDescriptionImageSlots(sourcePath, override)
	stored[0].ImagePath = filepath.Join(t.TempDir(), "selected.png")
	stored[0].Variants = []api.ScreenshotSlotVariant{{
		ImagePath: stored[0].ImagePath,
		Host:      "imgbox",
		ImgURL:    "https://img.example/hosted.png",
	}}
	repo := &stubRepo{
		trackerRecords:      []api.TrackerMetadata{{Tracker: "AITHER", Description: "[img]https://img.example/raw.png[/img]"}},
		screenshotSlots:     cloneScreenshotSlots(stored),
		descriptionOverride: override,
		overrideGroupKey:    "unit3d",
	}
	meta := api.UploadSubject{SourcePath: sourcePath, Options: api.UploadOptions{KeepImages: true}}
	registry := descriptionAssetsTestRegistry(t)
	preloaded, err := preloadDescriptionAssetData(t.Context(), meta, repo, registry)
	if err != nil {
		t.Fatalf("preload saved override: %v", err)
	}
	if !reflect.DeepEqual(repo.screenshotSlots, stored) {
		t.Fatalf("preload replaced saved override slots: %#v", repo.screenshotSlots)
	}
	slots, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, repo, api.NopLogger{}, preloaded, registry)
	if err != nil || len(slots) != 1 || slots[0].OriginalURL != overrideURL || slots[0].ImagePath != stored[0].ImagePath ||
		len(slots[0].Variants) != 1 || slots[0].Variants[0].ImgURL != stored[0].Variants[0].ImgURL {
		t.Fatalf("saved override slot assets were lost: slots=%#v err=%v", slots, err)
	}
}

func TestFinalPreparedDescriptionPreservesImportedComparison(t *testing.T) {
	comparisonURL := "https://img.example/shared.png"
	screenshotURL := comparisonURL
	comparisonBlock := "[spoiler=Comparisons]\r\n[img]" + comparisonURL + "[/img]\r\n[/spoiler]"
	description := comparisonBlock + "\n\n[center][img]" + screenshotURL + "[/img][/center]"
	meta := api.UploadSubject{
		SourcePath:             "/tmp/source",
		DescriptionGroupsFinal: true,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: description,
			HasOverride:    true,
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	stored := parseDescriptionImageSlots(meta.SourcePath, description)
	preloaded := &preloadedDescriptionAssetData{
		registry:              descriptionAssetsTestRegistry(t),
		screenshotSlots:       stored,
		screenshotSlotsLoaded: true,
	}
	slots, err := screenshotSlotsFromSource(t.Context(), "AITHER", meta, nil, api.NopLogger{}, preloaded, preloaded.registry)
	if err != nil {
		t.Fatalf("load stored slots: %v", err)
	}
	if len(slots) != 1 || slots[0].OriginalURL != screenshotURL || slots[0].SlotOrder != 0 {
		t.Fatalf("final description retained imported comparison slot: %#v", slots)
	}
	synthesized, err := synthesizeScreenshotSlots(t.Context(), "AITHER", meta, &stubRepo{}, api.NopLogger{}, nil, preloaded.registry)
	if err != nil || len(synthesized) != 1 || synthesized[0].OriginalURL != screenshotURL {
		t.Fatalf("final description synthesized imported comparison slot: slots=%#v err=%v", synthesized, err)
	}
	assets := DescriptionAssets{
		Description: description,
		Slots:       slots,
		Override:    true,
		Final:       true,
	}
	applyResolvedDescriptionScreenshots(t.Context(), "AITHER", meta, nil, preloaded, &assets, []api.ScreenshotImage{{
		RawURL: "https://img.example/rehosted.png",
	}})
	if !strings.Contains(assets.Description, comparisonBlock) || strings.Count(assets.Description, screenshotURL) != 1 ||
		!strings.Contains(assets.Description, "https://img.example/rehosted.png") {
		t.Fatalf("final description changed imported comparison or missed screenshot: %q", assets.Description)
	}
}

func TestImportedNestedComparisonIsExcludedFromSynthesizedSlots(t *testing.T) {
	comparison := "[spoiler=Comparisons][spoiler=Source][img]https://img.example/source.png[/img][/spoiler]" +
		"[img]https://img.example/encode.png[/img][/spoiler]"
	meta := api.UploadSubject{
		SourcePath:  "/tmp/source",
		TrackerData: []api.TrackerMetadata{{Tracker: "AITHER", Description: comparison + "\n\n[center][img]https://img.example/screen.png[/img][/center]"}},
		Options:     api.UploadOptions{KeepImages: true},
	}
	registry := descriptionAssetsTestRegistry(t)
	slots, err := synthesizeScreenshotSlots(t.Context(), "AITHER", meta, &stubRepo{}, api.NopLogger{}, nil, registry)
	if err != nil {
		t.Fatalf("synthesize slots: %v", err)
	}
	if len(slots) != 1 || slots[0].OriginalURL != "https://img.example/screen.png" {
		t.Fatalf("nested comparison imported into screenshot slots: %#v", slots)
	}
}

func TestAlignRenderableSlotsUsesHashedArtifactNameAfterFailedImage(t *testing.T) {
	slots := []api.ScreenshotSlot{
		{OriginalURL: "https://first.example/shot.png", RenderInScreenshots: true},
		{OriginalURL: "https://t1.pixhost.cc/thumbs/123/shot.png", RenderInScreenshots: true},
	}
	imported := descriptionunit3d.CleanDescriptionImages("[center][img]"+slots[1].OriginalURL+"[/img][/center]", "https://blu.example")
	if len(imported.Images) != 1 || imported.Images[0].RawURL != "https://img1.pixhost.cc/images/123/shot.png" {
		t.Fatalf("unexpected Unit3D full image URL: %#v", imported.Images)
	}
	fullURL := imported.Images[0].RawURL
	secondPath := filepath.Join(t.TempDir(), buildTrackerArtifactImageName(fullURL, 1))
	if !alignRenderableSlotsToSourceImages(slots, []api.ScreenshotImage{{Path: secondPath}}) ||
		slots[0].RenderInScreenshots || slots[0].ImagePath != "" || slots[1].ImagePath != secondPath {
		t.Fatalf("failed image hole shifted later screenshot: %#v", slots)
	}
}

func TestAlignRenderableSlotsMatchesEscapedSpaceArtifactAfterFailedImage(t *testing.T) {
	firstURL := "https://first.example/my%20shot.png"
	secondURL := "https://second.example/my%20shot.png"
	slots := []api.ScreenshotSlot{
		{OriginalURL: firstURL, RenderInScreenshots: true},
		{OriginalURL: secondURL, RenderInScreenshots: true},
	}
	digest := sha256.Sum256([]byte(secondURL))
	secondPath := filepath.Join(t.TempDir(), fmt.Sprintf("my_shot_02_%x.png", digest[:6]))
	if !alignRenderableSlotsToSourceImages(slots, []api.ScreenshotImage{{Path: secondPath}}) ||
		slots[0].RenderInScreenshots || slots[0].ImagePath != "" || slots[1].ImagePath != secondPath {
		t.Fatalf("escaped-space image matched failed earlier slot: %#v", slots)
	}
}

func TestAlignRenderableSlotsMatchesNumericURLStemAfterFailedImage(t *testing.T) {
	firstURL := "https://img.example/shot_01.jpg"
	secondURL := "https://img.example/shot_02.jpg"
	for _, name := range []string{
		buildTrackerArtifactImageName(secondURL, 1),
		buildDescriptionSlotImageName(secondURL, 1),
	} {
		t.Run(name, func(t *testing.T) {
			slots := []api.ScreenshotSlot{
				{OriginalURL: firstURL, RenderInScreenshots: true},
				{OriginalURL: secondURL, RenderInScreenshots: true},
			}
			secondPath := filepath.Join(t.TempDir(), name)
			if !alignRenderableSlotsToSourceImages(slots, []api.ScreenshotImage{{Path: secondPath}}) ||
				slots[0].RenderInScreenshots || slots[0].ImagePath != "" || slots[1].ImagePath != secondPath {
				t.Fatalf("numbered URL stem shifted later screenshot: %#v", slots)
			}
		})
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
	assets := DescriptionAssets{
		Description: description,
		Slots:       slots,
		Override:    true,
	}
	applyResolvedDescriptionScreenshots(context.Background(), "", api.UploadSubject{SourcePath: "/tmp/source"}, nil, nil, &assets, []api.ScreenshotImage{
		{Path: "/tmp/source-copy.png", RawURL: "https://pixhost/source.png"},
		{Path: "/tmp/encode_01.png", RawURL: "https://pixhost/encode-comparison.png"},
		{Path: "/tmp/encode_01.png", RawURL: "https://pixhost/encode-normal.png"},
	})

	if assets.Description != description {
		t.Fatalf("comparison block changed during override screenshot rewrite: %q", assets.Description)
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
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/first-local.png",
			Host:       "hdb",
			UsageScope: "tracker:HDB",
			ImgURL:     "https://t.hdbits.org/first.jpg",
			RawURL:     "https://img.hdbits.org/first.jpg",
			WebURL:     "https://img.hdbits.org/first",
		},
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/second-local.png",
			Host:       "hdb",
			UsageScope: "tracker:HDB",
			ImgURL:     "https://t.hdbits.org/second.jpg",
			RawURL:     "https://img.hdbits.org/second.jpg",
			WebURL:     "https://img.hdbits.org/second",
		},
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
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/first-local.png",
			Host:       "hdb",
			UsageScope: "tracker:HDB",
			ImgURL:     "https://t.hdbits.org/first.jpg",
			RawURL:     "https://img.hdbits.org/first.jpg",
			WebURL:     "https://img.hdbits.org/first",
		},
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/second-local.png",
			Host:       "hdb",
			UsageScope: "tracker:HDB",
			ImgURL:     "https://t.hdbits.org/second.jpg",
			RawURL:     "https://img.hdbits.org/second.jpg",
			WebURL:     "https://img.hdbits.org/second",
		},
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
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/encode.png",
			Host:       "pixhost",
			UsageScope: "global",
			RawURL:     "https://pixhost/encode.png",
			ImgURL:     "https://pixhost/encode.png",
			WebURL:     "https://pixhost/view",
		},
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
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/first-local.png",
			Host:       "hdb",
			UsageScope: "tracker:HDB",
			ImgURL:     "https://t.hdbits.org/first.jpg",
			RawURL:     "https://img.hdbits.org/first.jpg",
			WebURL:     "https://img.hdbits.org/first",
		},
		{
			SourcePath: "/tmp/source",
			ImagePath:  "/tmp/second-local.png",
			Host:       "hdb",
			UsageScope: "tracker:HDB",
			ImgURL:     "https://t.hdbits.org/second.jpg",
			RawURL:     "https://img.hdbits.org/second.jpg",
			WebURL:     "https://img.hdbits.org/second",
		},
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
	screenshots := resolveTrackerScreenshotsWithPolicy([]string{
		"not a url",
		"https://",
		"   ",
	}, imageHostPolicy{})
	if len(screenshots) != 0 {
		t.Fatalf("expected no screenshots for invalid urls, got %#v", screenshots)
	}
}

func TestEnsureDescriptionImageHostReusesAllowedHost(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost/a.png",
				RawURL:     "https://pixhost/a.png",
				WebURL:     "https://pixhost/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost/b.png",
				RawURL:     "https://pixhost/b.png",
				WebURL:     "https://pixhost/b",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, nil, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
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
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, meta.Release)
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
			{
				SourcePath: sourcePath,
				ImagePath:  firstPath,
				Host:       "pixhost",
				UsageScope: "global",
				ImgURL:     "https://pixhost/1.png",
				RawURL:     "https://pixhost/raw1.png",
				WebURL:     "https://pixhost/view1",
			},
			{
				SourcePath: sourcePath,
				ImagePath:  secondPath,
				Host:       "pixhost",
				UsageScope: "global",
				ImgURL:     "https://pixhost/2.png",
				RawURL:     "https://pixhost/raw2.png",
				WebURL:     "https://pixhost/view2",
			},
		},
	}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"HHD",
		meta,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		config.TrackerConfig{ImageHost: "pixhost"},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
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

func TestEnsureDescriptionImageHostReusesAllowedHostForRequiredTracker(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, images, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected imgbb host, got %q", resolution.feedback.SelectedHost)
	}
	if resolution.feedback.Reuploaded || len(images.calls) != 0 {
		t.Fatalf("expected existing allowed host to be reused, got feedback=%#v uploads=%v", resolution.feedback, images.calls)
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected 2 screenshots, got %d", len(resolution.screenshots))
	}
	for _, screenshot := range resolution.screenshots {
		if screenshot.Host != "imgbb" {
			t.Fatalf("expected all reused screenshots to use imgbb, got %#v", resolution.screenshots)
		}
	}
}

func TestEnsureDescriptionImageHostReuploadsWhenAllowedHostCoverageIsPartial(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost/a.png",
				RawURL:     "https://pixhost/a.png",
				WebURL:     "https://pixhost/a",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, images, descriptionAssetsTestRegistry(t))
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
	meta := api.UploadSubject{
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
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, meta.Release)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	for index, rawURL := range meta.TrackerData[0].ImageURLs {
		fileName := buildTrackerArtifactImageName(rawURL, index)
		if index == 0 {
			fileName = legacyTrackerArtifactImageName(rawURL, index)
		}
		pathValue := filepath.Join(releaseDir, "aither", fileName)
		if err := os.MkdirAll(filepath.Dir(pathValue), 0o700); err != nil {
			t.Fatalf("tracker artifact dir: %v", err)
		}
		if err := os.WriteFile(pathValue, []byte("image"), 0o600); err != nil {
			t.Fatalf("write local image: %v", err)
		}
	}
	repo := &stubRepo{}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"PTP",
		meta,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		config.TrackerConfig{},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images.calls) != 1 || images.calls[0] != "pixhost" {
		t.Fatalf("expected pixhost upload from local tracker images, got calls %v", images.calls)
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected current and legacy local tracker screenshots, got %d", len(resolution.screenshots))
	}
	if len(repo.screenshotSlots) != 3 {
		t.Fatalf("expected persisted description slots, got %d", len(repo.screenshotSlots))
	}
	if repo.screenshotSlots[2].RenderInScreenshots {
		t.Fatalf("expected unmatched old description image to be non-renderable, got %#v", repo.screenshotSlots[2])
	}
}

func TestImageHostPreflightKeepsOtherTrackerSharedSlots(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	aitherDescription := "[center][img]https://img.example/aither.png[/img][/center]"
	ptpDescription := "[center][img]https://img.example/ptp.png[/img][/center]"
	stored := parseDescriptionImageSlots(sourcePath, aitherDescription)
	stored[0].ImagePath = filepath.Join(t.TempDir(), "aither.png")
	stored[0].Variants = []api.ScreenshotSlotVariant{{
		ImagePath: stored[0].ImagePath,
		Host:      "imgbox",
		ImgURL:    "https://img.example/aither-hosted.png",
	}}
	ptpPath := filepath.Join(t.TempDir(), "ptp.png")
	repo := &stubRepo{
		trackerRecords: []api.TrackerMetadata{
			{Tracker: "AITHER", Description: aitherDescription},
			{Tracker: "PTP", Description: ptpDescription},
		},
		screenshotSlots: cloneScreenshotSlots(stored),
		selections:      []api.ScreenshotFinalSelection{{SourcePath: sourcePath, ImagePath: ptpPath}},
	}
	meta := api.UploadSubject{SourcePath: sourcePath, Options: api.UploadOptions{KeepImages: true}}
	images := &stubImageService{repo: repo}
	registry := descriptionAssetsTestRegistry(t)
	preloaded, err := preloadDescriptionAssetData(t.Context(), meta, repo, registry)
	if err != nil {
		t.Fatalf("preload screenshots: %v", err)
	}
	resolution, err := ensureDescriptionImageHostWithDataAndRegistry(t.Context(), "PTP", meta, config.Config{}, config.TrackerConfig{},
		repo, images, api.NopLogger{}, registry, preloaded)
	if err != nil || len(resolution.screenshots) != 1 {
		t.Fatalf("PTP preflight: resolution=%#v err=%v", resolution, err)
	}
	if !reflect.DeepEqual(repo.screenshotSlots, stored) {
		t.Fatalf("PTP preflight replaced Aither's shared slots: %#v", repo.screenshotSlots)
	}
}

func TestEnsureDescriptionImageHostCopiesImportedComparisonAndRehostsSeparateScreenshot(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	imageBaseURL := "http://8.8.8.8"
	comparisonURLs := []string{
		imageBaseURL + "/source.png",
		imageBaseURL + "/encode.png",
		imageBaseURL + "/other.png",
	}
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		TrackerData: []api.TrackerMetadata{{
			Tracker: "AITHER",
			Description: strings.TrimSpace(fmt.Sprintf(`
[comparison=Source,Encode,Other]
%s
%s
%s
[/comparison]

[img]%s[/img]
`, comparisonURLs[0], comparisonURLs[1], comparisonURLs[2], comparisonURLs[1])),
			ImageURLs: []string{comparisonURLs[1]},
		}},
		Options: api.UploadOptions{KeepImages: true},
	}
	tmpRoot, err := dbsvc.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, meta.Release)
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
	repo := &stubRepo{}
	images := &stubImageService{}
	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"PTP",
		meta,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		config.TrackerConfig{},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolution.screenshots) != 1 || !strings.Contains(resolution.screenshots[0].Path, "aither") {
		t.Fatalf("expected only extracted tracker screenshot to be rehosted, got %#v", resolution.screenshots)
	}
	assets, err := ResolveDescriptionAssets(context.Background(), "PTP", meta, repo, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("resolve assets: %v", err)
	}
	applyResolvedDescriptionScreenshots(context.Background(), "", meta, repo, nil, &assets, resolution.screenshots)
	comparisonBlock := meta.TrackerData[0].Description[:strings.Index(meta.TrackerData[0].Description, "[/comparison]")+len("[/comparison]")]
	if !strings.Contains(assets.Description, comparisonBlock) {
		t.Fatalf("imported comparison was changed: %q", assets.Description)
	}
	if len(assets.Screenshots) != 1 {
		t.Fatalf("expected matched extracted image as normal screenshot, got %#v", assets.Screenshots)
	}
	if assets.Screenshots[0].RawURL != "https://pixhost/0.png" {
		t.Fatalf("expected separate normal screenshot upload, got %#v", assets.Screenshots)
	}
}

func TestEnsureDescriptionImageHostFallsBackAfterConfiguredHostFailure(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	images := &stubImageService{
		errs: map[string]error{"onlyimage": errors.New("onlyimage unavailable")},
	}

	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"OE",
		meta,
		config.Config{},
		config.TrackerConfig{ImageHost: "onlyimage"},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	images := &stubImageService{
		errs: map[string]error{"pixhost": errors.New("pixhost unavailable")},
	}

	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"HHD",
		meta,
		config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "imgbb"}},
		config.TrackerConfig{ImageHost: "pixhost"},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
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

func TestEnsureDescriptionImageHostUploadsPreferredHostForUnrestrictedTracker(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHostWithDataAndRegistry(
		context.Background(),
		"RHD",
		meta,
		config.Config{ImageHosting: config.ImageHostingConfig{Host1: "imgbb", Host2: "pixhost"}},
		config.TrackerConfig{},
		repo,
		images,
		api.NopLogger{},
		descriptionAssetsTestRegistry(t),
		nil,
		"imgbb",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.blocking {
		t.Fatalf("expected unrestricted preferred-host upload not to block")
	}
	if !resolution.feedback.Reuploaded || resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected imgbb reupload feedback, got %#v", resolution.feedback)
	}
	if len(images.calls) != 1 || images.calls[0] != "imgbb" {
		t.Fatalf("expected one imgbb upload, got %#v", images.calls)
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected two rehosted screenshots, got %#v", resolution.screenshots)
	}
	for _, screenshot := range resolution.screenshots {
		if screenshot.Host != "imgbb" || strings.TrimSpace(screenshot.RawURL) == "" {
			t.Fatalf("expected imgbb screenshot URLs, got %#v", resolution.screenshots)
		}
	}
}

func TestEnsureDescriptionImageHostSkipsHostThatFailedEarlierInRun(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: sourcePath,
				ImagePath:  filepath.Join(t.TempDir(), "a.png"),
				Order:      0,
			},
			{
				SourcePath: sourcePath,
				ImagePath:  filepath.Join(t.TempDir(), "b.png"),
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		ImageHostOverrides: api.ImageHostOverrides{
			FailedHosts: []string{" IMGBOX ", "imgbox"},
		},
	}
	images := &stubImageService{}

	resolution, err := ensureDescriptionImageHostWithDataAndRegistry(
		context.Background(),
		"OE",
		meta,
		config.Config{ImageHosting: config.ImageHostingConfig{Host1: "imgbox", Host2: "imgbb"}},
		config.TrackerConfig{},
		repo,
		images,
		api.NopLogger{},
		descriptionAssetsTestRegistry(t),
		nil,
		"imgbox",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.blocking || resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected imgbb fallback after prior imgbox failure, got %#v", resolution)
	}
	if len(images.calls) != 1 || images.calls[0] != "imgbb" {
		t.Fatalf("expected description upload to skip imgbox, got calls %#v", images.calls)
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected every screenshot from fallback host, got %#v", resolution.screenshots)
	}
}

func TestEnsureDescriptionImageHostBlocksWhenAllUploadHostsFail(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}
	images := &stubImageService{
		errs: map[string]error{
			"pixhost": errors.New("pixhost unavailable"),
		},
	}

	resolution, err := ensureDescriptionImageHostWithRegistry(
		context.Background(),
		"PTP",
		meta,
		config.Config{},
		config.TrackerConfig{},
		repo,
		images,
		descriptionAssetsTestRegistry(t),
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost/a.png",
				RawURL:     "https://pixhost/a.png",
				WebURL:     "https://pixhost/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost/b.png",
				RawURL:     "https://pixhost/b.png",
				WebURL:     "https://pixhost/b",
			},
		},
	}
	meta := api.UploadSubject{
		SourcePath: "/tmp/source",
		ImageHostOverrides: api.ImageHostOverrides{
			PreferredHost: &preferredHost,
		},
	}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "OE", meta, config.Config{}, config.TrackerConfig{}, repo, nil, descriptionAssetsTestRegistry(t))
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/a.png",
				RawURL:     "https://hdb/a.png",
				WebURL:     "https://hdb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "hdb",
				UsageScope: "tracker:HDB",
				ImgURL:     "https://hdb/b.png",
				RawURL:     "https://hdb/b.png",
				WebURL:     "https://hdb/b",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				UsageScope: "global",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "OE", meta, config.Config{}, config.TrackerConfig{}, repo, nil, descriptionAssetsTestRegistry(t))
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

func TestEnsureDescriptionImageHostReusesAllowedHostWhenAutomaticUploadDisabled(t *testing.T) {
	skipUpload := true
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/a.png",
				RawURL:     "https://imgbb/a.png",
				WebURL:     "https://imgbb/a",
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Host:       "imgbb",
				ImgURL:     "https://imgbb/b.png",
				RawURL:     "https://imgbb/b.png",
				WebURL:     "https://imgbb/b",
			},
		},
	}
	meta := api.UploadSubject{
		SourcePath: "/tmp/source",
		ImageHostOverrides: api.ImageHostOverrides{
			SkipUpload: &skipUpload,
		},
	}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, &stubImageService{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolution.feedback.Status != "reused" || resolution.feedback.SelectedHost != "imgbb" {
		t.Fatalf("expected imgbb reuse, got %#v", resolution.feedback)
	}
	if resolution.feedback.Reuploaded {
		t.Fatal("expected automatic upload to stay disabled")
	}
	if len(resolution.screenshots) != 2 {
		t.Fatalf("expected two reused screenshots, got %#v", resolution.screenshots)
	}
}

func TestEnsureDescriptionImageHostWarnsOnPartialAllowedHostCoverageWithoutUploader(t *testing.T) {
	repo := &stubRepo{
		selections: []api.ScreenshotFinalSelection{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
		uploads: []api.UploadedImageLink{
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Host:       "pixhost",
				ImgURL:     "https://pixhost/a.png",
				RawURL:     "https://pixhost/a.png",
				WebURL:     "https://pixhost/a",
			},
		},
	}
	meta := api.UploadSubject{SourcePath: "/tmp/source"}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, nil, descriptionAssetsTestRegistry(t))
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
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/a.png",
				Order:      0,
			},
			{
				SourcePath: "/tmp/source",
				ImagePath:  "/tmp/b.png",
				Order:      1,
			},
		},
	}
	meta := api.UploadSubject{MediaBinding: trackerTestMediaBinding("/tmp/source"), SourcePath: "/tmp/source"}
	images := &stubImageService{
		uploads: map[string][]api.UploadedImageLink{
			"pixhost": {
				{
					SourcePath: "/tmp/source",
					ImagePath:  "/tmp/a.png",
					Host:       "pixhost",
					ImgURL:     "https://pixhost/a.png",
					RawURL:     "https://pixhost/a.png",
					WebURL:     "https://pixhost/a",
				},
			},
		},
	}

	resolution, err := ensureDescriptionImageHostWithRegistry(context.Background(), "PTP", meta, config.Config{}, config.TrackerConfig{}, repo, images, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolution.blocking {
		t.Fatalf("expected blocking image host resolution, got %#v", resolution)
	}
	if resolution.feedback.Status != "warning" {
		t.Fatalf("expected warning feedback, got %#v", resolution.feedback)
	}
	if len(resolution.feedback.Warnings) != 1 || !strings.Contains(resolution.feedback.Warnings[0].Message, "missing eligible screenshot variant") {
		t.Fatalf("expected slot selection warning, got %#v", resolution.feedback.Warnings)
	}
	if len(repo.deletedUploads) != 1 {
		t.Fatalf("expected one uploaded image rollback, got %#v", repo.deletedUploads)
	}
	if repo.deletedUploads[0] != "pixhost:/tmp/a.png" {
		t.Fatalf("unexpected rollback target: %#v", repo.deletedUploads)
	}
}
