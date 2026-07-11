// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type recordingDVDMenuService struct {
	meta       api.PreparedMetadata
	maxItems   int
	deleted    string
	listed     bool
	capability api.DVDMenuEngineInfo
	probed     bool
}

func TestImportMenuImagesPersistsPurposeAndDeduplicatesManagedCopy(t *testing.T) {
	t.Parallel()

	repo, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

	root := t.TempDir()
	dbPath := filepath.Join(root, "db.sqlite")
	sourcePath := filepath.Join(root, "Example.Release.2026.DVD-GRP")
	originalPath := filepath.Join(t.TempDir(), "manual-menu.png")
	if err := os.WriteFile(originalPath, []byte("synthetic menu image"), 0o600); err != nil {
		t.Fatalf("write original menu: %v", err)
	}
	now := time.Now().UTC()
	if err := repo.SaveFinalSelections(context.Background(), sourcePath, []api.ScreenshotFinalSelection{
		{SourcePath: sourcePath, ImagePath: filepath.Join(root, "auto.png"), Order: 0, Source: api.ScreenshotSelectionSourceDVDMenu, SelectedAt: now},
		{SourcePath: sourcePath, ImagePath: filepath.Join(root, "normal.png"), Order: 1, Source: "generated", SelectedAt: now},
	}); err != nil {
		t.Fatalf("seed selections: %v", err)
	}
	coreSvc := &Core{
		cfg:      config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		services: api.ServiceSet{Filesystem: stubFS{}},
		repo:     repo,
	}
	req := api.Request{Paths: []string{sourcePath}, Mode: api.ModeCLI}
	for range 2 {
		if err := coreSvc.ImportMenuImages(context.Background(), req, []string{originalPath}); err != nil {
			t.Fatalf("import menu images: %v", err)
		}
	}

	selections, err := repo.ListFinalSelections(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list selections: %v", err)
	}
	if len(selections) != 3 {
		t.Fatalf("selections = %#v, want auto/manual/normal", selections)
	}
	if selections[0].Source != api.ScreenshotSelectionSourceDVDMenu || selections[1].Source != api.ScreenshotSelectionSourceMenu || selections[2].Source != "generated" {
		t.Fatalf("selection category order = %#v", selections)
	}
	if selections[1].ImagePath == originalPath {
		t.Fatal("manual selection retained arbitrary original path")
	}
	if _, err := os.Stat(selections[1].ImagePath); err != nil {
		t.Fatalf("managed menu copy missing: %v", err)
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("original menu changed: %v", err)
	}
	records, err := repo.ListScreenshotsByPath(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list screenshots: %v", err)
	}
	if len(records) != 1 || records[0].ImagePath != selections[1].ImagePath || records[0].Purpose != api.ScreenshotPurposeMenu {
		t.Fatalf("manual screenshot record = %#v", records)
	}
}

func (s *recordingDVDMenuService) Capture(_ context.Context, meta api.PreparedMetadata, maxItems int) (api.DVDMenuCaptureResult, error) {
	s.meta = meta
	s.maxItems = maxItems
	return api.DVDMenuCaptureResult{SourcePath: meta.SourcePath, MaxItems: maxItems}, nil
}

func (s *recordingDVDMenuService) List(_ context.Context, meta api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	s.meta = meta
	s.listed = true
	return []api.ScreenshotImage{{Path: "dvd-menu-01.png", Purpose: api.ScreenshotPurposeMenu}}, nil
}

func (s *recordingDVDMenuService) Delete(_ context.Context, meta api.PreparedMetadata, imagePath string) error {
	s.meta = meta
	s.deleted = imagePath
	return nil
}

func (s *recordingDVDMenuService) Capability(context.Context) (api.DVDMenuEngineInfo, error) {
	s.probed = true
	return s.capability, nil
}

func TestDVDMenuCapabilityUsesConfiguredService(t *testing.T) {
	t.Parallel()

	menus := &recordingDVDMenuService{capability: api.DVDMenuEngineInfo{
		EngineVersion:  "phase0a-1",
		FFmpegDVDVideo: true,
	}}
	coreSvc := &Core{services: api.ServiceSet{DVDMenus: menus}}
	info, err := coreSvc.DVDMenuCapability(context.Background())
	if err != nil {
		t.Fatalf("DVDMenuCapability: %v", err)
	}
	if !menus.probed || info.EngineVersion != "phase0a-1" || !info.FFmpegDVDVideo {
		t.Fatalf("capability info = %#v probed=%t", info, menus.probed)
	}
}

func TestDVDMenuCoreMethodsUsePreparedMetadataAndResolvedLimit(t *testing.T) {
	t.Parallel()

	menus := &recordingDVDMenuService{}
	coreSvc := &Core{
		cfg: config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
		services: api.ServiceSet{
			Filesystem: stubFS{},
			Metadata:   &stubMeta{},
			DVDMenus:   menus,
		},
	}
	req := api.Request{Paths: []string{"Example.Release.2026.1080p-GRP"}, Mode: api.ModeCLI}

	result, err := coreSvc.CaptureDVDMenus(context.Background(), req)
	if err != nil {
		t.Fatalf("CaptureDVDMenus: %v", err)
	}
	if result.SourcePath != req.Paths[0] || menus.meta.SourcePath != req.Paths[0] {
		t.Fatalf("prepared source result=%q service=%q", result.SourcePath, menus.meta.SourcePath)
	}
	if result.MaxItems != config.DefaultDVDMenuItems || menus.maxItems != config.DefaultDVDMenuItems {
		t.Fatalf("resolved max result=%d service=%d", result.MaxItems, menus.maxItems)
	}

	images, err := coreSvc.ListDVDMenuScreenshots(context.Background(), req)
	if err != nil {
		t.Fatalf("ListDVDMenuScreenshots: %v", err)
	}
	if !menus.listed || len(images) != 1 || images[0].Purpose != api.ScreenshotPurposeMenu {
		t.Fatalf("list result = %+v", images)
	}

	if err := coreSvc.DeleteDVDMenuScreenshot(context.Background(), req, images[0].Path); err != nil {
		t.Fatalf("DeleteDVDMenuScreenshot: %v", err)
	}
	if menus.deleted != images[0].Path {
		t.Fatalf("deleted = %q, want %q", menus.deleted, images[0].Path)
	}
}
