// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type reviewDupes struct {
	summary api.DupeCheckSummary
}

func (r *reviewDupes) Check(context.Context, api.PreparedMetadata, []string) (api.DupeCheckSummary, error) {
	return r.summary, nil
}

type reviewTrackers struct{}

func (reviewTrackers) Upload(context.Context, api.PreparedMetadata) (api.UploadSummary, error) {
	return api.UploadSummary{}, nil
}

func (reviewTrackers) BuildPreparation(context.Context, api.PreparedMetadata, []string) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (reviewTrackers) BuildUploadDryRun(context.Context, api.PreparedMetadata, []string) ([]api.TrackerDryRunEntry, error) {
	return []api.TrackerDryRunEntry{
		{Tracker: "AITHER", Status: "ready", ReleaseName: "AITHER.NAME"},
		{Tracker: "BLU", Status: "ready", ReleaseName: "BLU.NAME"},
	}, nil
}

func TestBuildUploadReviewIncludesRuleFailuresDupesAndDryRun(t *testing.T) {
	t.Parallel()

	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes: &reviewDupes{summary: api.DupeCheckSummary{
				SourcePath: "/tmp/a",
				Results:    []api.DupeCheckResult{{Tracker: "AITHER", HasDupes: true, Status: "completed", Notes: []string{"possible dupe"}}},
			}},
			Torrents: &stubTorrent{},
			Trackers: reviewTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Trackers:   []string{"AITHER", "BLU"},
		Tag:        "-GROUP",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"BLU": {{Rule: "movie_only", Reason: "movie only"}},
		},
	})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeCLI,
		Trackers: []string{"AITHER", "BLU"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if len(review.Trackers) != 2 {
		t.Fatalf("expected 2 tracker reviews, got %d", len(review.Trackers))
	}
	if !review.Trackers[0].DupeCheck.HasDupes {
		t.Fatalf("expected AITHER dupe result")
	}
	if got := review.Trackers[1].RuleFailures; len(got) != 1 || got[0].Rule != "movie_only" {
		t.Fatalf("expected BLU rule failure, got %#v", got)
	}
	if review.Trackers[0].DryRun.ReleaseName == "" || review.Trackers[1].DryRun.ReleaseName == "" {
		t.Fatalf("expected dry-run release names in review")
	}
}

func TestApplyRequestToPreparedMetaClearsDupeBlocksWhenSkipped(t *testing.T) {
	t.Parallel()

	meta := applyRequestToPreparedMeta(api.PreparedMetadata{
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
		},
	}, api.Request{SkipDupeCheck: true}, config.Config{}, api.NopLogger{})

	if len(meta.BlockedTrackers) != 0 {
		t.Fatalf("expected dupe blocks to be cleared when dupe check is skipped, got %#v", meta.BlockedTrackers)
	}
}

func TestApplyRequestToPreparedMetaClearsDupeBlocksForIgnoredTrackers(t *testing.T) {
	t.Parallel()

	meta := applyRequestToPreparedMeta(api.PreparedMetadata{
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"HDB": {api.TrackerBlockReasonDupe},
			"BHD": {api.TrackerBlockReasonDupe},
		},
	}, api.Request{IgnoreDupesFor: []string{"HDB"}}, config.Config{}, api.NopLogger{})

	if _, ok := meta.BlockedTrackers["HDB"]; ok {
		t.Fatalf("expected HDB dupe block to be cleared, got %#v", meta.BlockedTrackers)
	}
	if got := meta.BlockedTrackers["BHD"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected BHD dupe block to remain, got %#v", meta.BlockedTrackers)
	}
}

func TestApplyDupeSummaryBlocksSkippedAndFailedTrackers(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{}
	applyDupeSummaryToPreparedMeta(&meta, api.DupeCheckSummary{
		Results: []api.DupeCheckResult{
			{Tracker: "BTN", Skipped: true, SkipReason: "BTN only supports TV dupe search"},
			{Tracker: "RTF", Status: "failed", Error: "dupe search failed"},
			{Tracker: "OE", Status: "completed"},
		},
	})

	if got := meta.BlockedTrackers["BTN"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected BTN skipped dupe block, got %#v", meta.BlockedTrackers)
	}
	if got := meta.BlockedTrackers["RTF"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected RTF failed dupe block, got %#v", meta.BlockedTrackers)
	}
	if _, ok := meta.BlockedTrackers["OE"]; ok {
		t.Fatalf("expected completed OE dupe check not to block, got %#v", meta.BlockedTrackers)
	}
}

func TestApplyRequestToPreparedMetaPreservesCachedDescriptionGroupsWhenRequestOmitted(t *testing.T) {
	t.Parallel()

	meta := applyRequestToPreparedMeta(api.PreparedMetadata{
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"BLU"},
			RawDescription: "cached body",
			HasOverride:    true,
		}},
	}, api.Request{}, config.Config{}, api.NopLogger{})

	if len(meta.DescriptionGroups) != 1 {
		t.Fatalf("expected cached description groups to be preserved, got %d", len(meta.DescriptionGroups))
	}
	if meta.DescriptionGroups[0].GroupKey != "unit3d" {
		t.Fatalf("expected cached group key to be preserved, got %q", meta.DescriptionGroups[0].GroupKey)
	}
	if meta.DescriptionGroups[0].RawDescription != "cached body" {
		t.Fatalf("expected cached group body to be preserved, got %q", meta.DescriptionGroups[0].RawDescription)
	}
	if !meta.DescriptionGroups[0].HasOverride {
		t.Fatalf("expected cached override flag to be preserved")
	}
}

func TestApplyRequestToPreparedMetaAppliesMetadataOverrides(t *testing.T) {
	t.Parallel()

	distributor := "Criterion"
	trueValue := true
	falseValue := false

	meta := applyRequestToPreparedMeta(api.PreparedMetadata{}, api.Request{
		MetadataOverrides: api.MetadataOverrides{
			Distributor:     &distributor,
			PersonalRelease: &trueValue,
			Commentary:      &trueValue,
			WebDV:           &trueValue,
			StreamOptimized: &trueValue,
			Anime:           &falseValue,
		},
	}, config.Config{}, api.NopLogger{})

	if meta.Distributor != "Criterion" {
		t.Fatalf("expected distributor override, got %q", meta.Distributor)
	}
	if !meta.PersonalRelease {
		t.Fatalf("expected personal release override")
	}
	if !meta.HasCommentary {
		t.Fatalf("expected commentary override")
	}
	if !meta.WebDV {
		t.Fatalf("expected webdv override")
	}
	if meta.StreamOptimized != 1 {
		t.Fatalf("expected stream override to set 1, got %d", meta.StreamOptimized)
	}
	if meta.Anime {
		t.Fatalf("expected anime override to set false")
	}
}

func TestApplyRequestToPreparedMetaAppliesTorrentOverrides(t *testing.T) {
	t.Parallel()

	infoHash := "abcdef0123456789abcdef0123456789abcdef01"
	meta := applyRequestToPreparedMeta(api.PreparedMetadata{}, api.Request{
		TorrentOverrides: api.TorrentOverrides{
			InfoHash: &infoHash,
		},
	}, config.Config{}, api.NopLogger{})

	if meta.InfoHash != infoHash {
		t.Fatalf("expected infohash override, got %q", meta.InfoHash)
	}
}

func TestApplyRequestToPreparedMetaRecomputesAudioPolicyForOverridesAndTrackers(t *testing.T) {
	t.Parallel()

	originalLanguage := "ja"
	meta := applyRequestToPreparedMeta(api.PreparedMetadata{
		Audio:          "Dubbed DD 5.1",
		AudioLanguages: []string{"English", "Japanese", "French"},
		Trackers:       []string{"ANT"},
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"ANT": {api.TrackerBlockReasonAudio},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"ANT": {{Rule: "audio_bloat", Reason: "stale"}},
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
		},
	}, api.Request{
		Trackers: []string{"SPD"},
		MetadataOverrides: api.MetadataOverrides{
			OriginalLanguage: &originalLanguage,
		},
	}, config.Config{}, api.NopLogger{})

	if meta.Audio != "Dual-Audio DD 5.1" {
		t.Fatalf("expected audio label to be recomputed, got %q", meta.Audio)
	}
	if _, ok := meta.BlockedTrackers["ANT"]; ok {
		t.Fatalf("expected stale ANT audio block to be cleared, got %#v", meta.BlockedTrackers)
	}
	if got := meta.TrackerRuleFailures["ANT"]; len(got) != 0 {
		t.Fatalf("expected stale ANT audio rule failure to be cleared, got %#v", meta.TrackerRuleFailures)
	}
	if got := meta.TrackerRuleFailures["SPD"]; len(got) != 0 {
		t.Fatalf("expected no SPD audio rule failure, got %#v", meta.TrackerRuleFailures)
	}
}

func TestBuildUploadReviewMarksBlockedTrackersInDryRun(t *testing.T) {
	t.Parallel()

	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   reviewTrackers{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Trackers:   []string{"AITHER"},
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"AITHER": {api.TrackerBlockReasonClaim},
		},
	})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeCLI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if len(review.Trackers) != 1 {
		t.Fatalf("expected 1 tracker review, got %d", len(review.Trackers))
	}
	if review.Trackers[0].DryRun.Status != "blocked" {
		t.Fatalf("expected blocked dry-run status, got %#v", review.Trackers[0].DryRun)
	}
	if !strings.Contains(review.Trackers[0].DryRun.Message, "claim") {
		t.Fatalf("expected blocked dry-run message to mention claim, got %#v", review.Trackers[0].DryRun)
	}
}

func TestGetGUICachedMetaFallsBackWhenNonExternalSignatureMismatch(t *testing.T) {
	t.Parallel()

	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})
	if cached, ok := coreSvc.getGUICachedMeta("/tmp/a", "originalLanguage=ja", api.ExternalIDOverrides{}); !ok {
		t.Fatalf("expected signed GUI cache lookup to reuse non-external cached metadata")
	} else if cached.SourcePath != "/tmp/a" {
		t.Fatalf("expected cached metadata source path, got %q", cached.SourcePath)
	}
}

func TestGetGUICachedMetaReusesSignedEntryForEmptySignatureWhenNonExternal(t *testing.T) {
	t.Parallel()

	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}

	coreSvc.storeDupeCache("/tmp/a", "originalLanguage=ja", api.PreparedMetadata{SourcePath: "/tmp/a"})
	if cached, ok := coreSvc.getGUICachedMeta("/tmp/a", "", api.ExternalIDOverrides{}); !ok {
		t.Fatalf("expected unsigned GUI cache lookup to reuse signed non-external cached metadata")
	} else if cached.SourcePath != "/tmp/a" {
		t.Fatalf("expected cached metadata source path, got %q", cached.SourcePath)
	}
}

func TestApplyRequestToPreparedMetaDoesNotMutateCachedExternalMetadata(t *testing.T) {
	t.Parallel()

	original := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
		},
	}
	updatedLanguage := "ja"
	result := applyRequestToPreparedMeta(original, api.Request{
		MetadataOverrides: api.MetadataOverrides{OriginalLanguage: &updatedLanguage},
	}, config.Config{}, api.NopLogger{})

	if original.ExternalMetadata.TMDB == nil || original.ExternalMetadata.TMDB.OriginalLanguage != "en" {
		t.Fatalf("expected cached metadata to remain unchanged, got %#v", original.ExternalMetadata)
	}
	if result.ExternalMetadata.TMDB == nil || result.ExternalMetadata.TMDB.OriginalLanguage != "ja" {
		t.Fatalf("expected request-scoped metadata override to apply, got %#v", result.ExternalMetadata)
	}
}

func TestMergeTrackerIDOverridesNormalizesExistingKeys(t *testing.T) {
	t.Parallel()

	spacedExistingKey := " AITHER "
	spacedOverrideKey := " RF "
	existing := map[string]string{
		spacedExistingKey: " 10001 ",
		"RF":              " ",
		"":                "10002",
	}
	overrides := map[string]string{
		"aither":          "20001",
		spacedOverrideKey: " 20002 ",
	}

	merged := mergeTrackerIDOverrides(existing, overrides)

	if len(merged) != 2 {
		t.Fatalf("expected two normalized tracker ids, got %#v", merged)
	}
	if got := merged["aither"]; got != "20001" {
		t.Fatalf("expected override to replace normalized existing id, got %q", got)
	}
	if got := merged["rf"]; got != "20002" {
		t.Fatalf("expected normalized override id, got %q", got)
	}
	if _, ok := merged[" AITHER "]; ok {
		t.Fatalf("expected unnormalized existing key removed, got %#v", merged)
	}
}
