// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

func TestMediaAssetsRequireExactPreparedBinding(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	first := testPreparedMediaBinding("C:\\releases\\Example.Release.2026")
	second := first
	second.PreparedMediaFingerprint = "replacement-prepared-media"
	second.PreparedGeneration = 2
	imagePath := "C:\\screens\\disc-a.png"
	stamp := time.Date(2026, time.August, 18, 1, 2, 3, 0, time.UTC)

	writeMediaAssetsForBinding(t, repo, first, "disc-a", imagePath, stamp)
	assertMediaAssetsForBinding(t, repo, first, "disc-a", 1)
	assertMediaAssetsForBinding(t, repo, second, "", 0)

	writeMediaAssetsForBinding(t, repo, second, "disc-b", imagePath, stamp.Add(time.Minute))
	assertMediaAssetsForBinding(t, repo, first, "", 0)
	assertMediaAssetsForBinding(t, repo, second, "disc-b", 1)
}

func TestOlderPreparedBindingCannotReplaceNewerMediaAssets(t *testing.T) {
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
	older := testPreparedMediaBinding("C:\\releases\\Example.Release.2026")
	newer := older
	newer.PreparedMediaFingerprint = "newer-prepared-media"
	newer.PreparedGeneration = older.PreparedGeneration + 1
	imagePath := "C:\\screens\\same-name.png"
	stamp := time.Date(2026, time.August, 18, 1, 2, 3, 0, time.UTC)

	writeMediaAssetsForBinding(t, repo, older, "disc-old", imagePath, stamp)
	writeMediaAssetsForBinding(t, repo, newer, "disc-new", imagePath, stamp.Add(time.Minute))

	if err := repo.SaveFinalSelections(ctx, older, nil); err != nil {
		t.Fatalf("clear stale final selections: %v", err)
	}
	if err := repo.ReplaceScreenshotSlots(ctx, older, nil); err != nil {
		t.Fatalf("clear stale screenshot slots: %v", err)
	}
	assertMediaAssetsForBinding(t, repo, newer, "disc-new", 1)

	assertStaleMediaWriteRejected(t, repo.SaveScreenshot(ctx, older, Screenshot{
		DiscID:     "disc-old",
		ImagePath:  imagePath,
		Purpose:    "final",
		CapturedAt: stamp,
	}))
	assertStaleMediaWriteRejected(t, repo.SaveFinalSelections(ctx, older, []ScreenshotFinalSelection{{
		DiscID:     "disc-old",
		ImagePath:  imagePath,
		Source:     "final",
		SelectedAt: stamp,
	}}))
	assertStaleMediaWriteRejected(t, repo.SaveUploadedImages(ctx, older, "imgbox", []UploadedImageLink{{
		DiscID:     "disc-old",
		ImagePath:  imagePath,
		UsageScope: "global",
		RawURL:     "https://example.invalid/stale.png",
		UploadedAt: stamp,
	}}))
	assertStaleMediaWriteRejected(t, repo.ReplaceScreenshotSlots(ctx, older, []ScreenshotSlot{{
		DiscID:    "disc-old",
		SlotOrder: 0,
		ImagePath: imagePath,
		Variants: []ScreenshotSlotVariant{{
			DiscID:     "disc-old",
			SlotOrder:  0,
			Host:       "imgbox",
			UsageScope: "global",
			ImagePath:  imagePath,
			RawURL:     "https://example.invalid/stale.png",
			UploadedAt: stamp,
		}},
	}}))
	assertStaleMediaWriteRejected(t, repo.UpsertScreenshotSlotVariants(ctx, older, []ScreenshotSlotVariant{{
		DiscID:     "disc-old",
		SlotOrder:  0,
		Host:       "imgbox",
		UsageScope: "global",
		ImagePath:  imagePath,
		RawURL:     "https://example.invalid/stale.png",
		UploadedAt: stamp,
	}}))
	mismatched := newer
	mismatched.PreparedMediaFingerprint = "mismatched-prepared-media"
	assertStaleMediaWriteRejected(t, repo.SaveScreenshot(ctx, mismatched, Screenshot{
		DiscID:     "disc-mismatch",
		ImagePath:  imagePath,
		Purpose:    "final",
		CapturedAt: stamp,
	}))
	assertMediaAssetsForBinding(t, repo, newer, "disc-new", 1)
}

func TestCurrentPreparedBindingCanReplaceOrphanedHigherGeneration(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	sourcePath := "C:\\releases\\Example.Release.2026"
	orphaned := testPreparedMediaBinding(sourcePath)
	orphaned.PreparedMediaFingerprint = "orphaned-prepared-media"
	orphaned.PreparedGeneration = 5
	current := testPreparedMediaBinding(sourcePath)
	imagePath := "C:\\screens\\same-name.png"
	stamp := time.Date(2026, time.August, 18, 1, 2, 3, 0, time.UTC)
	writeMediaAssetsForBinding(t, repo, orphaned, "disc-orphaned", imagePath, stamp)

	if _, err := repo.RawDB().ExecContext(context.Background(), `
		INSERT INTO prepared_release_current (
			source_path, generation, source_fingerprint, fact_instruction_fingerprint,
			policy_fingerprint, contract_version, source_json, naming_json, episode_json,
			media_json, disc_json, assessments_json, prepared_at
		) VALUES (?, ?, 'source', 'facts', 'policy', 'contract', '{}', '{}', '{}', '{}', '{}', '{}', ?)
	`, sourcePath, current.PreparedGeneration, stamp.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed current prepared generation: %v", err)
	}

	writeMediaAssetsForBinding(t, repo, current, "disc-current", imagePath, stamp.Add(time.Minute))
	assertMediaAssetsForBinding(t, repo, orphaned, "", 0)
	assertMediaAssetsForBinding(t, repo, current, "disc-current", 1)
}

func assertStaleMediaWriteRejected(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("stale media write error = %v, want ErrInvalidInput", err)
	}
}

func writeMediaAssetsForBinding(
	t *testing.T,
	repo *SQLiteRepository,
	binding PreparedMediaBinding,
	discID string,
	imagePath string,
	stamp time.Time,
) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveScreenshot(ctx, binding, Screenshot{
		DiscID:     discID,
		ImagePath:  imagePath,
		Timestamp:  42,
		Purpose:    "final",
		CapturedAt: stamp,
	}); err != nil {
		t.Fatalf("save screenshot: %v", err)
	}
	if err := repo.SaveFinalSelections(ctx, binding, []ScreenshotFinalSelection{{
		DiscID:     discID,
		ImagePath:  imagePath,
		Source:     "final",
		SelectedAt: stamp,
	}}); err != nil {
		t.Fatalf("save final selections: %v", err)
	}
	if err := repo.SaveUploadedImages(ctx, binding, "imgbox", []UploadedImageLink{{
		DiscID:     discID,
		ImagePath:  imagePath,
		UsageScope: "global",
		RawURL:     "https://example.invalid/image.png",
		UploadedAt: stamp,
	}}); err != nil {
		t.Fatalf("save uploaded images: %v", err)
	}
	if err := repo.ReplaceScreenshotSlots(ctx, binding, []ScreenshotSlot{{
		DiscID:    discID,
		SlotOrder: 0,
		ImagePath: imagePath,
		Variants: []ScreenshotSlotVariant{{
			DiscID:     discID,
			SlotOrder:  0,
			Host:       "imgbox",
			UsageScope: "global",
			ImagePath:  imagePath,
			RawURL:     "https://example.invalid/image.png",
			UploadedAt: stamp,
		}},
	}}); err != nil {
		t.Fatalf("replace screenshot slots: %v", err)
	}
}

func assertMediaAssetsForBinding(
	t *testing.T,
	repo *SQLiteRepository,
	binding PreparedMediaBinding,
	discID string,
	want int,
) {
	t.Helper()
	ctx := context.Background()
	screenshots, err := repo.ListScreenshotsByPath(ctx, binding)
	if err != nil {
		t.Fatalf("list screenshots: %v", err)
	}
	selections, err := repo.ListFinalSelections(ctx, binding)
	if err != nil {
		t.Fatalf("list final selections: %v", err)
	}
	uploads, err := repo.ListUploadedImagesByPath(ctx, binding)
	if err != nil {
		t.Fatalf("list uploaded images: %v", err)
	}
	slots, err := repo.ListScreenshotSlotsByPath(ctx, binding)
	if err != nil {
		t.Fatalf("list screenshot slots: %v", err)
	}
	if len(screenshots) != want || len(selections) != want || len(uploads) != want || len(slots) != want {
		t.Fatalf("asset counts = screenshots:%d selections:%d uploads:%d slots:%d, want %d", len(screenshots), len(selections), len(uploads), len(slots), want)
	}
	if want == 0 {
		return
	}
	if screenshots[0].DiscID != discID || selections[0].DiscID != discID || uploads[0].DiscID != discID ||
		slots[0].DiscID != discID || len(slots[0].Variants) != 1 || slots[0].Variants[0].DiscID != discID {
		t.Fatalf("disc binding was not retained: screenshots=%#v selections=%#v uploads=%#v slots=%#v", screenshots, selections, uploads, slots)
	}
	if screenshots[0].PreparedMediaFingerprint != binding.PreparedMediaFingerprint || screenshots[0].PreparedGeneration != binding.PreparedGeneration ||
		selections[0].PreparedMediaFingerprint != binding.PreparedMediaFingerprint || uploads[0].PreparedGeneration != binding.PreparedGeneration ||
		slots[0].PreparedMediaFingerprint != binding.PreparedMediaFingerprint || slots[0].Variants[0].PreparedGeneration != binding.PreparedGeneration {
		t.Fatalf("prepared binding was not retained: screenshots=%#v selections=%#v uploads=%#v slots=%#v", screenshots, selections, uploads, slots)
	}
}
