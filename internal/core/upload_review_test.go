// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"reflect"
	"slices"
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

type recordingReviewTrackers struct {
	calls        int
	lastTrackers []string
	entries      []api.TrackerDryRunEntry
	err          error
}

func (*recordingReviewTrackers) Upload(context.Context, api.PreparedMetadata) (api.UploadSummary, error) {
	return api.UploadSummary{}, nil
}

func (*recordingReviewTrackers) BuildPreparation(context.Context, api.PreparedMetadata, []string) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (r *recordingReviewTrackers) BuildUploadDryRun(_ context.Context, _ api.PreparedMetadata, trackers []string) ([]api.TrackerDryRunEntry, error) {
	r.calls++
	r.lastTrackers = append([]string{}, trackers...)
	if r.err != nil {
		return nil, r.err
	}
	if len(r.entries) == 0 {
		return []api.TrackerDryRunEntry{}, nil
	}
	return append([]api.TrackerDryRunEntry(nil), r.entries...), nil
}

type recordingReviewTorrent struct {
	calls int
	err   error
}

func (r *recordingReviewTorrent) Create(context.Context, api.PreparedMetadata) (api.TorrentResult, error) {
	r.calls++
	if r.err != nil {
		return api.TorrentResult{}, r.err
	}
	return api.TorrentResult{Path: "/tmp/file.torrent"}, nil
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

func TestApplyRequestToPreparedMetaClearsIgnoredDupeRemovalState(t *testing.T) {
	t.Parallel()

	meta := applyRequestToPreparedMeta(api.PreparedMetadata{
		TrackersRemove:  []string{"AiThEr", "BLU"},
		MatchedTrackers: []string{"aither", "BLU"},
	}, api.Request{
		IgnoreDupesFor: []string{"AITHER"},
	}, config.Config{}, api.NopLogger{})

	if !reflect.DeepEqual(meta.TrackersRemove, []string{"BLU"}) {
		t.Fatalf("expected ignored duplicate removal cleared and unignored removal preserved, got %#v", meta.TrackersRemove)
	}
	if !reflect.DeepEqual(meta.MatchedTrackers, []string{"BLU"}) {
		t.Fatalf("expected ignored duplicate match cleared and unignored match preserved, got %#v", meta.MatchedTrackers)
	}
}

func TestApplyRequestToPreparedMetaPreservesRequestedRemovalForIgnoredDupe(t *testing.T) {
	t.Parallel()

	meta := applyRequestToPreparedMeta(api.PreparedMetadata{
		TrackersRemove:  []string{"AITHER"},
		MatchedTrackers: []string{"AITHER"},
	}, api.Request{
		TrackersRemove: []string{"AITHER"},
		IgnoreDupesFor: []string{"AITHER"},
	}, config.Config{}, api.NopLogger{})

	if !reflect.DeepEqual(meta.TrackersRemove, []string{"AITHER"}) {
		t.Fatalf("expected requested removal to remain authoritative, got %#v", meta.TrackersRemove)
	}
	if len(meta.MatchedTrackers) != 0 {
		t.Fatalf("expected ignored duplicate match cleared, got %#v", meta.MatchedTrackers)
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

func TestApplyDupeSummaryUsesStructuredCZTAPIKeyOnlySkip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		results     []api.DupeCheckResult
		wantBlocked map[string]bool
	}{
		{
			name: "czt api key only skip with changed wording does not block",
			results: []api.DupeCheckResult{{
				Tracker:    "CZT",
				Skipped:    true,
				SkipReason: "CZT search skipped because upload auth uses an API token",
				SkipCode:   api.DupeSkipCodeCZTAPIKeyOnly,
			}},
			wantBlocked: map[string]bool{"CZT": false},
		},
		{
			name: "czt skip without structured code blocks",
			results: []api.DupeCheckResult{{
				Tracker:    "CZT",
				Skipped:    true,
				SkipReason: "missing passkey for tracker",
			}},
			wantBlocked: map[string]bool{"CZT": true},
		},
		{
			name: "non czt skip with czt-like wording still blocks",
			results: []api.DupeCheckResult{{
				Tracker:    "BLU",
				Skipped:    true,
				SkipReason: "requires passkey; API key upload token is not supported by the search API",
			}},
			wantBlocked: map[string]bool{"BLU": true},
		},
		{
			name: "grouped czt api key only skip splits decisions per tracker",
			results: []api.DupeCheckResult{{
				Tracker:    "CZT, HDB",
				Skipped:    true,
				SkipReason: "CZT search skipped because upload auth uses an API token",
				SkipCode:   api.DupeSkipCodeCZTAPIKeyOnly,
			}},
			wantBlocked: map[string]bool{"CZT": false, "HDB": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := api.PreparedMetadata{}
			applyDupeSummaryToPreparedMeta(&meta, api.DupeCheckSummary{Results: tc.results})

			for tracker, wantBlocked := range tc.wantBlocked {
				got := len(meta.BlockedTrackers[tracker]) > 0
				if got != wantBlocked {
					t.Fatalf("blocked[%s]=%t, want %t; blocks=%#v", tracker, got, wantBlocked, meta.BlockedTrackers)
				}
			}
		})
	}
}

func TestApplyDupeSummaryStoresMatchedDownloadsForCrossSeedInjection(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{}
	applyDupeSummaryToPreparedMeta(&meta, api.DupeCheckSummary{
		Results: []api.DupeCheckResult{
			{
				Tracker:  "HDB",
				HasDupes: true,
				Match: api.DupeMatch{
					MatchedID:       "123",
					MatchedLink:     "https://hdbits.org/details.php?id=123",
					MatchedDownload: "https://hdbits.org/download.php/file?id=123&passkey=abc",
				},
			},
			{Tracker: "BTN", Skipped: true},
		},
	})

	if len(meta.CrossSeedTorrents) != 1 {
		t.Fatalf("expected one cross-seed torrent, got %#v", meta.CrossSeedTorrents)
	}
	crossSeed := meta.CrossSeedTorrents[0]
	if crossSeed.Tracker != "HDB" {
		t.Fatalf("expected HDB cross-seed tracker, got %q", crossSeed.Tracker)
	}
	if crossSeed.DownloadURL != "https://hdbits.org/download.php/file?id=123&passkey=abc" {
		t.Fatalf("expected matched download URL, got %q", crossSeed.DownloadURL)
	}
	if crossSeed.TorrentID != "123" || crossSeed.TorrentURL != "https://hdbits.org/details.php?id=123" {
		t.Fatalf("expected matched metadata to be preserved, got %#v", crossSeed)
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

func TestBuildUploadReviewReturnsEmptyWhenSelectedTrackersResolveEmpty(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{{Tracker: "BLU", Status: "ready"}},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{
		SourcePath:     "/tmp/a",
		TrackersRemove: []string{"AITHER"},
	})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeCLI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if trackersSvc.calls != 0 {
		t.Fatalf("expected dry-run to be skipped, got %d calls", trackersSvc.calls)
	}
	if len(review.Trackers) != 0 {
		t.Fatalf("expected no tracker reviews, got %#v", review.Trackers)
	}
}

func TestBuildUploadReviewAddsDefaultsForExplicitTrackersOutsideGUI(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{
			{Tracker: "BLU", Status: "ready"},
			{Tracker: "AITHER", Status: "ready"},
		},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{TMDBAPI: "x"},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
			Trackers:           config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
		},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeCLI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if !slices.Equal(trackersSvc.lastTrackers, []string{"BLU", "AITHER"}) {
		t.Fatalf("expected default plus explicit trackers, got %v", trackersSvc.lastTrackers)
	}
	if got := []string{review.Trackers[0].Tracker, review.Trackers[1].Tracker}; !slices.Equal(got, []string{"BLU", "AITHER"}) {
		t.Fatalf("expected review rows for default plus explicit trackers, got %v", got)
	}
}

func TestBuildUploadReviewFallsBackToDefaultsWhenExplicitTrackersRemovedOutsideGUI(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{{Tracker: "BLU", Status: "ready"}},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{TMDBAPI: "x"},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
			Trackers:           config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
		},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{
		SourcePath:     "/tmp/a",
		TrackersRemove: []string{"AITHER"},
	})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeCLI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if !slices.Equal(trackersSvc.lastTrackers, []string{"BLU"}) {
		t.Fatalf("expected default tracker after explicit tracker removal, got %v", trackersSvc.lastTrackers)
	}
	if len(review.Trackers) != 1 || review.Trackers[0].Tracker != "BLU" {
		t.Fatalf("expected one BLU review row, got %#v", review.Trackers)
	}
}

func TestBuildUploadReviewPreservesSiteUploadSingleton(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{{Tracker: "AITHER", Status: "ready"}},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{TMDBAPI: "x"},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
			Trackers:           config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
		},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{SourcePath: "/tmp/a"})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeCLI,
		Execution: api.ExecutionOptions{
			SiteUploadTracker: "AITHER",
		},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if !slices.Equal(trackersSvc.lastTrackers, []string{"AITHER"}) {
		t.Fatalf("expected singleton site-upload tracker, got %v", trackersSvc.lastTrackers)
	}
	if len(review.Trackers) != 1 || review.Trackers[0].Tracker != "AITHER" {
		t.Fatalf("expected one AITHER review row, got %#v", review.Trackers)
	}
}

func TestBuildUploadReviewPartialGUIReviewPreservesOmittedCacheState(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{{Tracker: "AITHER", Status: "ready", ReleaseName: "AITHER.NAME"}},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes: &reviewDupes{summary: api.DupeCheckSummary{
				SourcePath: "/tmp/a",
				Results: []api.DupeCheckResult{{
					Tracker:  "AITHER",
					HasDupes: true,
					Status:   "completed",
					Match: api.DupeMatch{
						MatchedID:       "200",
						MatchedLink:     "https://aither.cc/torrents/200",
						MatchedDownload: "https://aither.cc/download/200.torrent",
					},
				}},
			}},
			Torrents: &stubTorrent{},
			Trackers: trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	coreSvc.storeDupeCache("/tmp/a", "", api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Trackers:   []string{"AITHER", "BLU"},
		TrackersRemove: []string{
			"BLU",
		},
		MatchedTrackers: []string{
			"BLU",
		},
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"BLU": {api.TrackerBlockReasonDupe},
		},
		CrossSeedTorrents: []api.UploadedTorrent{{
			Tracker:     "BLU",
			TorrentID:   "100",
			DownloadURL: "https://blu/download/100.torrent",
			TorrentURL:  "https://blu/torrents/100",
		}},
	})

	review, err := coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	if len(review.Trackers) != 1 || review.Trackers[0].Tracker != "AITHER" {
		t.Fatalf("expected one AITHER review row, got %#v", review.Trackers)
	}

	exported, ok, err := coreSvc.ExportGUICachedPreparedMeta(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	})
	if err != nil {
		t.Fatalf("export gui cached prepared meta: %v", err)
	}
	if !ok {
		t.Fatal("expected exported cached metadata after partial review")
	}
	if got := exported.BlockedTrackers["BLU"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected BLU dupe block to survive, got %#v", exported.BlockedTrackers)
	}
	if got := exported.BlockedTrackers["AITHER"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected AITHER dupe block to update, got %#v", exported.BlockedTrackers)
	}
	if !reflect.DeepEqual(exported.TrackersRemove, []string{"BLU"}) {
		t.Fatalf("expected reviewed tracker removals cleared and unreviewed removals preserved, got %#v", exported.TrackersRemove)
	}
	if !reflect.DeepEqual(exported.MatchedTrackers, []string{"BLU"}) {
		t.Fatalf("expected reviewed tracker matches cleared and unreviewed matches preserved, got %#v", exported.MatchedTrackers)
	}
	if len(exported.CrossSeedTorrents) != 2 {
		t.Fatalf("expected both cross-seed torrents to survive, got %#v", exported.CrossSeedTorrents)
	}

	runCore, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new run core: %v", err)
	}
	if err := runCore.ImportPreparedMetadataForGUI(context.Background(), api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, exported); err != nil {
		t.Fatalf("import prepared metadata for gui: %v", err)
	}
	imported, ok := runCore.lookupGUICachedMeta(api.Request{
		Paths: []string{"/tmp/a"},
		Mode:  api.ModeGUI,
	}, "/tmp/a")
	if !ok {
		t.Fatal("expected imported cached metadata")
	}
	if got := imported.BlockedTrackers["BLU"]; len(got) != 1 || got[0] != api.TrackerBlockReasonDupe {
		t.Fatalf("expected imported BLU dupe block to survive, got %#v", imported.BlockedTrackers)
	}
	if len(imported.CrossSeedTorrents) != 2 {
		t.Fatalf("expected imported cross-seed torrents to survive, got %#v", imported.CrossSeedTorrents)
	}
}

func TestMergeUploadReviewCacheMetaRefreshesReviewedTrackerStateWithMixedCaseBaseKeys(t *testing.T) {
	t.Parallel()

	base := api.PreparedMetadata{
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"AiThEr": {api.TrackerBlockReasonDupe},
			"BLU":    {api.TrackerBlockReasonClaim},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AiThEr": {{Rule: "old_aither", Reason: "stale"}},
			"aither": {{Rule: "older_aither", Reason: "staler"}},
			"BLU":    {{Rule: "old_blu", Reason: "keep"}},
		},
		TrackersRemove:  []string{"AiThEr", "BLU"},
		MatchedTrackers: []string{"AiThEr", "BLU"},
		CrossSeedTorrents: []api.UploadedTorrent{
			{Tracker: "AiThEr", TorrentID: "1"},
			{Tracker: "BLU", TorrentID: "2"},
		},
	}
	updated := api.PreparedMetadata{
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"aItHeR": {api.TrackerBlockReasonAudio, api.TrackerBlockReasonDupe},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"aither": {{Rule: "new_aither", Reason: "fresh"}},
		},
		TrackersRemove: []string{"aither"},
		CrossSeedTorrents: []api.UploadedTorrent{
			{Tracker: "AITHER", TorrentID: "3"},
		},
	}

	merged := mergeUploadReviewCacheMeta(base, updated, []string{"AITHER"})

	if got := merged.BlockedTrackers["AITHER"]; !reflect.DeepEqual(got, []api.TrackerBlockReason{api.TrackerBlockReasonAudio, api.TrackerBlockReasonDupe}) {
		t.Fatalf("expected reviewed tracker blocks to refresh, got %#v", got)
	}
	if _, ok := merged.BlockedTrackers["AiThEr"]; ok {
		t.Fatalf("expected mixed-case reviewed tracker block key removed, got %#v", merged.BlockedTrackers)
	}
	if got := merged.BlockedTrackers["BLU"]; !reflect.DeepEqual(got, []api.TrackerBlockReason{api.TrackerBlockReasonClaim}) {
		t.Fatalf("expected unreviewed tracker blocks to remain, got %#v", got)
	}
	if got := merged.TrackerRuleFailures["AITHER"]; !reflect.DeepEqual(got, []api.RuleFailure{{Rule: "new_aither", Reason: "fresh"}}) {
		t.Fatalf("expected reviewed tracker rule failures to refresh, got %#v", got)
	}
	if _, ok := merged.TrackerRuleFailures["AiThEr"]; ok {
		t.Fatalf("expected mixed-case reviewed tracker rule failure key removed, got %#v", merged.TrackerRuleFailures)
	}
	if _, ok := merged.TrackerRuleFailures["aither"]; ok {
		t.Fatalf("expected duplicate reviewed tracker rule failure key removed, got %#v", merged.TrackerRuleFailures)
	}
	if got := merged.TrackerRuleFailures["BLU"]; !reflect.DeepEqual(got, []api.RuleFailure{{Rule: "old_blu", Reason: "keep"}}) {
		t.Fatalf("expected unreviewed tracker rule failures to remain, got %#v", got)
	}
	if !reflect.DeepEqual(merged.TrackersRemove, []string{"BLU", "AITHER"}) {
		t.Fatalf("expected reviewed tracker removals to refresh and unreviewed removals to remain, got %#v", merged.TrackersRemove)
	}
	if !reflect.DeepEqual(merged.MatchedTrackers, []string{"BLU"}) {
		t.Fatalf("expected reviewed matched tracker cleared and unreviewed match preserved, got %#v", merged.MatchedTrackers)
	}
	if len(merged.CrossSeedTorrents) != 2 {
		t.Fatalf("expected merged cross-seed torrents, got %#v", merged.CrossSeedTorrents)
	}
	if len(base.CrossSeedTorrents) != 2 || len(updated.CrossSeedTorrents) != 1 {
		t.Fatalf("expected merge to avoid aliasing, base=%#v updated=%#v", base.CrossSeedTorrents, updated.CrossSeedTorrents)
	}
	coreSvc := &Core{logger: api.NopLogger{}}
	eligible := mergeUploadReviewCacheMeta(base, api.PreparedMetadata{}, []string{"AITHER"})
	if got := coreSvc.filterImageUploadTrackers([]string{"AITHER", "BLU"}, eligible); !reflect.DeepEqual(got, []string{"AITHER"}) {
		t.Fatalf("expected reviewed tracker image upload eligibility restored after merge, got %#v", got)
	}
}

func TestBuildUploadReviewCommitsReviewedTrackerUpdateFromMatchingRefreshedCache(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{
			{Tracker: "AITHER", Status: "ready"},
			{Tracker: "BLU", Status: "ready"},
		},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	initial := api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Mode:       api.ModeGUI,
		Options: api.UploadOptions{
			InteractionMode: api.InteractionModeInteractive,
			Screens:         1,
		},
		Trackers: []string{"BLU", "AITHER"},
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"AITHER": {api.TrackerBlockReasonDupe},
			"BLU":    {api.TrackerBlockReasonDupe},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"BLU": {{Rule: "old_blu", Reason: "keep"}},
		},
		CrossSeedTorrents: []api.UploadedTorrent{
			{Tracker: "AITHER", TorrentID: "1", DownloadURL: "https://dupes/aither"},
			{Tracker: "BLU", TorrentID: "2", DownloadURL: "https://dupes/blu"},
		},
	}
	coreSvc.storeRefreshedDupeCache("/tmp/a", "", initial)

	_, err = coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"aither", "blu"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	entry, _, ok := coreSvc.lookupGUICachedMetaEntry(api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"aither", "blu"},
	}, "/tmp/a")
	if !ok {
		t.Fatal("expected gui cache entry")
	}
	if entry.requestRefreshed {
		t.Fatal("expected matching refreshed cache entry to commit as stable cache")
	}
	if got := entry.meta.BlockedTrackers["AITHER"]; len(got) != 0 {
		t.Fatalf("expected reviewed tracker dupe block cleared, got %#v", entry.meta.BlockedTrackers)
	}
}

func TestBuildUploadReviewSkipsCacheCommitWhenRefreshedBaseDoesNotMatchRequest(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{{Tracker: "BLU", Status: "ready"}},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	initial := api.PreparedMetadata{
		SourcePath: "/tmp/a",
		Mode:       api.ModeGUI,
		Options: api.UploadOptions{
			InteractionMode: api.InteractionModeInteractive,
			Screens:         1,
		},
		Trackers: []string{"AITHER"},
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"BLU": {api.TrackerBlockReasonDupe},
		},
	}
	coreSvc.storeRefreshedDupeCache("/tmp/a", "", initial)

	_, err = coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"BLU"},
	})
	if err != nil {
		t.Fatalf("build upload review: %v", err)
	}
	entry, _, ok := coreSvc.lookupGUICachedMetaEntry(api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"BLU"},
	}, "/tmp/a")
	if !ok {
		t.Fatal("expected gui cache entry")
	}
	if !entry.requestRefreshed {
		t.Fatal("expected mismatched refreshed cache entry to remain request-refreshed")
	}
	if !reflect.DeepEqual(entry.meta, initial) {
		t.Fatalf("expected mismatched refreshed cache entry unchanged, got %#v", entry.meta)
	}
}

func TestBuildUploadReviewDoesNotCorruptCacheWhenTorrentCreateFails(t *testing.T) {
	t.Parallel()

	torrentSvc := &recordingReviewTorrent{err: errors.New("torrent create failed")}
	trackersSvc := &recordingReviewTrackers{
		entries: []api.TrackerDryRunEntry{{Tracker: "AITHER", Status: "ready"}},
	}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   torrentSvc,
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	initial := api.PreparedMetadata{SourcePath: "/tmp/a", Trackers: []string{"AITHER"}}
	coreSvc.storeRefreshedDupeCache("/tmp/a", "", initial)

	_, err = coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err == nil || !strings.Contains(err.Error(), "torrent create failed") {
		t.Fatalf("expected torrent create failure, got %v", err)
	}
	entry, _, ok := coreSvc.lookupGUICachedMetaEntry(api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	}, "/tmp/a")
	if !ok {
		t.Fatal("expected gui cache entry")
	}
	if !reflect.DeepEqual(entry.meta, initial) {
		t.Fatalf("expected cache entry unchanged after torrent failure, got %#v", entry.meta)
	}
}

func TestBuildUploadReviewDoesNotCorruptCacheWhenDryRunFails(t *testing.T) {
	t.Parallel()

	trackersSvc := &recordingReviewTrackers{err: errors.New("dry-run failed")}
	coreSvc, err := New(api.CoreDependencies{
		Config: config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "x"}, ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		Services: api.ServiceSet{
			Filesystem: &stubFS{},
			Metadata:   &stubMeta{},
			Dupes:      &reviewDupes{},
			Torrents:   &stubTorrent{},
			Trackers:   trackersSvc,
		},
		Repository: &stubRepo{},
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	initial := api.PreparedMetadata{SourcePath: "/tmp/a", Trackers: []string{"AITHER"}}
	coreSvc.storeRefreshedDupeCache("/tmp/a", "", initial)

	_, err = coreSvc.BuildUploadReview(context.Background(), api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	})
	if err == nil || !strings.Contains(err.Error(), "dry-run failed") {
		t.Fatalf("expected dry-run failure, got %v", err)
	}
	entry, _, ok := coreSvc.lookupGUICachedMetaEntry(api.Request{
		Paths:    []string{"/tmp/a"},
		Mode:     api.ModeGUI,
		Trackers: []string{"AITHER"},
	}, "/tmp/a")
	if !ok {
		t.Fatal("expected gui cache entry")
	}
	if !reflect.DeepEqual(entry.meta, initial) {
		t.Fatalf("expected cache entry unchanged after dry-run failure, got %#v", entry.meta)
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
