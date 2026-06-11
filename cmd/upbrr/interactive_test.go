// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRunInteractiveCLIPathReturnsNilAfterSuccessfulUpload(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{
		review: api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	err := runInteractiveCLIPath(context.Background(), coreSvc, nil, cliOptions{Unattended: true}, map[string]bool{}, "movie.mkv", 1, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
	})
	if err != nil {
		t.Fatalf("runInteractiveCLIPath: %v", err)
	}
	if coreSvc.runUploadPreparedCalls != 1 {
		t.Fatalf("expected one prepared upload, got %d", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunInteractiveCLIPathHandlesScreenshotsBeforeReview(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{
		screenshotPlan: api.ScreenshotPlan{
			SuggestedSelections: []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}},
		},
		screenshotResult: api.ScreenshotResult{
			Images: []api.ScreenshotImage{{Index: 1, TimestampSeconds: 60, Path: "screen1.png"}},
		},
		review: api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	err := runInteractiveCLIPath(context.Background(), coreSvc, nil, cliOptions{Unattended: true}, map[string]bool{}, "movie.mkv", 1, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
	})
	if err != nil {
		t.Fatalf("runInteractiveCLIPath: %v", err)
	}
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,dupes,screenshot-plan,generate-screenshots,save-screenshots,review" {
		t.Fatalf("expected screenshots before review, got %s", got)
	}
	if len(coreSvc.savedFinalImages) != 1 || coreSvc.savedFinalImages[0].Path != "screen1.png" {
		t.Fatalf("expected generated final screenshot saved, got %#v", coreSvc.savedFinalImages)
	}
}

func TestRunInteractiveCLIPathDryRunSkipsScreenshotSideEffects(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{
		screenshotPlan: api.ScreenshotPlan{
			SuggestedSelections: []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}},
		},
		screenshotResult: api.ScreenshotResult{
			Images: []api.ScreenshotImage{{Index: 1, TimestampSeconds: 60, Path: "screen1.png"}},
		},
		review: api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	err := runInteractiveCLIPath(context.Background(), coreSvc, nil, cliOptions{Unattended: true, DryRun: true}, map[string]bool{}, "movie.mkv", 1, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
	})
	if err != nil {
		t.Fatalf("runInteractiveCLIPath: %v", err)
	}
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,dupes,review" {
		t.Fatalf("expected dry-run to skip screenshot side effects, got %s", got)
	}
	if len(coreSvc.savedFinalImages) != 0 {
		t.Fatalf("expected dry-run to skip saved screenshots, got %#v", coreSvc.savedFinalImages)
	}
	if coreSvc.runUploadPreparedCalls != 0 {
		t.Fatalf("expected dry-run to skip upload, got %d", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunInteractiveCLIPathDebugSkipsScreenshotSideEffects(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{
		screenshotPlan: api.ScreenshotPlan{
			SuggestedSelections: []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}},
		},
		screenshotResult: api.ScreenshotResult{
			Images: []api.ScreenshotImage{{Index: 1, TimestampSeconds: 60, Path: "screen1.png"}},
		},
		review: api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	err := runInteractiveCLIPath(context.Background(), coreSvc, nil, cliOptions{Unattended: true, Debug: true}, map[string]bool{}, "movie.mkv", 1, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}},
	})
	if err != nil {
		t.Fatalf("runInteractiveCLIPath: %v", err)
	}
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,dupes,review" {
		t.Fatalf("expected debug to skip screenshot side effects, got %s", got)
	}
	if len(coreSvc.savedFinalImages) != 0 {
		t.Fatalf("expected debug to skip saved screenshots, got %#v", coreSvc.savedFinalImages)
	}
	if coreSvc.runUploadPreparedCalls != 0 {
		t.Fatalf("expected debug to skip upload, got %d", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunInteractiveCLIPathUsesResolvedPreviewSourceForPreparedUpload(t *testing.T) {
	t.Parallel()

	rehash := true
	coreSvc := &cliCoreForTest{
		previewSourcePath: filepath.Join("folder", "movie.mkv"),
		review:            api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	err := runInteractiveCLIPath(
		context.Background(),
		coreSvc,
		nil,
		cliOptions{Unattended: true, Rehash: true},
		map[string]bool{"rehash": true},
		"folder",
		1,
		config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"BLU"}}},
	)
	if err != nil {
		t.Fatalf("runInteractiveCLIPath: %v", err)
	}

	expectedPath := filepath.Join("folder", "movie.mkv")
	for _, call := range coreSvc.requests {
		if call.name == "preview" {
			continue
		}
		if len(call.req.Paths) != 1 || call.req.Paths[0] != expectedPath {
			t.Fatalf("expected %s to use resolved preview source %q, got %#v", call.name, expectedPath, call.req.Paths)
		}
		if call.req.TorrentOverrides.Rehash == nil || *call.req.TorrentOverrides.Rehash != rehash {
			t.Fatalf("expected %s to preserve rehash override, got %#v", call.name, call.req.TorrentOverrides.Rehash)
		}
	}
}

func TestRunSiteCheckCLIPathSeedsMetadataBeforeReview(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{previewSourcePath: filepath.Join("folder", "movie.mkv")}
	if err := runSiteCheckCLIPath(context.Background(), coreSvc, cliOptions{SiteCheck: true}, map[string]bool{}, "movie.mkv", 1); err != nil {
		t.Fatalf("runSiteCheckCLIPath: %v", err)
	}
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,review" {
		t.Fatalf("expected preview before review, got %s", got)
	}
	if len(coreSvc.requests) != 2 || len(coreSvc.requests[1].req.Paths) != 1 || coreSvc.requests[1].req.Paths[0] != filepath.Join("folder", "movie.mkv") {
		t.Fatalf("expected site-check review to use resolved preview source, got %#v", coreSvc.requests)
	}
}

func TestResolveCLIUploadTrackersExplicitTrackersSuppressDefaults(t *testing.T) {
	t.Parallel()

	selected, removalBase := resolveCLIUploadTrackers(
		map[string]bool{"trackers": true},
		api.Request{
			Trackers: []string{"BLU"},
			Options:  api.UploadOptions{InteractionMode: api.InteractionModeInteractive},
		},
		api.MetadataPreview{},
		config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER", "BLU"}}},
	)
	if len(selected) != 1 || selected[0] != "BLU" {
		t.Fatalf("expected explicit BLU selection, got %#v", selected)
	}
	if got := unselectedTrackers(removalBase, selected); len(got) != 1 || got[0] != "AITHER" {
		t.Fatalf("expected AITHER removal from defaults, got %#v", got)
	}
}

func TestPromptTrackerDupeReviewBuildsConfirmedTrackerList(t *testing.T) {
	t.Parallel()

	approved, ignoreDupes, ruleOverrides, err := promptTrackerDupeReview(
		bufio.NewReader(strings.NewReader("y\nn\nn\n")),
		api.DupeCheckSummary{Results: []api.DupeCheckResult{
			{Tracker: "ANT", Status: "completed", HasDupes: true},
			{Tracker: "BLU", Status: "completed"},
			{Tracker: "NBL", Status: "skipped", Skipped: true, SkipReason: "rule check failed: category movie is not tv"},
		}},
		api.Request{Options: api.UploadOptions{InteractionMode: api.InteractionModeInteractive}},
		[]string{"ANT", "BLU", "NBL"},
		nil,
	)
	if err != nil {
		t.Fatalf("promptTrackerDupeReview: %v", err)
	}
	if strings.Join(approved, ",") != "ANT" {
		t.Fatalf("expected ANT approved, got %#v", approved)
	}
	if strings.Join(ignoreDupes, ",") != "ANT" {
		t.Fatalf("expected dupe ignores for approved blocked trackers, got %#v", ignoreDupes)
	}
	if len(ruleOverrides) != 0 {
		t.Fatalf("expected no rule overrides for skipped rule result, got %#v", ruleOverrides)
	}
}

func TestPromptTrackerDupeReviewSkipsPathedTorrentMatches(t *testing.T) {
	t.Parallel()

	approved, ignoreDupes, ruleOverrides, err := promptTrackerDupeReview(
		bufio.NewReader(strings.NewReader("y\n")),
		api.DupeCheckSummary{Results: []api.DupeCheckResult{
			{
				Tracker:  "BHD, DP",
				Status:   "completed",
				HasDupes: true,
				Notes:    []string{"pathed torrent match found; skipping dupe search"},
			},
			{Tracker: "ANT", Status: "completed"},
		}},
		api.Request{Options: api.UploadOptions{InteractionMode: api.InteractionModeInteractive}},
		[]string{"BHD", "DP", "ANT"},
		nil,
	)
	if err != nil {
		t.Fatalf("promptTrackerDupeReview: %v", err)
	}
	if strings.Join(approved, ",") != "ANT" {
		t.Fatalf("expected only ANT approved, got %#v", approved)
	}
	if len(ignoreDupes) != 0 {
		t.Fatalf("expected no dupe ignores for skipped pathed matches, got %#v", ignoreDupes)
	}
	if len(ruleOverrides) != 0 {
		t.Fatalf("expected no rule overrides for skipped pathed matches, got %#v", ruleOverrides)
	}
}

func TestPromptTrackerDupeReviewAllowsRuleCheckOverrides(t *testing.T) {
	t.Parallel()

	approved, ignoreDupes, ruleOverrides, err := promptTrackerDupeReview(
		bufio.NewReader(strings.NewReader("y\ny\ny\n")),
		api.DupeCheckSummary{Results: []api.DupeCheckResult{
			{Tracker: "NBL", Status: "skipped", Skipped: true, SkipReason: "rule check failed: category movie is not tv"},
			{Tracker: "OTW", Status: "skipped", Skipped: true, Error: "rule failed: Genre does not match Animation or Family for OTW."},
			{Tracker: "ANT", Status: "completed"},
		}},
		api.Request{Options: api.UploadOptions{InteractionMode: api.InteractionModeInteractive}},
		[]string{"NBL", "OTW", "ANT"},
		nil,
	)
	if err != nil {
		t.Fatalf("promptTrackerDupeReview: %v", err)
	}
	if strings.Join(approved, ",") != "NBL,OTW,ANT" {
		t.Fatalf("expected overridden rule-failed trackers approved, got %#v", approved)
	}
	if strings.Join(ignoreDupes, ",") != "NBL,OTW" {
		t.Fatalf("expected dupe ignores for approved blocked rule violations, got %#v", ignoreDupes)
	}
	if strings.Join(ruleOverrides, ",") != "NBL,OTW" {
		t.Fatalf("expected rule overrides for approved rule violations, got %#v", ruleOverrides)
	}
}

func TestPromptTrackerDupeReviewApprovesUserSkippedDupeChecksInUnattendedMode(t *testing.T) {
	t.Parallel()

	req := api.Request{
		SkipDupeCheck: true,
		Trackers:      []string{"ANT", "BLU"},
		Options:       api.UploadOptions{InteractionMode: api.InteractionModeUnattended},
	}
	summary, err := runCLIDupeCheck(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("runCLIDupeCheck: %v", err)
	}

	approved, ignoreDupes, ruleOverrides, err := promptTrackerDupeReview(
		bufio.NewReader(strings.NewReader("")),
		summary,
		req,
		req.Trackers,
		nil,
	)
	if err != nil {
		t.Fatalf("promptTrackerDupeReview: %v", err)
	}
	if strings.Join(approved, ",") != "ANT,BLU" {
		t.Fatalf("expected unattended skip-dupe approvals, got %#v", approved)
	}
	if len(ignoreDupes) != 0 {
		t.Fatalf("expected no dupe ignores for user-requested skip, got %#v", ignoreDupes)
	}
	if len(ruleOverrides) != 0 {
		t.Fatalf("expected no rule overrides for user-requested skip, got %#v", ruleOverrides)
	}
}

func TestPromptTrackerDupeReviewShowsTrackerNamingChange(t *testing.T) {
	output := captureStdout(t, func() {
		approved, _, _, err := promptTrackerDupeReview(
			bufio.NewReader(strings.NewReader("y\n")),
			api.DupeCheckSummary{Results: []api.DupeCheckResult{{Tracker: "AITHER", Status: "completed"}}},
			api.Request{Options: api.UploadOptions{InteractionMode: api.InteractionModeInteractive}},
			[]string{"AITHER"},
			map[string]api.TrackerDryRunEntry{
				"AITHER": {
					ReleaseNameChanged:  true,
					OriginalReleaseName: "Movie.2026.1080p.WEB-DL.H264-GRP",
					UploadReleaseName:   "Movie.2026.1080p.WEB-DL.x264-GRP",
				},
			},
		)
		if err != nil {
			t.Fatalf("promptTrackerDupeReview: %v", err)
		}
		if strings.Join(approved, ",") != "AITHER" {
			t.Fatalf("expected AITHER approved, got %#v", approved)
		}
	})

	expected := "AITHER changes name to Movie.2026.1080p.WEB-DL.x264-GRP\nUpload to AITHER? [y/N]: "
	if !strings.Contains(output, expected) {
		t.Fatalf("expected naming change in prompt %q, got %q", expected, output)
	}
}

func TestPrepareCLIUploadMetadataSeedsEachPath(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{}
	req := api.Request{Paths: []string{"one.mkv", "two.mkv"}}
	resolvedReq, err := prepareCLIUploadMetadata(context.Background(), coreSvc, req)
	if err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}
	if len(coreSvc.previewPaths) != 2 || coreSvc.previewPaths[0] != "one.mkv" || coreSvc.previewPaths[1] != "two.mkv" {
		t.Fatalf("unexpected preview paths: %#v", coreSvc.previewPaths)
	}
	if strings.Join(resolvedReq.Paths, ",") != "one.mkv,two.mkv" {
		t.Fatalf("unexpected resolved paths: %#v", resolvedReq.Paths)
	}
}

func TestPrepareCLIUploadMetadataReturnsResolvedPreviewPaths(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{previewSourcePath: filepath.Join("folder", "movie.mkv")}
	req := api.Request{Paths: []string{"folder"}}
	resolvedReq, err := prepareCLIUploadMetadata(context.Background(), coreSvc, req)
	if err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}
	if len(resolvedReq.Paths) != 1 || resolvedReq.Paths[0] != filepath.Join("folder", "movie.mkv") {
		t.Fatalf("expected resolved preview path, got %#v", resolvedReq.Paths)
	}
}

func TestBuildCLIUploadDebugReviewsUsesPreparedResolvedPath(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{
		previewSourcePath: filepath.Join("folder", "movie.mkv"),
		review:            api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	req := api.Request{Paths: []string{"folder"}}
	resolvedReq, err := prepareCLIUploadMetadata(context.Background(), coreSvc, req)
	if err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}

	reviews, err := buildCLIUploadDebugReviews(context.Background(), coreSvc, req.Paths, resolvedReq)
	if err != nil {
		t.Fatalf("buildCLIUploadDebugReviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected one debug review, got %d", len(reviews))
	}
	if reviews[0].SourcePath != "folder" {
		t.Fatalf("expected debug review to retain original source label, got %q", reviews[0].SourcePath)
	}
	if len(coreSvc.requests) != 2 {
		t.Fatalf("expected preview and review requests, got %#v", coreSvc.requests)
	}
	if got := coreSvc.requests[1]; got.name != "review" || len(got.req.Paths) != 1 || got.req.Paths[0] != filepath.Join("folder", "movie.mkv") {
		t.Fatalf("expected debug review to use prepared resolved path, got %#v", got)
	}
}

func TestPromptTrackerQuestionnairesRejectsBlankRequiredUnattendedDefault(t *testing.T) {
	t.Parallel()

	_, _, err := promptTrackerQuestionnaires(bufio.NewReader(strings.NewReader("")), api.UploadReview{
		Trackers: []api.TrackerReview{{
			Tracker: "ANT",
			Questionnaire: &api.TrackerQuestionnaire{Fields: []api.TrackerQuestionnaireField{{
				Key:      "type",
				Label:    "ANT Type",
				Required: true,
			}}},
		}},
	}, cliOptions{Unattended: true})
	if err == nil {
		t.Fatal("expected unattended required questionnaire error")
	}
	if !strings.Contains(err.Error(), "unattended upload requires ANT Type questionnaire value for ANT") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleBDMVPlaylistSelectionDoesNotPromptInUnattendedMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bdmvPath := filepath.Join(root, "BDMV")
	if err := os.Mkdir(bdmvPath, 0o755); err != nil {
		t.Fatalf("mkdir BDMV: %v", err)
	}
	coreSvc := &cliCoreForTest{
		playlistSelectionErr: internalerrors.ErrNotFound,
		playlists: []api.PlaylistInfo{
			{File: "00001.mpls", Duration: 7200, Score: 1},
			{File: "00002.mpls", Duration: 7100, Score: 0.9},
		},
	}

	err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{}, api.NopLogger{}, cliOptions{Unattended: true})
	if err == nil {
		t.Fatal("expected unattended playlist selection error")
	}
	if !strings.Contains(err.Error(), "unattended BDMV upload requires a saved playlist selection") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleBDMVPlaylistSelectionAllowsUnattendedUseLargestPlaylist(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bdmvPath := filepath.Join(root, "BDMV")
	if err := os.Mkdir(bdmvPath, 0o755); err != nil {
		t.Fatalf("mkdir BDMV: %v", err)
	}
	coreSvc := &cliCoreForTest{
		playlistSelectionErr: internalerrors.ErrNotFound,
		playlists: []api.PlaylistInfo{
			{File: "00001.mpls", Duration: 7200, Score: 1},
			{File: "00002.mpls", Duration: 7100, Score: 0.9},
		},
	}

	err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{
		Metadata: config.MetadataConfig{UseLargestPlaylist: true},
	}, api.NopLogger{}, cliOptions{Unattended: true})
	if err != nil {
		t.Fatalf("handleBDMVPlaylistSelection: %v", err)
	}
	if len(coreSvc.savedPlaylists) != 1 || coreSvc.savedPlaylists[0] != "00001.mpls" {
		t.Fatalf("unexpected saved playlists: %#v", coreSvc.savedPlaylists)
	}
}

func TestHandleBDMVPlaylistSelectionReturnsSaveErrorInUnattendedUseLargestPlaylist(t *testing.T) {
	t.Parallel()

	saveErr := errors.New("save failed")
	root := t.TempDir()
	bdmvPath := filepath.Join(root, "BDMV")
	if err := os.Mkdir(bdmvPath, 0o755); err != nil {
		t.Fatalf("mkdir BDMV: %v", err)
	}
	coreSvc := &cliCoreForTest{
		playlistSelectionErr: internalerrors.ErrNotFound,
		playlists: []api.PlaylistInfo{
			{File: "00001.mpls", Duration: 7200, Score: 1},
		},
		savePlaylistErr: saveErr,
	}

	err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{
		Metadata: config.MetadataConfig{UseLargestPlaylist: true},
	}, api.NopLogger{}, cliOptions{Unattended: true})
	if !errors.Is(err, saveErr) {
		t.Fatalf("expected save error, got %v", err)
	}
}

func TestHandleBDMVPlaylistSelectionReturnsSaveErrorInUnattendedSinglePlaylist(t *testing.T) {
	t.Parallel()

	saveErr := errors.New("save failed")
	root := t.TempDir()
	bdmvPath := filepath.Join(root, "BDMV")
	if err := os.Mkdir(bdmvPath, 0o755); err != nil {
		t.Fatalf("mkdir BDMV: %v", err)
	}
	coreSvc := &cliCoreForTest{
		playlistSelectionErr: internalerrors.ErrNotFound,
		playlists: []api.PlaylistInfo{
			{File: "00001.mpls", Duration: 7200, Score: 1},
		},
		savePlaylistErr: saveErr,
	}

	err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{}, api.NopLogger{}, cliOptions{Unattended: true})
	if !errors.Is(err, saveErr) {
		t.Fatalf("expected save error, got %v", err)
	}
}

func TestMaybeEditCLIDescriptionsSavesEditedGroupOnRequest(t *testing.T) {
	coreSvc := &cliCoreForTest{
		review: api.UploadReview{Trackers: []api.TrackerReview{{
			Tracker: "AITHER",
			DryRun:  api.TrackerDryRunEntry{DescriptionGroup: "unit3d"},
		}}},
		descriptionPreview: api.DescriptionBuilderPreview{Groups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER", "ULCX"},
			RawDescription: "generated description",
		}}},
		savedDescriptionGroup: api.DescriptionBuilderGroup{
			GroupKey:           "unit3d",
			Trackers:           []string{"AITHER", "ULCX"},
			RawDescription:     "edited description",
			RawDescriptionHTML: "<p>edited description</p>",
			HasOverride:        true,
		},
	}
	oldEditor := editCLIDescriptionFile
	editCLIDescriptionFile = func(_ context.Context, initial string) (string, bool, error) {
		if initial != "generated description" {
			t.Fatalf("unexpected initial description: %q", initial)
		}
		return "edited description", true, nil
	}
	defer func() { editCLIDescriptionFile = oldEditor }()

	req := api.Request{Paths: []string{"movie.mkv"}, Trackers: []string{"AITHER"}}
	review := coreSvc.review
	updatedReq, _, err := maybeEditCLIDescriptions(context.Background(), coreSvc, bufio.NewReader(strings.NewReader("y\n")), req, review, cliOptions{})
	if err != nil {
		t.Fatalf("maybeEditCLIDescriptions: %v", err)
	}
	if len(coreSvc.savedDescriptionRaw) != 1 || coreSvc.savedDescriptionRaw[0] != "edited description" {
		t.Fatalf("expected edited description save, got %#v", coreSvc.savedDescriptionRaw)
	}
	if len(coreSvc.savedDescriptionReqs) != 1 || coreSvc.savedDescriptionReqs[0].DescriptionOverrideGroup != "unit3d" {
		t.Fatalf("expected unit3d save request, got %#v", coreSvc.savedDescriptionReqs)
	}
	if len(updatedReq.DescriptionGroups) != 1 || updatedReq.DescriptionGroups[0].RawDescription != "edited description" {
		t.Fatalf("expected edited request description group, got %#v", updatedReq.DescriptionGroups)
	}
	last := coreSvc.requests[len(coreSvc.requests)-1]
	if last.name != "review" || len(last.req.DescriptionGroups) != 1 || last.req.DescriptionGroups[0].RawDescription != "edited description" {
		t.Fatalf("expected rebuilt review with edited description group, got %#v", last)
	}
}

func TestMaybeEditCLIDescriptionsSkipsOnlyID(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{
		descriptionPreview: api.DescriptionBuilderPreview{Groups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			Trackers:       []string{"AITHER"},
			RawDescription: "generated description",
		}}},
	}
	req := api.Request{
		Paths:    []string{"movie.mkv"},
		Trackers: []string{"AITHER"},
		Options:  api.UploadOptions{OnlyID: true},
	}
	updatedReq, _, err := maybeEditCLIDescriptions(context.Background(), coreSvc, bufio.NewReader(strings.NewReader("y\n")), req, api.UploadReview{}, cliOptions{})
	if err != nil {
		t.Fatalf("maybeEditCLIDescriptions: %v", err)
	}
	if len(updatedReq.DescriptionGroups) != 0 {
		t.Fatalf("expected no description groups for onlyID request, got %#v", updatedReq.DescriptionGroups)
	}
	if len(coreSvc.requests) != 0 {
		t.Fatalf("expected onlyID request to skip description builder, got %#v", coreSvc.requests)
	}
}

type cliCoreForTest struct {
	review                 api.UploadReview
	dryRunPreview          api.TrackerDryRunPreview
	callOrder              []string
	requests               []cliCoreRequestForTest
	previewPaths           []string
	previewSourcePath      string
	runUploadPreparedCalls int
	dupeSummary            api.DupeCheckSummary
	screenshotPlan         api.ScreenshotPlan
	screenshotResult       api.ScreenshotResult
	savedFinalImages       []api.ScreenshotImage
	playlistSelectionErr   error
	playlists              []api.PlaylistInfo
	savedPlaylists         []string
	savePlaylistErr        error
	descriptionPreview     api.DescriptionBuilderPreview
	savedDescriptionRaw    []string
	savedDescriptionReqs   []api.Request
	savedDescriptionGroup  api.DescriptionBuilderGroup
}

type cliCoreRequestForTest struct {
	name string
	req  api.Request
}

func (c *cliCoreForTest) recordRequest(name string, req api.Request) {
	copyReq := req
	copyReq.Paths = append([]string(nil), req.Paths...)
	copyReq.Trackers = append([]string(nil), req.Trackers...)
	copyReq.TrackersRemove = append([]string(nil), req.TrackersRemove...)
	copyReq.DescriptionGroups = api.CloneDescriptionBuilderGroups(req.DescriptionGroups)
	c.requests = append(c.requests, cliCoreRequestForTest{name: name, req: copyReq})
}

func (c *cliCoreForTest) RunUpload(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *cliCoreForTest) RunUploadPrepared(_ context.Context, req api.Request) (api.Result, error) {
	c.recordRequest("upload", req)
	c.runUploadPreparedCalls++
	return api.Result{UploadedCount: 1}, nil
}

func (c *cliCoreForTest) FetchMetadataPreview(_ context.Context, req api.Request) (api.MetadataPreview, error) {
	c.callOrder = append(c.callOrder, "preview")
	c.recordRequest("preview", req)
	if len(req.Paths) > 0 {
		c.previewPaths = append(c.previewPaths, req.Paths[0])
	}
	return api.MetadataPreview{SourcePath: c.previewSourcePath}, nil
}

func (c *cliCoreForTest) FetchDescriptionBuilderPreview(_ context.Context, req api.Request) (api.DescriptionBuilderPreview, error) {
	c.recordRequest("description-builder", req)
	return c.descriptionPreview, nil
}

func (c *cliCoreForTest) FetchDescriptionBuilderGroupPreview(context.Context, api.Request) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *cliCoreForTest) FetchPreparationPreview(context.Context, api.Request) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (c *cliCoreForTest) FetchTrackerDryRunPreview(_ context.Context, req api.Request) (api.TrackerDryRunPreview, error) {
	c.callOrder = append(c.callOrder, "dry-run")
	c.recordRequest("dry-run", req)
	return c.dryRunPreview, nil
}

func (c *cliCoreForTest) CheckDupes(_ context.Context, req api.Request) (api.DupeCheckSummary, error) {
	c.callOrder = append(c.callOrder, "dupes")
	c.recordRequest("dupes", req)
	return c.dupeSummary, nil
}

func (c *cliCoreForTest) BuildUploadReview(_ context.Context, req api.Request) (api.UploadReview, error) {
	c.callOrder = append(c.callOrder, "review")
	c.recordRequest("review", req)
	return c.review, nil
}

func (c *cliCoreForTest) FetchScreenshotPlan(_ context.Context, req api.Request) (api.ScreenshotPlan, error) {
	c.callOrder = append(c.callOrder, "screenshot-plan")
	c.recordRequest("screenshot-plan", req)
	return c.screenshotPlan, nil
}

func (c *cliCoreForTest) GenerateScreenshots(_ context.Context, req api.Request, _ []api.ScreenshotSelection, _ api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	c.callOrder = append(c.callOrder, "generate-screenshots")
	c.recordRequest("generate-screenshots", req)
	return c.screenshotResult, nil
}

func (c *cliCoreForTest) PreviewScreenshotFrame(context.Context, api.Request, float64) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, nil
}

func (c *cliCoreForTest) DeleteScreenshot(context.Context, api.Request, string) error {
	return nil
}

func (c *cliCoreForTest) DeleteTrackerImageURL(context.Context, api.Request, string) error {
	return nil
}

func (c *cliCoreForTest) SaveFinalScreenshotSelections(_ context.Context, req api.Request, images []api.ScreenshotImage) error {
	c.callOrder = append(c.callOrder, "save-screenshots")
	c.recordRequest("save-screenshots", req)
	c.savedFinalImages = append([]api.ScreenshotImage(nil), images...)
	return nil
}

func (c *cliCoreForTest) ListUploadCandidates(context.Context, api.Request) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (c *cliCoreForTest) ListUploadedImages(context.Context, api.Request) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (c *cliCoreForTest) UploadImages(context.Context, api.Request, string, []api.ScreenshotImage) (api.UploadImagesResult, error) {
	return api.UploadImagesResult{}, nil
}

func (c *cliCoreForTest) DeleteUploadedImage(context.Context, api.Request, string, string) error {
	return nil
}

func (c *cliCoreForTest) ImportMenuImages(context.Context, api.Request, []string) error {
	return nil
}

func (c *cliCoreForTest) DiscoverPlaylists(context.Context, string) ([]api.PlaylistInfo, error) {
	return c.playlists, nil
}

func (c *cliCoreForTest) SavePlaylistSelection(_ context.Context, _ string, playlists []string, _ bool) error {
	c.savedPlaylists = append(c.savedPlaylists[:0], playlists...)
	return c.savePlaylistErr
}

func (c *cliCoreForTest) LoadPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, c.playlistSelectionErr
}

func (c *cliCoreForTest) ListHistory(context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (c *cliCoreForTest) GetHistoryOverview(context.Context, string) (api.HistoryOverview, error) {
	return api.HistoryOverview{}, nil
}

func (c *cliCoreForTest) DeleteHistoryRelease(context.Context, string) error {
	return nil
}

func (c *cliCoreForTest) DeleteAllHistoryReleases(context.Context) (int, error) {
	return 0, nil
}

func (c *cliCoreForTest) RenderDescription(context.Context, string) (string, error) {
	return "", nil
}

func (c *cliCoreForTest) SaveDescriptionOverride(_ context.Context, req api.Request, raw string) (api.DescriptionBuilderGroup, error) {
	c.recordRequest("save-description", req)
	c.savedDescriptionRaw = append(c.savedDescriptionRaw, raw)
	c.savedDescriptionReqs = append(c.savedDescriptionReqs, req)
	if strings.TrimSpace(c.savedDescriptionGroup.GroupKey) != "" || strings.TrimSpace(c.savedDescriptionGroup.RawDescription) != "" {
		return c.savedDescriptionGroup, nil
	}
	return api.DescriptionBuilderGroup{
		GroupKey:           req.DescriptionOverrideGroup,
		Trackers:           append([]string{}, req.Trackers...),
		RawDescription:     raw,
		RawDescriptionHTML: raw,
		HasOverride:        strings.TrimSpace(raw) != "",
	}, nil
}

func (c *cliCoreForTest) Close() error {
	return nil
}
