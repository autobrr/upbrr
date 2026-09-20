// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHistoryOrphanCleanupRemovesLegacyUIOnlySources(t *testing.T) {
	t.Parallel()
	for _, schema := range []string{"source_path", "id_data"} {
		t.Run(schema, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			repo, err := db.Open(filepath.Join(root, "history.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(root, "Retained.Release.mkv")
			orphan := filepath.Join(root, "Deleted.Release.mkv")
			if err := repo.Save(t.Context(), db.FileMetadata{
				Path:      retained,
				Title:     "Retained Release",
				UpdatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			definition := `CREATE TABLE ui_states (id TEXT PRIMARY KEY, data TEXT)`
			if schema == "source_path" {
				definition = `CREATE TABLE ui_states (source_path TEXT)`
			}
			if _, err := repo.RawDB().ExecContext(t.Context(), definition); err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{retained, orphan} {
				if schema == "source_path" {
					_, err = repo.RawDB().ExecContext(t.Context(), `INSERT INTO ui_states(source_path) VALUES (?)`, source)
				} else {
					payload, marshalErr := json.Marshal(map[string]string{"sourcePath": source})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					_, err = repo.RawDB().ExecContext(t.Context(), `INSERT INTO ui_states(id,data) VALUES (?,?)`, filepath.Base(source), string(payload))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
			if err := history.cleanupOrphanedHistory(t.Context(), repo); err != nil {
				t.Fatal(err)
			}
			query := `SELECT data FROM ui_states`
			if schema == "source_path" {
				query = `SELECT source_path FROM ui_states`
			}
			var value string
			if err := repo.RawDB().QueryRowContext(t.Context(), query).Scan(&value); err != nil {
				t.Fatal(err)
			}
			if schema == "id_data" {
				var payload map[string]string
				if err := json.Unmarshal([]byte(value), &payload); err != nil {
					t.Fatal(err)
				}
				value = payload["sourcePath"]
			}
			var count int
			if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM ui_states`).Scan(&count); err != nil || count != 1 || value != retained {
				t.Fatalf("legacy cleanup must retain only History source: count=%d retained=%t err=%v", count, value == retained, err)
			}
		})
	}
}

func TestHistoryOrphanCleanupDefersMissingReleaseAuthoritySource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	orphan := filepath.Join(root, "Deleted.Release.mkv")
	if err := repo.Save(t.Context(), db.FileMetadata{Path: orphan, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	workflow := historySubmissionWorkflowState(t, orphan, now)
	workflow.Status = api.WorkflowStatusBlocked
	workflow.Payload = []byte(`{"Workflow":{"Release":{"ID":"missing-release"}}}`)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
		t.Fatal(err)
	}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	if err := history.cleanupOrphanedHistory(t.Context(), repo); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByPath(t.Context(), orphan); err != nil {
		t.Fatalf("ambiguous ownership lost source data: %v", err)
	}
	if _, err := repo.LoadReleaseWorkflowState(t.Context(), workflow.OwnerID, workflow.WorkflowID); err != nil {
		t.Fatalf("ambiguous ownership lost workflow: %v", err)
	}
}

func TestHistoryOrphanCleanupDefersActiveTempCollision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	orphan := filepath.Join(root, "old", "Example.Release.mkv")
	active := filepath.Join(root, "new", "Example.Release.mkv")
	// A placeholder has no visible History entry but still needs cleanup.
	if err := repo.Save(t.Context(), db.FileMetadata{Path: orphan, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	activateHistoryInput(t, repo, active, now)
	tmpRoot, err := db.Subdir(repo.DBPath(), "tmp")
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(tmpRoot, paths.ReleaseTempBaseFor(active, api.ReleaseInfo{}), "capture.png")
	if err := os.MkdirAll(filepath.Dir(capture), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(capture, []byte("active capture"), 0o600); err != nil {
		t.Fatal(err)
	}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	if err := history.cleanupOrphanedHistory(t.Context(), repo); err != nil {
		t.Fatalf("automatic cleanup must not prevent active input recovery: %v", err)
	}
	if _, err := repo.GetByPath(t.Context(), orphan); err != nil {
		t.Fatalf("deferred orphan lost its retry record: %v", err)
	}
	if contents, err := os.ReadFile(capture); err != nil || string(contents) != "active capture" {
		t.Fatalf("active capture changed: %v", err)
	}
}

func TestHistoryOrphanCleanupRemovesWorkflowAndFilesPreservingHistory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	vault, err := releaseworkflow.NewPrivateArtifactVault(filepath.Join(root, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	history.privateVault = vault
	now := time.Now().UTC()
	orphan := filepath.Join(root, "Deleted.Release.mkv")
	retained := filepath.Join(root, "Retained.Release.mkv")
	for _, source := range []string{orphan, retained} {
		if err := os.WriteFile(source, []byte("source media"), 0o600); err != nil {
			t.Fatal(err)
		}
		workflow := historySubmissionWorkflowState(t, source, now)
		workflow.WorkflowID = api.WorkflowID(filepath.Base(source))
		workflow.CreationKey = filepath.Base(source)
		if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
			t.Fatal(err)
		}
		if err := vault.Put(workflow.OwnerID, workflow.WorkflowID, "preview", releaseworkflow.MediaPreviewContent{
			Bytes: []byte("preview"), ContentType: "image/png",
		}, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Save(t.Context(), db.FileMetadata{
		Path:      retained,
		Title:     "Retained Release",
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO release_workflow_effects
		(owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at)
		VALUES (?, ?, 'operation', 'effect', 'client_injection', 'client', 'fingerprint', 'unknown', ?, ?)`,
		"history-owner", filepath.Base(orphan), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	// Repeated startup must be harmless after the first successful cleanup.
	for range 2 {
		if err := history.cleanupOrphanedHistory(t.Context(), repo); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.LoadReleaseWorkflowState(t.Context(), "history-owner", api.WorkflowID(filepath.Base(orphan))); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
		t.Fatalf("orphan workflow survived: %v", err)
	}
	var effects int
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM release_workflow_effects`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("orphan unknown effect survived: count=%d err=%v", effects, err)
	}
	if _, err := vault.Get("history-owner", api.WorkflowID(filepath.Base(orphan)), "preview", now); !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
		t.Fatalf("orphan private artifact survived: %v", err)
	}
	if _, err := repo.LoadReleaseWorkflowState(t.Context(), "history-owner", api.WorkflowID(filepath.Base(retained))); err != nil {
		t.Fatalf("retained workflow removed: %v", err)
	}
	if _, err := vault.Get("history-owner", api.WorkflowID(filepath.Base(retained)), "preview", now); err != nil {
		t.Fatalf("retained private artifact removed: %v", err)
	}
	entries, err := repo.ListHistoryEntries(t.Context())
	if err != nil || len(entries) != 1 || entries[0].SourcePath != retained {
		t.Fatalf("retained history changed: %#v, %v", entries, err)
	}
	for _, source := range []string{orphan, retained} {
		if contents, err := os.ReadFile(source); err != nil || string(contents) != "source media" {
			t.Fatalf("source media changed: %v", err)
		}
	}
}

func TestHistoryOrphanCleanupRetainsRowsWhenPrivateCleanupFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	workflow := historySubmissionWorkflowState(t, filepath.Join(root, "Deleted.Release.mkv"), time.Now().UTC())
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
		t.Fatal(err)
	}
	vaultRoot := filepath.Join(root, "vault")
	vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(vaultRoot); err != nil {
		t.Fatal(err)
	}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	history.privateVault = vault
	if err := history.cleanupOrphanedHistory(t.Context(), repo); err == nil {
		t.Fatal("cleanup unexpectedly succeeded without its vault root")
	}
	if _, err := repo.LoadReleaseWorkflowState(t.Context(), workflow.OwnerID, workflow.WorkflowID); err != nil {
		t.Fatalf("failed cleanup lost retry ownership: %v", err)
	}
}
