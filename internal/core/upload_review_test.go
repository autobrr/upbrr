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
	}, api.Request{SkipDupeCheck: true})

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
	}, api.Request{IgnoreDupesFor: []string{"HDB"}})

	if _, ok := meta.BlockedTrackers["HDB"]; ok {
		t.Fatalf("expected HDB dupe block to be cleared, got %#v", meta.BlockedTrackers)
	}
	if got := meta.BlockedTrackers["BHD"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected BHD dupe block to remain, got %#v", meta.BlockedTrackers)
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
	}, api.Request{})

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
	})

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
	})

	if meta.InfoHash != infoHash {
		t.Fatalf("expected infohash override, got %q", meta.InfoHash)
	}
}

func TestBuildUploadReviewMarksBlockedTrackersInDryRun(t *testing.T) {
	t.Parallel()

	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
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
