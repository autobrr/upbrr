// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/pkg/api"
)

type preparedMetaTestCore struct {
	exportedMeta api.PreparedMetadata
	exportFound  bool
	exportErr    error
	importedMeta api.PreparedMetadata
	importedReq  api.Request
}

func (c *preparedMetaTestCore) RunUpload(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *preparedMetaTestCore) RunUploadPrepared(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *preparedMetaTestCore) FetchMetadataPreview(context.Context, api.Request) (api.MetadataPreview, error) {
	return api.MetadataPreview{}, nil
}

func (c *preparedMetaTestCore) FetchDescriptionBuilderPreview(context.Context, api.Request) (api.DescriptionBuilderPreview, error) {
	return api.DescriptionBuilderPreview{}, nil
}

func (c *preparedMetaTestCore) FetchPreparationPreview(context.Context, api.Request) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (c *preparedMetaTestCore) FetchTrackerDryRunPreview(context.Context, api.Request) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (c *preparedMetaTestCore) CheckDupes(context.Context, api.Request) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, nil
}

func (c *preparedMetaTestCore) BuildUploadReview(context.Context, api.Request) (api.UploadReview, error) {
	return api.UploadReview{}, nil
}

func (c *preparedMetaTestCore) FetchScreenshotPlan(context.Context, api.Request) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{}, nil
}

func (c *preparedMetaTestCore) GenerateScreenshots(context.Context, api.Request, []api.ScreenshotSelection, api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	return api.ScreenshotResult{}, nil
}

func (c *preparedMetaTestCore) PreviewScreenshotFrame(context.Context, api.Request, float64) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, nil
}

func (c *preparedMetaTestCore) DeleteScreenshot(context.Context, api.Request, string) error {
	return nil
}

func (c *preparedMetaTestCore) DeleteTrackerImageURL(context.Context, api.Request, string) error {
	return nil
}

func (c *preparedMetaTestCore) SaveFinalScreenshotSelections(context.Context, api.Request, []api.ScreenshotImage) error {
	return nil
}

func (c *preparedMetaTestCore) ListUploadCandidates(context.Context, api.Request) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) ListUploadedImages(context.Context, api.Request) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) UploadImages(context.Context, api.Request, string, []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) DeleteUploadedImage(context.Context, api.Request, string, string) error {
	return nil
}

func (c *preparedMetaTestCore) DiscoverPlaylists(context.Context, string) ([]api.PlaylistInfo, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return nil
}

func (c *preparedMetaTestCore) LoadPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, nil
}

func (c *preparedMetaTestCore) ListHistory(context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) GetHistoryOverview(context.Context, string) (api.HistoryOverview, error) {
	return api.HistoryOverview{}, nil
}

func (c *preparedMetaTestCore) DeleteHistoryRelease(context.Context, string) error {
	return nil
}

func (c *preparedMetaTestCore) DeleteAllHistoryReleases(context.Context) (int, error) {
	return 0, nil
}

func (c *preparedMetaTestCore) RenderDescription(context.Context, string) (string, error) {
	return "", nil
}

func (c *preparedMetaTestCore) FetchDescriptionBuilderGroupPreview(context.Context, api.Request) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *preparedMetaTestCore) SaveDescriptionOverride(context.Context, api.Request, string) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *preparedMetaTestCore) Close() error {
	return nil
}

func (c *preparedMetaTestCore) ExportGUICachedPreparedMeta(context.Context, api.Request) (api.PreparedMetadata, bool, error) {
	return c.exportedMeta, c.exportFound, c.exportErr
}

func (c *preparedMetaTestCore) ImportPreparedMetadataForGUI(_ context.Context, req api.Request, meta api.PreparedMetadata) error {
	c.importedReq = req
	c.importedMeta = meta
	return nil
}

func TestPruneCompletedDupeJobsLockedKeepsNewestCompleted(t *testing.T) {
	backend := &Backend{
		dupes: make(map[string]*dupeCheckJob),
	}

	active := &dupeCheckJob{id: "active", status: "running"}
	backend.dupes[active.id] = active

	now := time.Now().UTC()
	for idx := 0; idx < 3; idx++ {
		id := fmt.Sprintf("dupe-%d", idx)
		backend.dupes[id] = &dupeCheckJob{
			id:         id,
			status:     "completed",
			finishedAt: now.Add(time.Duration(idx) * time.Minute),
		}
	}

	backend.pruneCompletedDupeJobsLocked(2)

	if _, ok := backend.dupes["dupe-0"]; ok {
		t.Fatal("expected oldest completed dupe job to be pruned")
	}
	if _, ok := backend.dupes["dupe-1"]; !ok {
		t.Fatal("expected newer completed dupe job to remain")
	}
	if _, ok := backend.dupes["dupe-2"]; !ok {
		t.Fatal("expected newest completed dupe job to remain")
	}
	if _, ok := backend.dupes[active.id]; !ok {
		t.Fatal("expected active dupe job to remain")
	}
}

func TestPruneCompletedUploadJobsLockedKeepsNewestCompleted(t *testing.T) {
	backend := &Backend{
		uploads: make(map[string]*trackerUploadJob),
	}

	active := &trackerUploadJob{id: "active", status: "running"}
	backend.uploads[active.id] = active

	now := time.Now().UTC()
	for idx := 0; idx < 3; idx++ {
		id := fmt.Sprintf("upload-%d", idx)
		backend.uploads[id] = &trackerUploadJob{
			id:         id,
			status:     "completed",
			finishedAt: now.Add(time.Duration(idx) * time.Minute),
		}
	}

	backend.pruneCompletedUploadJobsLocked(2)

	if _, ok := backend.uploads["upload-0"]; ok {
		t.Fatal("expected oldest completed upload job to be pruned")
	}
	if _, ok := backend.uploads["upload-1"]; !ok {
		t.Fatal("expected newer completed upload job to remain")
	}
	if _, ok := backend.uploads["upload-2"]; !ok {
		t.Fatal("expected newest completed upload job to remain")
	}
	if _, ok := backend.uploads[active.id]; !ok {
		t.Fatal("expected active upload job to remain")
	}
}

func TestSeedRunCorePreparedMetaCopiesPreparedMetadata(t *testing.T) {
	source := &preparedMetaTestCore{
		exportFound:  true,
		exportedMeta: api.PreparedMetadata{SourcePath: "C:\\releases\\movie.mkv"},
	}
	target := &preparedMetaTestCore{}
	req := api.Request{
		Paths: []string{"C:\\releases\\movie.mkv"},
		Mode:  api.ModeGUI,
	}

	if err := guishared.SeedRunCorePreparedMeta(context.Background(), source, target, req); err != nil {
		t.Fatalf("seed run core prepared meta: %v", err)
	}
	if target.importedMeta.SourcePath != "C:\\releases\\movie.mkv" {
		t.Fatalf("expected imported source path, got %q", target.importedMeta.SourcePath)
	}
	if len(target.importedReq.Paths) != 1 || target.importedReq.Paths[0] != "C:\\releases\\movie.mkv" {
		t.Fatalf("expected import request paths to be preserved, got %#v", target.importedReq.Paths)
	}
}

func TestSeedRunCorePreparedMetaSkipsWhenNoCacheFound(t *testing.T) {
	source := &preparedMetaTestCore{}
	target := &preparedMetaTestCore{}

	if err := guishared.SeedRunCorePreparedMeta(context.Background(), source, target, api.Request{
		Paths: []string{"C:\\releases\\movie.mkv"},
		Mode:  api.ModeGUI,
	}); err != nil {
		t.Fatalf("seed run core prepared meta: %v", err)
	}
	if target.importedMeta.SourcePath != "" {
		t.Fatalf("expected no metadata import when cache missing, got %#v", target.importedMeta)
	}
}
