// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHistoryDeleteClosesPreparedIdleInput(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "one", true: "all"}[all], func(t *testing.T) {
			core, repo, source, opened := preparedIdleInputForTest(t)
			if all {
				if count, err := core.DeleteAllHistoryReleases(t.Context()); err != nil || count != 1 {
					t.Fatalf("delete all idle history: count=%d err=%v", count, err)
				}
			} else if err := core.DeleteHistoryRelease(t.Context(), source); err != nil {
				t.Fatalf("delete prepared idle history: %v", err)
			}
			slot, err := repo.LoadActiveInput(t.Context())
			if err != nil || slot.State != api.ActiveInputEmpty {
				t.Fatalf("deleted input remains attached: %s, %v", slot.State, err)
			}
			if _, err := repo.LoadReleaseWorkflowState(t.Context(), "input-owner", opened.Current.Workflow.ID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
				t.Fatalf("deleted workflow remains: %v", err)
			}
			entries, err := repo.ListHistoryEntries(t.Context())
			if err != nil || len(entries) != 0 {
				t.Fatalf("deleted history remains: count=%d err=%v", len(entries), err)
			}
		})
	}
}

func TestFreshCoreDoesNotLoadPreviousPreparedInput(t *testing.T) {
	previous, repo, _, _ := preparedIdleInputForTest(t)
	if err := previous.workflow.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := restartedInputCoreForTest(t, repo)
	view, err := restarted.GetActiveInput(t.Context(), "input-owner")
	if err != nil || view.State != api.ActiveInputEmpty || view.Current != nil || view.InputID != "" {
		t.Fatalf("fresh startup loaded previous input: state=%s current=%t err=%v", view.State, view.Current != nil, err)
	}
	entries, err := repo.ListHistoryEntries(t.Context())
	if err != nil || len(entries) != 1 {
		t.Fatalf("startup removed retained history: count=%d err=%v", len(entries), err)
	}
}

func TestFreshCoreKeepsUncertainInputWithoutLoadingIt(t *testing.T) {
	previous, repo, _, opened := preparedIdleInputForTest(t)
	if err := previous.workflow.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO release_workflow_effects
		(owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at)
		VALUES (?, ?, 'external-operation', 'uncertain-effect', 'client_injection', 'client', 'fingerprint', 'unknown', ?, ?)`,
		"input-owner", opened.Current.Workflow.ID, now, now); err != nil {
		t.Fatal(err)
	}
	restarted := restartedInputCoreForTest(t, repo)
	view, err := restarted.GetActiveInput(t.Context(), "input-owner")
	if err != nil || view.State != api.ActiveInputEmpty || view.Current != nil || view.InputID != "" {
		t.Fatalf("startup loaded uncertain input: state=%s current=%t err=%v", view.State, view.Current != nil, err)
	}
	if len(view.RecoveryWorkflowIDs) != 1 || view.RecoveryWorkflowIDs[0] != opened.Current.Workflow.ID {
		t.Fatalf("startup lost manual recovery discovery: %v", view.RecoveryWorkflowIDs)
	}
	var unknown int
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM release_workflow_effects WHERE status = 'unknown'`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatalf("startup changed unknown outcome: count=%d err=%v", unknown, err)
	}
	recovered, err := restarted.RecoverLegacyActiveInput(t.Context(), "input-owner", api.RecoverLegacyActiveInputRequest{WorkflowID: opened.Current.Workflow.ID})
	if err != nil || recovered.Current == nil || len(recovered.Current.Workflow.RequiredActions) != 1 {
		t.Fatalf("explicit recovery unavailable after startup: current=%t err=%v", recovered.Current != nil, err)
	}
}

func restartedInputCoreForTest(t *testing.T, repo *db.SQLiteRepository) *Core {
	t.Helper()
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = repo.DBPath()
	for name, client := range cfg.TorrentClients {
		client.URL, client.Username, client.Password = "http://127.0.0.1:1", "test", "test"
		cfg.TorrentClients[name] = client
	}
	restarted, err := NewWithContext(t.Context(), api.CoreDependencies{
		Config:              cfg,
		RepositoryOwner:     repo,
		Repository:          repo.RepositoryCapabilities(),
		SkipCookieMigration: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.workflow.Shutdown(context.Background()) })
	return restarted
}

func preparedIdleInputForTest(t *testing.T) (*Core, *db.SQLiteRepository, string, api.ActiveInputSnapshot) {
	t.Helper()
	root := t.TempDir()
	repo, err := db.Open(filepath.Join(root, "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := releaseworkflow.New(persistent, releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{}, nil
		}, PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			if err := repo.Save(ctx, db.FileMetadata{
				Path:      input.SourcePath,
				Title:     "Example Release",
				UpdatedAt: time.Now().UTC(),
			}); err != nil {
				return api.PrepareResult{}, fmt.Errorf("save prepared history fixture: %w", err)
			}
			return api.PrepareResult{Release: api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: input.SourcePath}}}, nil
		}},
		releaseworkflow.WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "sample",
				Manifest:      []byte(`{"identity":{"digest":"sample"}}`),
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.Shutdown(context.Background()) })
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	history.activeInputs = repo
	core := &Core{
		workflow: workflow,
		history:  history,
		logger:   api.NopLogger{},
	}
	source := filepath.Join(root, "Example.Release.mkv")
	opened, err := core.OpenActiveInput(t.Context(), "input-owner", api.OpenActiveInputRequest{
		Request: api.ContinueReleaseWorkflowRequest{
			IdempotencyKey: "prepare-idle-input",
			Goal:           api.WorkflowGoalPrepared,
			Intent:         api.WorkflowIntent{Preparation: &api.PrepareInput{SourcePath: source}},
		},
	})
	if err != nil || opened.Current == nil {
		t.Fatalf("prepare input: current=%t err=%v", opened.Current != nil, err)
	}
	if opened.Current.Operation != nil {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			operation, err := workflow.Operation(ctx, "input-owner", opened.Current.Workflow.ID, opened.Current.Operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if operation.Status == api.StageStatusCompleted {
				break
			}
			if operation.Status != api.StageStatusQueued && operation.Status != api.StageStatusRunning {
				t.Fatalf("preparation did not complete: %s, failures=%+v", operation.Status, operation.Failures)
			}
			select {
			case <-ctx.Done():
				t.Fatal("preparation did not finish")
			case <-ticker.C:
			}
		}
	}
	return core, repo, source, opened
}
