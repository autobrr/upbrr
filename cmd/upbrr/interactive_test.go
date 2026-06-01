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
	err := runInteractiveCLIPath(context.Background(), coreSvc, nil, cliOptions{Unattended: true}, map[string]bool{}, "movie.mkv", 1)
	if err != nil {
		t.Fatalf("runInteractiveCLIPath: %v", err)
	}
	if coreSvc.runUploadPreparedCalls != 1 {
		t.Fatalf("expected one prepared upload, got %d", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunSiteCheckCLIPathSeedsMetadataBeforeReview(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{}
	if err := runSiteCheckCLIPath(context.Background(), coreSvc, cliOptions{SiteCheck: true}, map[string]bool{}, "movie.mkv", 1); err != nil {
		t.Fatalf("runSiteCheckCLIPath: %v", err)
	}
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,review" {
		t.Fatalf("expected preview before review, got %s", got)
	}
}

func TestPrepareCLIUploadMetadataSeedsEachPath(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{}
	req := api.Request{Paths: []string{"one.mkv", "two.mkv"}}
	if err := prepareCLIUploadMetadata(context.Background(), coreSvc, req); err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}
	if len(coreSvc.previewPaths) != 2 || coreSvc.previewPaths[0] != "one.mkv" || coreSvc.previewPaths[1] != "two.mkv" {
		t.Fatalf("unexpected preview paths: %#v", coreSvc.previewPaths)
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

type cliCoreForTest struct {
	review                 api.UploadReview
	callOrder              []string
	previewPaths           []string
	runUploadPreparedCalls int
	playlistSelectionErr   error
	playlists              []api.PlaylistInfo
	savedPlaylists         []string
	savePlaylistErr        error
}

func (c *cliCoreForTest) RunUpload(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *cliCoreForTest) RunUploadPrepared(context.Context, api.Request) (api.Result, error) {
	c.runUploadPreparedCalls++
	return api.Result{UploadedCount: 1}, nil
}

func (c *cliCoreForTest) FetchMetadataPreview(_ context.Context, req api.Request) (api.MetadataPreview, error) {
	c.callOrder = append(c.callOrder, "preview")
	if len(req.Paths) > 0 {
		c.previewPaths = append(c.previewPaths, req.Paths[0])
	}
	return api.MetadataPreview{}, nil
}

func (c *cliCoreForTest) FetchDescriptionBuilderPreview(context.Context, api.Request) (api.DescriptionBuilderPreview, error) {
	return api.DescriptionBuilderPreview{}, nil
}

func (c *cliCoreForTest) FetchDescriptionBuilderGroupPreview(context.Context, api.Request) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *cliCoreForTest) FetchPreparationPreview(context.Context, api.Request) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (c *cliCoreForTest) FetchTrackerDryRunPreview(context.Context, api.Request) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (c *cliCoreForTest) CheckDupes(context.Context, api.Request) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, nil
}

func (c *cliCoreForTest) BuildUploadReview(context.Context, api.Request) (api.UploadReview, error) {
	c.callOrder = append(c.callOrder, "review")
	return c.review, nil
}

func (c *cliCoreForTest) FetchScreenshotPlan(context.Context, api.Request) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{}, nil
}

func (c *cliCoreForTest) GenerateScreenshots(context.Context, api.Request, []api.ScreenshotSelection, api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	return api.ScreenshotResult{}, nil
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

func (c *cliCoreForTest) SaveFinalScreenshotSelections(context.Context, api.Request, []api.ScreenshotImage) error {
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

func (c *cliCoreForTest) SaveDescriptionOverride(context.Context, api.Request, string) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *cliCoreForTest) Close() error {
	return nil
}
