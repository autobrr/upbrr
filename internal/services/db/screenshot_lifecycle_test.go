// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestScreenshotLifecyclePreservesCategoriesAndCleansReferences(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Example.Release.2026.DVD-GRP")
	autoOld := filepath.Join(root, "auto-old.png")
	manualOld := filepath.Join(root, "manual-old.png")
	normalOld := filepath.Join(root, "normal-old.png")
	normalNew := filepath.Join(root, "normal-new.png")
	now := time.Now().UTC()

	if err := repo.SaveScreenshot(ctx, api.Screenshot{SourcePath: sourcePath, ImagePath: autoOld, Purpose: api.ScreenshotPurposeMenu, CapturedAt: now}); err != nil {
		t.Fatalf("save old auto screenshot: %v", err)
	}
	if err := repo.SaveScreenshot(ctx, api.Screenshot{SourcePath: sourcePath, ImagePath: manualOld, Purpose: api.ScreenshotPurposeMenu, CapturedAt: now}); err != nil {
		t.Fatalf("save old manual screenshot: %v", err)
	}
	if err := repo.SaveFinalSelections(ctx, sourcePath, []api.ScreenshotFinalSelection{
		{SourcePath: sourcePath, ImagePath: normalOld, Order: 0, Source: "generated", SelectedAt: now},
		{SourcePath: sourcePath, ImagePath: manualOld, Order: 1, Source: api.ScreenshotSelectionSourceMenu, SelectedAt: now},
		{SourcePath: sourcePath, ImagePath: autoOld, Order: 2, Source: api.ScreenshotSelectionSourceDVDMenu, SelectedAt: now},
	}); err != nil {
		t.Fatalf("seed final selections: %v", err)
	}

	if err := repo.ReplaceNormalFinalSelections(ctx, sourcePath, []api.ScreenshotFinalSelection{
		{SourcePath: sourcePath, ImagePath: normalNew, Order: 0, Source: "generated", SelectedAt: now},
	}); err != nil {
		t.Fatalf("replace normal selections: %v", err)
	}
	assertFinalSelectionPaths(t, repo, sourcePath, []string{autoOld, manualOld, normalNew})

	manualNew := filepath.Join(root, "manual-new.png")
	if err := repo.AppendManualMenuScreenshots(ctx, sourcePath,
		[]api.Screenshot{{SourcePath: sourcePath, ImagePath: manualNew, Purpose: api.ScreenshotPurposeMenu, CapturedAt: now}},
		[]api.ScreenshotFinalSelection{{SourcePath: sourcePath, ImagePath: manualNew, Source: api.ScreenshotSelectionSourceMenu, SelectedAt: now}},
	); err != nil {
		t.Fatalf("append manual screenshot: %v", err)
	}
	assertFinalSelectionPaths(t, repo, sourcePath, []string{autoOld, manualOld, manualNew, normalNew})

	if err := repo.SaveUploadedImages(ctx, sourcePath, "example-host", []api.UploadedImageLink{{
		SourcePath: sourcePath,
		ImagePath:  autoOld,
		Host:       "example-host",
		UsageScope: "global",
		RawURL:     "https://example.invalid/auto-old.png",
		UploadedAt: now,
	}}); err != nil {
		t.Fatalf("save old auto upload: %v", err)
	}
	if err := repo.ReplaceScreenshotSlots(ctx, sourcePath, []api.ScreenshotSlot{{
		SourcePath: sourcePath,
		SlotOrder:  0,
		ImagePath:  autoOld,
		Variants: []api.ScreenshotSlotVariant{{
			SourcePath: sourcePath,
			SlotOrder:  0,
			Host:       "example-host",
			UsageScope: "global",
			ImagePath:  autoOld,
		}},
	}}); err != nil {
		t.Fatalf("save old auto slot: %v", err)
	}

	autoNew := filepath.Join(root, "auto-new.png")
	replaced, err := repo.ReplaceDVDMenuScreenshots(ctx, sourcePath,
		[]api.Screenshot{{SourcePath: sourcePath, ImagePath: autoNew, Purpose: api.ScreenshotPurposeMenu, CapturedAt: now}},
		[]api.ScreenshotFinalSelection{{SourcePath: sourcePath, ImagePath: autoNew, Source: api.ScreenshotSelectionSourceDVDMenu, SelectedAt: now}},
	)
	if err != nil {
		t.Fatalf("replace auto screenshots: %v", err)
	}
	if len(replaced) != 1 || replaced[0] != autoOld {
		t.Fatalf("replaced = %#v, want old automatic path", replaced)
	}
	assertFinalSelectionPaths(t, repo, sourcePath, []string{autoNew, manualOld, manualNew, normalNew})
	assertNoScreenshotReferences(t, repo, sourcePath, autoOld)

	if err := repo.SaveUploadedImages(ctx, sourcePath, "example-host", []api.UploadedImageLink{{
		SourcePath: sourcePath,
		ImagePath:  manualNew,
		Host:       "example-host",
		UsageScope: "global",
		RawURL:     "https://example.invalid/manual-new.png",
		UploadedAt: now,
	}}); err != nil {
		t.Fatalf("save manual upload: %v", err)
	}
	deleted, err := repo.DeleteDiscMenuScreenshot(ctx, sourcePath, manualNew)
	if err != nil {
		t.Fatalf("delete manual screenshot: %v", err)
	}
	if deleted.Selection.Source != api.ScreenshotSelectionSourceMenu || deleted.UploadedLinks != 1 {
		t.Fatalf("delete result = %#v", deleted)
	}
	assertFinalSelectionPaths(t, repo, sourcePath, []string{autoNew, manualOld, normalNew})
	assertNoScreenshotReferences(t, repo, sourcePath, manualNew)
}

func TestAppendManualMenuScreenshotsRollsBackCrossSourceImageConflict(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	imagePath := filepath.Join(root, "shared.png")
	if err := repo.SaveScreenshot(ctx, api.Screenshot{
		SourcePath: firstSource,
		ImagePath:  imagePath,
		Purpose:    api.ScreenshotPurposeFinal,
		CapturedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed screenshot: %v", err)
	}

	err = repo.AppendManualMenuScreenshots(ctx, secondSource,
		[]api.Screenshot{{SourcePath: secondSource, ImagePath: imagePath, Purpose: api.ScreenshotPurposeMenu}},
		[]api.ScreenshotFinalSelection{{SourcePath: secondSource, ImagePath: imagePath, Source: api.ScreenshotSelectionSourceMenu}},
	)
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("append error = %v, want invalid input", err)
	}
	selections, err := repo.ListFinalSelections(ctx, secondSource)
	if err != nil {
		t.Fatalf("list rolled-back selections: %v", err)
	}
	if len(selections) != 0 {
		t.Fatalf("rolled-back selections = %#v", selections)
	}
	records, err := repo.ListScreenshotsByPath(ctx, firstSource)
	if err != nil {
		t.Fatalf("list original screenshots: %v", err)
	}
	if len(records) != 1 || records[0].Purpose != api.ScreenshotPurposeFinal {
		t.Fatalf("original screenshot changed: %#v", records)
	}
}

func assertFinalSelectionPaths(t *testing.T, repo *SQLiteRepository, sourcePath string, expected []string) {
	t.Helper()
	selections, err := repo.ListFinalSelections(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list final selections: %v", err)
	}
	if len(selections) != len(expected) {
		t.Fatalf("selection count = %d, want %d: %#v", len(selections), len(expected), selections)
	}
	for index, imagePath := range expected {
		if selections[index].ImagePath != imagePath || selections[index].Order != index {
			t.Fatalf("selection[%d] = %#v, want path %q order %d", index, selections[index], imagePath, index)
		}
	}
}

func assertNoScreenshotReferences(t *testing.T, repo *SQLiteRepository, sourcePath string, imagePath string) {
	t.Helper()
	records, err := repo.ListScreenshotsByPath(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list screenshots: %v", err)
	}
	for _, record := range records {
		if record.ImagePath == imagePath {
			t.Fatalf("screenshot record retained for %q", imagePath)
		}
	}
	uploads, err := repo.ListUploadedImagesByPath(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}
	for _, upload := range uploads {
		if upload.ImagePath == imagePath {
			t.Fatalf("upload retained for %q", imagePath)
		}
	}
	slots, err := repo.ListScreenshotSlotsByPath(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list slots: %v", err)
	}
	for _, slot := range slots {
		if slot.ImagePath == imagePath {
			t.Fatalf("slot retained for %q", imagePath)
		}
		for _, variant := range slot.Variants {
			if variant.ImagePath == imagePath {
				t.Fatalf("slot variant retained for %q", imagePath)
			}
		}
	}
}
