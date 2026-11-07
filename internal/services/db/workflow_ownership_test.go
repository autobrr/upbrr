// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowOwnershipBackfillRollsBackAndRestarts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if err := migrateAddReleaseWorkflowStates(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE input_workflow_associations (owner_id TEXT, workflow_id TEXT, canonical_path TEXT)`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "source.mkv"),
		filepath.Join(root, "preparation.mkv"),
		filepath.Join(root, "composite.mkv"),
		filepath.Join(root, "releases", "child.mkv"),
		filepath.Join(root, "associated.mkv"),
	}
	payload, err := json.Marshal(storedReleaseWorkflowSource{
		SourcePath:       paths[0],
		PreparationInput: &api.PrepareInput{SourcePath: paths[1]},
		Composite: &storedReleaseWorkflowCompositeSource{
			Intent: storedReleaseWorkflowIntentSource{Preparation: &api.PrepareInput{SourcePath: paths[2]}},
		},
		Releases: map[api.ReleaseSnapshotID]api.ReleaseSnapshot{
			"release": {Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: paths[3]}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, workflow := range []struct {
		id      string
		payload []byte
	}{
		{id: "a-valid", payload: payload},
		{id: "b-malformed", payload: []byte(`{"Releases":`)},
	} {
		if _, err := raw.ExecContext(ctx, `INSERT INTO release_workflow_states
			(owner_id, workflow_id, revision, status, state_json, created_at, updated_at) VALUES ('owner', ?, 7, 'blocked', ?, 'retained', 'retained')`,
			workflow.id, workflow.payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO input_workflow_associations VALUES ('owner', 'a-valid', ?)`, paths[4]); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := &interruptedOwnershipBackfill{migrationExecutor: tx, remaining: 1}
	if err := migrateWorkflowOwnership(ctx, interrupted); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted backfill = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var exists int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'release_workflow_ownership'`).Scan(&exists); err != nil || exists != 0 {
		t.Fatalf("partial backfill escaped rollback: tables=%d err=%v", exists, err)
	}
	for range 2 {
		tx, err := raw.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateWorkflowOwnership(ctx, tx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := raw.QueryContext(ctx, `SELECT source_path FROM release_workflow_sources WHERE workflow_id = 'a-valid' ORDER BY source_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		got = append(got, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	if !slices.Equal(got, paths) {
		t.Fatalf("backfilled sources = %v, want %v", got, paths)
	}
	var revision int
	var ambiguous, empty bool
	var retained []byte
	if err := raw.QueryRowContext(ctx, `SELECT revision, ambiguous, empty FROM release_workflow_ownership WHERE workflow_id = 'b-malformed'`).
		Scan(&revision, &ambiguous, &empty); err != nil || revision != 7 || !ambiguous || empty {
		t.Fatalf("malformed evidence classification: revision=%d ambiguous=%v empty=%v err=%v", revision, ambiguous, empty, err)
	}
	if err := raw.QueryRowContext(ctx, `SELECT state_json FROM release_workflow_states WHERE workflow_id = 'a-valid'`).Scan(&retained); err != nil || string(retained) != string(payload) {
		t.Fatalf("backfill changed retained payload: err=%v", err)
	}
	if err := requireWorkflowOwnershipCertain(ctx, raw); !errors.Is(err, errWorkflowOwnershipUncertain) {
		t.Fatalf("ambiguous ownership cleanup = %v", err)
	}
}

type interruptedOwnershipBackfill struct {
	migrationExecutor
	remaining int
}

func (exec *interruptedOwnershipBackfill) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO release_workflow_ownership") {
		if exec.remaining == 0 {
			return nil, context.Canceled
		}
		exec.remaining--
	}
	result, err := exec.migrationExecutor.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("interrupted ownership backfill: %w", err)
	}
	return result, nil
}

func TestWorkflowOwnershipQueriesDoNotReadUnrelatedSnapshots(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	root := t.TempDir()
	selected := filepath.Join(root, "selected.mkv")
	unrelated := filepath.Join(root, "unrelated.mkv")
	for _, workflow := range []api.ReleaseWorkflowStateRecord{
		orphanHistoryWorkflowStateForTest(t, "selected", api.WorkflowStatusCompleted, now, selected),
		orphanHistoryWorkflowStateForTest(t, "unrelated", api.WorkflowStatusCompleted, now, unrelated),
	} {
		if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := repo.withWriteTx(ctx, "repeat workflow backfill", func(tx *sql.Tx) error {
			return migrateStructuredWorkflowStorage(ctx, tx)
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A read of this unrelated payload would fail to decode. Ownership remains
	// at its exact committed revision and must answer these independent queries.
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE release_workflow_states SET state_json = ? WHERE workflow_id = 'unrelated'`, []byte(`{"Releases":`)); err != nil {
		t.Fatal(err)
	}
	paths, err := repo.ListStoredReleasePaths(ctx)
	if err != nil || !slices.Contains(paths, selected) || !slices.Contains(paths, unrelated) {
		t.Fatalf("stored sources read snapshots: count=%d err=%v", len(paths), err)
	}
	matches, err := matchingStoredReleaseWorkflowStates(ctx, repo.RawDB(), selected)
	if err != nil || len(matches) != 1 || matches[0].workflowID != "selected" {
		t.Fatalf("source matching read snapshots: matches=%v err=%v", matches, err)
	}
	if err := repo.WithHistoryDeletion(ctx, func(ctx context.Context) error {
		snapshot, err := repo.LoadHistoryCleanupSnapshot(ctx, selected)
		if err != nil || len(snapshot.WorkflowScopes) != 1 || snapshot.WorkflowScopes[0].WorkflowID != "selected" {
			return fmt.Errorf("cleanup snapshot: scopes=%v err=%w", snapshot.WorkflowScopes, err)
		}
		return repo.PurgeContentData(ctx, selected)
	}); err != nil {
		t.Fatal(err)
	}
	paths, err = repo.ListStoredReleasePaths(ctx)
	if err != nil || slices.Contains(paths, selected) || !slices.Contains(paths, unrelated) {
		t.Fatalf("source deletion changed unrelated ownership: count=%d err=%v", len(paths), err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE release_workflow_states SET revision = revision + 1 WHERE workflow_id = 'unrelated'`); err != nil {
		t.Fatal(err)
	}
	if _, err := matchingStoredReleaseWorkflowStates(ctx, repo.RawDB(), unrelated); !errors.Is(err, errWorkflowOwnershipUncertain) {
		t.Fatalf("stale ownership allowed cleanup: %v", err)
	}
}
