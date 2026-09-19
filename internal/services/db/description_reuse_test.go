// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReusableDescriptionRoundTripReplacesSourceRecord(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	first := reusableDescriptionForTest("a", "first")
	if err := repo.SaveReusableDescription(ctx, sourcePath, first); err != nil {
		t.Fatalf("save first reusable description: %v", err)
	}
	second := reusableDescriptionForTest("c", "second")
	if err := repo.SaveReusableDescription(ctx, "  "+sourcePath+"  ", second); err != nil {
		t.Fatalf("replace reusable description: %v", err)
	}
	loaded, found, err := repo.LoadReusableDescription(ctx, sourcePath)
	if err != nil || !found {
		t.Fatalf("load reusable description found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(loaded, second) {
		t.Fatalf("loaded reusable description = %#v, want %#v", loaded, second)
	}
	var rows int
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM description_reusable WHERE source_path = ?`, sourcePath).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("reusable description rows = %d, want 1", rows)
	}
}

func TestReusableDescriptionRejectsInvalidInputAndReportsMissing(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	valid := reusableDescriptionForTest("a", "rendered")
	if err := repo.SaveReusableDescription(ctx, "", valid); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("save empty source = %v", err)
	}
	valid.CompatibilityFingerprint = ""
	if err := repo.SaveReusableDescription(ctx, filepath.Join(t.TempDir(), "invalid.mkv"), valid); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("save invalid record = %v", err)
	}
	if _, found, err := repo.LoadReusableDescription(ctx, filepath.Join(t.TempDir(), "missing.mkv")); err != nil || found {
		t.Fatalf("load missing found=%t err=%v", found, err)
	}
	if _, _, err := repo.LoadReusableDescription(ctx, " "); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("load empty source = %v", err)
	}
}

func TestReusableDescriptionRejectsExpiredCoordinatorWrites(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	now := time.Now().UTC()
	empty, err := repo.LoadActiveInput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       1,
		Fence:          1,
		OwnerID:        "owner",
		CoordinatorID:  "current",
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "open",
		RequestedPath:  sourcePath,
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(t.Context(), empty, opening, now); err != nil {
		t.Fatal(err)
	}
	stale := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "previous", Fence: 1})
	if err := repo.SaveReusableDescription(stale, sourcePath, reusableDescriptionForTest("a", "rendered")); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("save with expired coordinator = %v", err)
	}
}

func TestPurgeContentDataRemovesReusableDescriptionAndListsItsSource(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "reusable-description.mkv")
	otherPath := filepath.Join(t.TempDir(), "other-description.mkv")
	if err := repo.SaveReusableDescription(ctx, sourcePath, reusableDescriptionForTest("a", "source")); err != nil {
		t.Fatalf("save source reusable description: %v", err)
	}
	if err := repo.SaveReusableDescription(ctx, otherPath, reusableDescriptionForTest("b", "other")); err != nil {
		t.Fatalf("save other reusable description: %v", err)
	}
	paths, err := repo.ListStoredReleasePaths(ctx)
	if err != nil || !slices.Contains(paths, sourcePath) {
		t.Fatalf("stored release paths = %#v, %v", paths, err)
	}
	if err := repo.PurgeContentData(ctx, sourcePath); err != nil {
		t.Fatalf("purge reusable description: %v", err)
	}
	if _, found, err := repo.LoadReusableDescription(ctx, sourcePath); err != nil || found {
		t.Fatalf("load purged description found=%t err=%v", found, err)
	}
	if loaded, found, err := repo.LoadReusableDescription(ctx, otherPath); err != nil || !found || loaded.Descriptions[0].Rendered != "other" {
		t.Fatalf("load preserved description = %#v found=%t err=%v", loaded, found, err)
	}
}

func reusableDescriptionForTest(fingerprintCharacter, rendered string) api.ReusableDescription {
	return api.ReusableDescription{
		CompatibilityFingerprint: api.WorkflowFingerprint(strings.Repeat(fingerprintCharacter, 64)),
		Descriptions: []api.RenderedDescription{{
			GroupKey:           "main",
			TrackerIDs:         []api.TrackerID{"AITHER"},
			Source:             "source",
			Rendered:           rendered,
			ContentFingerprint: api.WorkflowFingerprint(strings.Repeat("f", 64)),
		}},
		TrackerResults: []api.DescriptionTrackerResult{{TrackerID: "AITHER", Status: api.StageStatusCompleted}},
		Overrides:      []api.DescriptionOverrideInput{{GroupKey: "main", Source: "edited source"}},
	}
}
