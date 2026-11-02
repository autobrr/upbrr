// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestStructuredWorkflowMigrationRollsBackAndRestarts(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	legacy := slices.DeleteFunc(slices.Clone(migrationRegistry), func(step migrationStep) bool {
		return step.id == "2026_11_structured_workflow_storage"
	})
	if err := migrateContextWithRegistry(ctx, repo.RawDB(), legacy); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Example.2026.mkv")
	snapshot := map[string]any{
		"id":       "release-v1",
		"revision": 7,
		"release":  map[string]any{"source": map[string]any{"sourcePath": source}, "opaque": "retained evidence"},
	}
	payload, err := json.Marshal(map[string]any{
		"SourcePath": source,
		"Releases":   map[string]any{"release-v1": snapshot},
		"Receipts": map[string]any{"prepare:key": map[string]any{
			"Fingerprint": "exact-command", "Result": map[string]any{"release": snapshot},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for id, data := range map[string][]byte{"known": payload, "malformed": []byte(`{"SourcePath":`)} {
		if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_states
			(owner_id, workflow_id, revision, status, creation_key, creation_fingerprint, state_json, created_at, updated_at)
			VALUES (?, ?, 7, ?, ?, ?, ?, ?, ?)`, "owner-1", id, api.WorkflowStatusCompleted, "create-"+id,
			"exact-create", data, formatWorkflowStateTime(now), formatWorkflowStateTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	interrupted, cancel := context.WithCancel(ctx)
	defer cancel()
	registry := slices.Clone(migrationRegistry)
	for i := range registry {
		if registry[i].id == "2026_11_structured_workflow_storage" {
			registry[i].apply = func(ctx context.Context, exec migrationExecutor) error {
				if err := migrateStructuredWorkflowStorage(ctx, exec); err != nil {
					return err
				}
				cancel()
				return ctx.Err()
			}
		}
	}
	if err := migrateContextWithRegistry(interrupted, repo.RawDB(), registry); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted migration = %v", err)
	}
	var stored []byte
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT state_json FROM release_workflow_states WHERE workflow_id = 'known'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(payload) {
		t.Fatal("interrupted migration retired legacy payload")
	}
	var applied bool
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE id = '2026_11_structured_workflow_storage')`).Scan(&applied); err != nil || applied {
		t.Fatalf("interrupted migration completion = %v, err=%v", applied, err)
	}
	var retained int
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name IN (
		'release_workflow_ownership', 'release_workflow_sources', 'release_workflow_snapshots',
		'release_workflow_snapshot_refs', 'release_workflow_receipts', 'idx_release_workflow_sources_path',
		'idx_release_workflow_snapshot_refs_digest', 'idx_input_workflow_associations_workflow'
	)`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("interrupted migration retained structured storage schema: objects=%d err=%v", retained, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.MigrateContext(ctx); err != nil {
			t.Fatal(err)
		}
		loaded, err := repo.LoadReleaseWorkflowState(ctx, "owner-1", "known")
		if err != nil {
			t.Fatal(err)
		}
		var before, after any
		if err := json.Unmarshal(payload, &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(loaded.Payload, &after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) || loaded.Revision != 7 || loaded.CreationFingerprint != "exact-create" ||
			!loaded.CreatedAt.Equal(now) || !loaded.UpdatedAt.Equal(now) {
			t.Fatal("migration changed logical state, replay evidence, revision, or timestamps")
		}
		malformed, err := repo.LoadReleaseWorkflowState(ctx, "owner-1", "malformed")
		if err != nil || string(malformed.Payload) != `{"SourcePath":` {
			t.Fatalf("malformed evidence was discarded: %q err=%v", malformed.Payload, err)
		}
		if _, err := repo.LoadHistoryCleanupSnapshot(ctx, source); !errors.Is(err, errWorkflowOwnershipUncertain) {
			t.Fatalf("ambiguous legacy ownership permitted cleanup: %v", err)
		}
	}
}

func TestWorkflowStorageLoadKeepsOneRevisionDuringConcurrentSave(t *testing.T) {
	started, release := armConfigActivationReadHook(t)
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	repo := openMigratedTestRepo(t)
	before := workflowStateRecordForTest("pinned", api.WorkflowStatusActive, time.Now().UTC(),
		`{"SourcePath":"synthetic","Releases":{"release":{"id":"release","revision":1}}}`)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(repo.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// A connection-local view pauses only this reader's first SELECT. The
	// writer still updates the real main table using its ordinary repository.
	_, err = repo.RawDB().ExecContext(t.Context(), `CREATE TEMP VIEW release_workflow_states AS
        SELECT owner_id, workflow_id, revision + config_activation_test_read_hook() AS revision,
            status, creation_key, creation_fingerprint, state_json, created_at, updated_at
        FROM main.release_workflow_states`)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		record api.ReleaseWorkflowStateRecord
		err    error
	}
	loaded := make(chan result, 1)
	go func() {
		record, err := repo.LoadReleaseWorkflowState(t.Context(), before.OwnerID, before.WorkflowID)
		loaded <- result{record, err}
	}()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	after := before
	after.Revision = 2
	after.UpdatedAt = before.UpdatedAt.Add(time.Second)
	after.Payload = []byte(`{"SourcePath":"synthetic","Releases":{"release":{"id":"release","revision":2}}}`)
	if err := writer.SaveReleaseWorkflowState(t.Context(), 1, after); err != nil {
		t.Fatal(err)
	}
	close(release)
	released = true
	got := <-loaded
	if got.err != nil {
		t.Fatal(got.err)
	}
	var state struct {
		Releases map[string]struct{ Revision uint64 }
	}
	if err := json.Unmarshal(got.record.Payload, &state); err != nil {
		t.Fatal(err)
	}
	if got.record.Revision != 1 || state.Releases["release"].Revision != 1 {
		t.Fatalf("read changed committed revision: workflow=%d snapshot=%d", got.record.Revision, state.Releases["release"].Revision)
	}
}
