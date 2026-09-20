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

func TestReusableDescriptionWorkflowStateSaveReplacesSourceRecord(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	state := createReusableDescriptionWorkflowState(t, repo, "round-trip")
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	first := reusableDescriptionForTest("a", "first")
	state = saveReusableDescriptionWithWorkflowState(t, repo, state, sourcePath, first)
	second := reusableDescriptionForTest("c", "second")
	_ = saveReusableDescriptionWithWorkflowState(t, repo, state, "  "+sourcePath+"  ", second)
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
	state := createReusableDescriptionWorkflowState(t, repo, "invalid")
	valid := reusableDescriptionForTest("a", "rendered")
	updated := reusableDescriptionWorkflowStateRecord(state, "", valid)
	if err := repo.SaveReleaseWorkflowState(ctx, state.Revision, updated); err == nil {
		t.Fatal("save empty source succeeded")
	}
	valid.CompatibilityFingerprint = ""
	updated = reusableDescriptionWorkflowStateRecord(state, filepath.Join(t.TempDir(), "invalid.mkv"), valid)
	if err := repo.SaveReleaseWorkflowState(ctx, state.Revision, updated); err == nil {
		t.Fatal("save invalid record succeeded")
	}
	if _, found, err := repo.LoadReusableDescription(ctx, filepath.Join(t.TempDir(), "missing.mkv")); err != nil || found {
		t.Fatalf("load missing found=%t err=%v", found, err)
	}
	if _, _, err := repo.LoadReusableDescription(ctx, " "); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("load empty source = %v", err)
	}
}

func TestPurgeContentDataRemovesReusableDescriptionAndListsItsSource(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "reusable-description.mkv")
	otherPath := filepath.Join(t.TempDir(), "other-description.mkv")
	state := createReusableDescriptionWorkflowState(t, repo, "purge-source")
	_ = saveReusableDescriptionWithWorkflowState(t, repo, state, sourcePath, reusableDescriptionForTest("a", "source"))
	otherState := createReusableDescriptionWorkflowState(t, repo, "purge-other")
	_ = saveReusableDescriptionWithWorkflowState(t, repo, otherState, otherPath, reusableDescriptionForTest("b", "other"))
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

func createReusableDescriptionWorkflowState(
	t *testing.T,
	repo *SQLiteRepository,
	workflowSuffix string,
) api.ReleaseWorkflowStateRecord {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	state := workflowStateRecordForTest(
		api.WorkflowID("workflow-description-reuse-"+workflowSuffix),
		api.WorkflowStatusActive,
		now,
		`{"revision":1}`,
	)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), state); err != nil {
		t.Fatalf("create reusable description workflow: %v", err)
	}
	return state
}

func saveReusableDescriptionWithWorkflowState(
	t *testing.T,
	repo *SQLiteRepository,
	state api.ReleaseWorkflowStateRecord,
	sourcePath string,
	description api.ReusableDescription,
) api.ReleaseWorkflowStateRecord {
	t.Helper()
	updated := reusableDescriptionWorkflowStateRecord(state, sourcePath, description)
	if err := repo.SaveReleaseWorkflowState(t.Context(), state.Revision, updated); err != nil {
		t.Fatalf("save reusable description workflow state: %v", err)
	}
	return updated
}

func reusableDescriptionWorkflowStateRecord(
	state api.ReleaseWorkflowStateRecord,
	sourcePath string,
	description api.ReusableDescription,
) api.ReleaseWorkflowStateRecord {
	updated := state
	updated.Revision++
	updated.UpdatedAt = state.UpdatedAt.Add(time.Minute)
	updated.DescriptionReuse = &api.ReusableDescriptionRecord{SourcePath: sourcePath, Description: description}
	return updated
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
