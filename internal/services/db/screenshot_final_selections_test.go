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

func TestScreenshotFinalSelectionsCRUD(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	sourcePath := "/media/file.mkv"
	now := time.Now().UTC().Truncate(time.Second)

	selections := []ScreenshotFinalSelection{
		{
			SourcePath: sourcePath,
			ImagePath:  "/tmp/final-01.png",
			Order:      0,
			Source:     "existing",
			SelectedAt: now,
		},
		{
			SourcePath: sourcePath,
			ImagePath:  "/tmp/final-02.png",
			Order:      1,
			Source:     "generated",
			SelectedAt: now,
		},
	}

	if err := repo.SaveFinalSelections(ctx, sourcePath, selections); err != nil {
		t.Fatalf("save final selections: %v", err)
	}

	loaded, err := repo.ListFinalSelections(ctx, sourcePath)
	if err != nil {
		t.Fatalf("list final selections: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 selections, got %d", len(loaded))
	}
	if loaded[0].ImagePath != selections[0].ImagePath || loaded[0].Order != 0 {
		t.Fatalf("unexpected first selection: %#v", loaded[0])
	}
	if loaded[1].ImagePath != selections[1].ImagePath || loaded[1].Order != 1 {
		t.Fatalf("unexpected second selection: %#v", loaded[1])
	}

	if err := repo.DeleteFinalSelection(ctx, selections[0].ImagePath); err != nil {
		t.Fatalf("delete final selection: %v", err)
	}

	loaded, err = repo.ListFinalSelections(ctx, sourcePath)
	if err != nil {
		t.Fatalf("list final selections: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 selection, got %d", len(loaded))
	}
	if loaded[0].ImagePath != selections[1].ImagePath {
		t.Fatalf("unexpected remaining selection: %#v", loaded[0])
	}
}

func TestScreenshotFinalSelectionsInvalidInput(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	if err := repo.SaveFinalSelections(ctx, "", nil); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}

	if _, err := repo.ListFinalSelections(ctx, ""); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}

	if err := repo.DeleteFinalSelection(ctx, ""); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}
