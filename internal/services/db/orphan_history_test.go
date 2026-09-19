// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSQLiteRepositoryListOrphanedHistoryPaths(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	historyRoot := filepath.Join(t.TempDir(), "retained-history")
	historyChild := filepath.Join(historyRoot, "disc", "feature.mkv")
	historyWorkflowSibling := filepath.Join(t.TempDir(), "retained-workflow-sibling.mkv")
	orphanPlaylist := filepath.Join(t.TempDir(), "orphan-playlist.mkv")
	orphanPlaceholder := filepath.Join(t.TempDir(), "orphan-placeholder.mkv")
	orphanWorkflow := filepath.Join(t.TempDir(), "orphan-workflow.mkv")
	orphanComposite := filepath.Join(t.TempDir(), "orphan-composite.mkv")
	orphanEffect := filepath.Join(t.TempDir(), "orphan-effect.mkv")
	activePath := filepath.Join(t.TempDir(), "active.mkv")
	busyPath := filepath.Join(t.TempDir(), "busy.mkv")

	if err := repo.Save(ctx, FileMetadata{
		Path:       historyRoot,
		SourceSize: 1,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("save visible history: %v", err)
	}
	if err := repo.SavePlaylistSelection(ctx, historyChild, "history", nil, false); err != nil {
		t.Fatalf("save child selection: %v", err)
	}
	if err := repo.SavePlaylistSelection(ctx, orphanPlaylist, "orphan", nil, false); err != nil {
		t.Fatalf("save orphan selection: %v", err)
	}
	if err := repo.Save(ctx, FileMetadata{Path: orphanPlaceholder, UpdatedAt: now}); err != nil {
		t.Fatalf("save placeholder history: %v", err)
	}

	retainedWorkflow := orphanHistoryWorkflowStateForTest(t, "retained-workflow", api.WorkflowStatusCompleted, now,
		historyRoot, historyWorkflowSibling)
	orphanedWorkflow := orphanHistoryWorkflowStateForTest(t, "orphan-workflow", api.WorkflowStatusFailed, now, orphanWorkflow)
	orphanedEffectWorkflow := orphanHistoryWorkflowStateForTest(t, "orphan-effect", api.WorkflowStatusCompleted, now, orphanEffect)
	activeWorkflow := orphanHistoryWorkflowStateForTest(t, "active-workflow", api.WorkflowStatusActive, now, activePath)
	busyWorkflow := orphanHistoryWorkflowStateForTest(t, "busy-workflow", api.WorkflowStatusActive, now, busyPath)
	for _, workflow := range []api.ReleaseWorkflowStateRecord{
		retainedWorkflow, orphanedWorkflow, orphanedEffectWorkflow, activeWorkflow, busyWorkflow,
	} {
		if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
			t.Fatalf("create %s: %v", workflow.WorkflowID, err)
		}
	}
	compositePayload, err := json.Marshal(storedReleaseWorkflowSource{
		Composite: &storedReleaseWorkflowCompositeSource{
			Intent: storedReleaseWorkflowIntentSource{Preparation: &api.PrepareInput{SourcePath: orphanComposite}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compositeWorkflow := workflowStateRecordForTest("orphan-composite", api.WorkflowStatusDraft, now, string(compositePayload))
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, compositeWorkflow); err != nil {
		t.Fatalf("create composite workflow: %v", err)
	}
	insertWorkflowHistoryEffectForTest(t, repo, orphanedEffectWorkflow, api.WorkflowEffectStatusSucceeded, now)
	insertOrphanHistoryBusyOperation(t, repo, busyWorkflow, now)
	setOrphanHistoryActiveWorkflow(t, repo, activeWorkflow)

	paths, scopes, err := repo.ListOrphanedHistoryPaths(ctx)
	if err != nil {
		t.Fatalf("list orphaned history: %v", err)
	}
	wantPaths := []string{orphanComposite, orphanEffect, orphanPlaceholder, orphanPlaylist, orphanWorkflow}
	slices.Sort(wantPaths)
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("orphaned paths = %#v, want %#v", paths, wantPaths)
	}
	if len(scopes) != 0 {
		t.Fatalf("unexpected orphaned workflow scopes = %#v", scopes)
	}
}

func TestSQLiteRepositoryListOrphanedHistoryPathsIncludesLegacyUIStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*testing.T, *SQLiteRepository, string, string, string)
		want  func(string) []string
	}{
		{
			name: "source path schema",
			setup: func(t *testing.T, repo *SQLiteRepository, retained, orphan, _ string) {
				t.Helper()
				if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TABLE ui_states (source_path TEXT)`); err != nil {
					t.Fatalf("create ui states: %v", err)
				}
				if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO ui_states (source_path) VALUES (?), (?)`, retained, orphan); err != nil {
					t.Fatalf("insert ui states: %v", err)
				}
			},
			want: func(orphan string) []string { return []string{orphan} },
		},
		{
			name: "id data schema groups retained path",
			setup: func(t *testing.T, repo *SQLiteRepository, retained, orphan, mixedOrphan string) {
				t.Helper()
				if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TABLE ui_states (id TEXT, data TEXT)`); err != nil {
					t.Fatalf("create ui states: %v", err)
				}
				mixedData, err := json.Marshal(map[string]any{
					"sources": []any{
						map[string]string{"sourcePath": retained},
						map[string]string{"sourcePath": mixedOrphan},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				orphanData, err := json.Marshal(map[string]string{"sourcePath": orphan})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repo.RawDB().ExecContext(t.Context(), `
					INSERT INTO ui_states (id, data) VALUES (?, ?), (?, ?), (?, ?)
				`, "session-id", mixedData, orphan, orphanData, "malformed", `{"sourcePath":`); err != nil {
					t.Fatalf("insert ui states: %v", err)
				}
			},
			want: func(orphan string) []string { return []string{orphan} },
		},
		{
			name: "unsupported schema",
			setup: func(t *testing.T, repo *SQLiteRepository, _, _, _ string) {
				t.Helper()
				if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TABLE ui_states (session_id TEXT, payload TEXT)`); err != nil {
					t.Fatalf("create ui states: %v", err)
				}
			},
			want: func(string) []string { return nil },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repo := openMigratedTestRepo(t)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Second)
			root := t.TempDir()
			retained := filepath.Join(root, "retained.mkv")
			orphan := filepath.Join(root, "orphan.mkv")
			mixedOrphan := filepath.Join(root, "mixed-orphan.mkv")
			if err := repo.Save(ctx, FileMetadata{
Path: retained,
 SourceSize: 1,
 UpdatedAt: now,
}); err != nil {
				t.Fatalf("save retained history: %v", err)
			}
			test.setup(t, repo, retained, orphan, mixedOrphan)

			paths, scopes, err := repo.ListOrphanedHistoryPaths(ctx)
			if err != nil {
				t.Fatalf("list orphaned history: %v", err)
			}
			wantPaths := test.want(orphan)
			if !slices.Equal(paths, wantPaths) {
				t.Fatalf("orphaned paths = %#v, want %#v", paths, wantPaths)
			}
			if len(scopes) != 0 {
				t.Fatalf("unexpected orphaned workflow scopes = %#v", scopes)
			}
		})
	}
}

func TestSQLiteRepositoryListOrphanedHistoryPathsReturnsEmptyWorkflowScopes(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	emptyDraft := workflowStateRecordForTest("empty-draft", api.WorkflowStatusDraft, now, `{}`)
	emptyBlocked := workflowStateRecordForTest("empty-blocked", api.WorkflowStatusBlocked, now, `{"Releases":null}`)
	emptyReceipt := workflowStateRecordForTest("empty-receipt", api.WorkflowStatusBlocked, now, `{
		"OwnerID":"owner",
		"ProcessEpoch":"epoch",
		"Workflow":{"id":"empty-receipt","revision":1,"factInstructions":{"id":"facts","revision":1},"status":"blocked","createdAt":"2026-09-19T00:00:00Z","updatedAt":"2026-09-19T00:00:00Z","requiredActions":[{}]},
		"FactInstructions":{"facts":{}},
		"Receipts":{"create":{"Fingerprint":"fingerprint","Result":{"workflow":{"id":"empty-receipt","revision":1,"factInstructions":{"id":"facts","revision":1},"status":"blocked","createdAt":"2026-09-19T00:00:00Z","updatedAt":"2026-09-19T00:00:00Z","requiredActions":[{}]},"continuation":{}}}}
	}`)
	for _, workflow := range []api.ReleaseWorkflowStateRecord{emptyDraft, emptyBlocked, emptyReceipt} {
		if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
			t.Fatalf("create %s: %v", workflow.WorkflowID, err)
		}
	}

	paths, scopes, err := repo.ListOrphanedHistoryPaths(ctx)
	if err != nil {
		t.Fatalf("list orphaned history: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("unexpected orphaned paths = %#v", paths)
	}
	wantScopes := []api.WorkflowScope{
		{OwnerID: emptyBlocked.OwnerID, WorkflowID: emptyBlocked.WorkflowID},
		{OwnerID: emptyDraft.OwnerID, WorkflowID: emptyDraft.WorkflowID},
		{OwnerID: emptyReceipt.OwnerID, WorkflowID: emptyReceipt.WorkflowID},
	}
	if !slices.Equal(scopes, wantScopes) {
		t.Fatalf("orphaned workflow scopes = %#v, want %#v", scopes, wantScopes)
	}
}

func TestSQLiteRepositoryListOrphanedHistoryPathsDefersSourceLessAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  api.WorkflowStatus
		payload string
		setup   func(*testing.T, *SQLiteRepository, api.ReleaseWorkflowStateRecord, time.Time)
	}{
		{
			name:    "terminal status",
			status:  api.WorkflowStatusFailed,
			payload: `{}`,
		},
		{
			name:    "effect",
			status:  api.WorkflowStatusDraft,
			payload: `{}`,
			setup: func(t *testing.T, repo *SQLiteRepository, workflow api.ReleaseWorkflowStateRecord, now time.Time) {
				insertWorkflowHistoryEffectForTest(t, repo, workflow, api.WorkflowEffectStatusSucceeded, now)
			},
		},
		{
			name:    "fence",
			status:  api.WorkflowStatusBlocked,
			payload: `{}`,
			setup: func(t *testing.T, repo *SQLiteRepository, workflow api.ReleaseWorkflowStateRecord, now time.Time) {
				insertWorkflowHistoryFenceForTest(t, repo, workflow, api.SubmissionContentIdentity{
					Version: "test-version",
					Digest:  "test-digest",
					Scope:   "test-scope",
				}, api.WorkflowEffectStatusSucceeded, now)
			},
		},
		{
			name:    "workflow release without snapshot",
			status:  api.WorkflowStatusBlocked,
			payload: `{"Workflow":{"release":{"id":"missing","revision":1}}}`,
		},
		{
			name:    "receipt release",
			status:  api.WorkflowStatusBlocked,
			payload: `{"Receipts":{"receipt":{"Result":{"release":{}}}}}`,
		},
		{
			name:    "operation result",
			status:  api.WorkflowStatusBlocked,
			payload: `{"Operations":{"operation":{"result":{"kind":"release","workflowRevision":1,"refId":"release","refRevision":1}}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repo := openMigratedTestRepo(t)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Second)
			orphan := filepath.Join(t.TempDir(), "orphan.mkv")
			if err := repo.SavePlaylistSelection(ctx, orphan, "selection", nil, false); err != nil {
				t.Fatal(err)
			}
			workflow := workflowStateRecordForTest("source-less", test.status, now, test.payload)
			if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			if test.setup != nil {
				test.setup(t, repo, workflow, now)
			}

			paths, scopes, err := repo.ListOrphanedHistoryPaths(ctx)
			if err != nil || len(paths) != 0 || len(scopes) != 0 {
				t.Fatalf("source-less authority must defer all cleanup: paths=%v scopes=%v err=%v", paths, scopes, err)
			}
		})
	}
}

func TestSQLiteRepositoryListOrphanedHistoryPathsDefersAmbiguousOwnership(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"malformed JSON", "mixed valid and blank source"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			repo := openMigratedTestRepo(t)
			ctx := t.Context()
			now := time.Now().UTC()
			orphan := filepath.Join(t.TempDir(), "orphan.mkv")
			if err := repo.SavePlaylistSelection(ctx, orphan, "selection", nil, false); err != nil {
				t.Fatal(err)
			}
			empty := workflowStateRecordForTest("empty-draft", api.WorkflowStatusDraft, now, `{}`)
			ambiguous := orphanHistoryWorkflowStateForTest(t, "ambiguous", api.WorkflowStatusBlocked, now,
				filepath.Join(t.TempDir(), "known.mkv"), "")
			if scenario == "malformed JSON" {
				ambiguous.Payload = []byte(`{"Releases":`)
			}
			for _, workflow := range []api.ReleaseWorkflowStateRecord{empty, ambiguous} {
				if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
					t.Fatal(err)
				}
			}
			paths, scopes, err := repo.ListOrphanedHistoryPaths(ctx)
			if err != nil || len(paths) != 0 || len(scopes) != 0 {
				t.Fatalf("ambiguous ownership must defer all cleanup: paths=%v scopes=%v err=%v", paths, scopes, err)
			}
		})
	}
}

func TestSQLiteRepositoryListOrphanedHistoryPathsSkipsBusySourceLessWorkflow(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	workflow := workflowStateRecordForTest("busy-source-less", api.WorkflowStatusDraft, now, `{}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	insertOrphanHistoryBusyOperation(t, repo, workflow, now)

	paths, scopes, err := repo.ListOrphanedHistoryPaths(ctx)
	if err != nil {
		t.Fatalf("list orphaned history: %v", err)
	}
	if len(paths) != 0 || len(scopes) != 0 {
		t.Fatalf("busy source-less workflow cleanup = paths=%#v scopes=%#v", paths, scopes)
	}
}

func orphanHistoryWorkflowStateForTest(
	t *testing.T,
	workflowID api.WorkflowID,
	status api.WorkflowStatus,
	now time.Time,
	sourcePaths ...string,
) api.ReleaseWorkflowStateRecord {
	t.Helper()
	releases := make(map[api.ReleaseSnapshotID]api.ReleaseSnapshot, len(sourcePaths))
	for index, sourcePath := range sourcePaths {
		releases[api.ReleaseSnapshotID(string(rune('a'+index)))] = api.ReleaseSnapshot{
			Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: sourcePath}},
		}
	}
	payload, err := json.Marshal(storedReleaseWorkflowSource{Releases: releases})
	if err != nil {
		t.Fatal(err)
	}
	return workflowStateRecordForTest(workflowID, status, now, string(payload))
}

func insertOrphanHistoryBusyOperation(t *testing.T, repo *SQLiteRepository, workflow api.ReleaseWorkflowStateRecord, now time.Time) {
	t.Helper()
	if _, err := repo.RawDB().ExecContext(t.Context(), `
		INSERT INTO release_workflow_operations (
			owner_id, workflow_id, operation_id, expected_revision, idempotency_key,
			command_fingerprint, command_name, process_epoch, status, sequence,
			operation_json, started_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, workflow.OwnerID, workflow.WorkflowID, "operation", 1, "", "fingerprint", "continue", "process", api.StageStatusQueued, 1,
		[]byte(`{}`), formatWorkflowStateTime(now), formatWorkflowStateTime(now)); err != nil {
		t.Fatalf("insert busy operation: %v", err)
	}
}

func setOrphanHistoryActiveWorkflow(t *testing.T, repo *SQLiteRepository, workflow api.ReleaseWorkflowStateRecord) {
	t.Helper()
	payload, err := json.Marshal(api.ActiveInputRecord{
		State:      api.ActiveInputActive,
		OwnerID:    workflow.OwnerID,
		WorkflowID: workflow.WorkflowID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE active_input SET record_json = ? WHERE singleton = 1`, payload); err != nil {
		t.Fatalf("set active workflow: %v", err)
	}
}
