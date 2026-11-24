// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOperationInputLogFallsBackToRetainedWorkflowSource(t *testing.T) {
	t.Parallel()

	openFixture := func(t *testing.T) (*db.SQLiteRepository, *PersistentRepository, *logging.Logger, string) {
		t.Helper()
		ctx := t.Context()
		root := t.TempDir()
		dbPath := filepath.Join(root, "workflow.db")
		repo, err := db.OpenContext(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		if err := repo.MigrateContext(ctx); err != nil {
			t.Fatal(err)
		}
		persistent, err := NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		logger, err := logging.New(config.LoggingConfig{Level: "warn"}, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = logger.Close() })
		logger.SetConsoleOutput(io.Discard, io.Discard)
		return repo, persistent, logger, filepath.Join(root, "Example.Release.2026-GRP.mkv")
	}

	t.Run("Start falls back to retained release source", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		repo, persistent, logger, source := openFixture(t)
		vault, err := NewPrivateArtifactVault(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		module, err := New(
			persistent,
			vault,
			testPreparer(),
			WithLogger(logger),
			WithClock(fixedClock{now: time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)}),
			WithIDGenerator(&sequenceIDGenerator{}),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = module.Shutdown(shutdownCtx)
		})
		created := executeCommand(t, module, CreateWorkflowCommand{
			WorkflowID:     "wf-legacy-start",
			IdempotencyKey: "create-legacy-start",
		})
		prepared := executeCommand(t, module, PrepareReleaseCommand{
			WorkflowID:       created.Workflow.ID,
			ExpectedRevision: created.Workflow.Revision,
			Input:            api.PrepareInput{SourcePath: source},
			IdempotencyKey:   "prepare-legacy-start",
		})
		state, err := persistent.Load(ctx, testOwnerID, created.Workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.SourcePath != "" {
			t.Fatalf("expected blank legacy state.SourcePath, got %q", state.SourcePath)
		}

		operation, err := module.Start(ctx, testOwnerID, PrepareReleaseCommand{
			WorkflowID:       prepared.Workflow.ID,
			ExpectedRevision: prepared.Workflow.Revision,
			Input:            api.PrepareInput{SourcePath: source, Force: true},
			IdempotencyKey:   "reprepare-legacy-start",
		})
		if err != nil {
			t.Fatalf("start preparation: %v", err)
		}
		waitForWorkflowOperation(t, module, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
			return status.Status == api.StageStatusCompleted
		})
		if err := module.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown module: %v", err)
		}

		attempts, err := repo.InputLogs(ctx, source, "")
		if err != nil || len(attempts) != 1 || attempts[0].Kind != string(operation.Operation) || attempts[0].Outcome != string(api.StageStatusCompleted) {
			t.Fatalf("legacy Start input logs = %#v, %v", attempts, err)
		}
	})

	t.Run("recoverOperationAfterLease falls back to retained release source", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		repo, persistent, logger, source := openFixture(t)
		vaultRoot := t.TempDir()
		vaultA, err := NewPrivateArtifactVault(vaultRoot)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
		started := make(chan struct{})
		release := make(chan struct{})
		closeRelease := sync.OnceFunc(func() { close(release) })
		base := testPreparer()
		blockingPreparer := ReleasePreparerFunc{
			PrepareFunc: func(callCtx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				if input.Force {
					close(started)
					select {
					case <-callCtx.Done():
						return api.PrepareResult{}, fmt.Errorf("canceled stalled worker: %w", callCtx.Err())
					case <-release:
					}
				}
				return base.Prepare(callCtx, input)
			},
			DisplayFunc:   base.ResolveDisplay,
			SubjectFunc:   base.ResolveUploadSubject,
			DuplicateFunc: base.ResolveDuplicateSubject,
		}
		moduleA, err := New(
			persistent,
			vaultA,
			blockingPreparer,
			WithLogger(logger),
			WithClock(fixedClock{now: now}),
			WithIDGenerator(&sequenceIDGenerator{}),
			WithProcessEpoch("epoch-legacy-a"),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			closeRelease()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = moduleA.Shutdown(shutdownCtx)
		})
		created := executeCommand(t, moduleA, CreateWorkflowCommand{
			WorkflowID:     "wf-legacy-recover",
			IdempotencyKey: "create-legacy-recover",
		})
		prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
			WorkflowID:       created.Workflow.ID,
			ExpectedRevision: created.Workflow.Revision,
			Input:            api.PrepareInput{SourcePath: source},
			IdempotencyKey:   "prepare-legacy-recover",
		})
		operation, err := moduleA.Start(ctx, testOwnerID, PrepareReleaseCommand{
			WorkflowID:       prepared.Workflow.ID,
			ExpectedRevision: prepared.Workflow.Revision,
			Input:            api.PrepareInput{SourcePath: source, Force: true},
			IdempotencyKey:   "reprepare-legacy-recover",
		})
		if err != nil {
			t.Fatalf("start operation before recovery: %v", err)
		}
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("stalled operation did not reach prepare barrier")
		}
		moduleA.operationWorkersMu.Lock()
		workerA := moduleA.operationWorkers[operation.ID]
		moduleA.operationWorkersMu.Unlock()

		vaultB, err := NewPrivateArtifactVault(vaultRoot)
		if err != nil {
			t.Fatal(err)
		}
		moduleB, err := New(
			persistent,
			vaultB,
			base,
			WithLogger(logger),
			WithClock(fixedClock{now: now.Add(2 * workflowWorkLeaseTTL)}),
			WithIDGenerator(&sequenceIDGenerator{}),
			WithProcessEpoch("epoch-legacy-b"),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = moduleB.Shutdown(shutdownCtx)
		})
		if err := moduleB.ensureOperationRecovery(ctx); err != nil {
			t.Fatalf("recover interrupted operation: %v", err)
		}
		waitForWorkflowOperation(t, moduleB, prepared.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
			return status.Status == api.StageStatusCompleted
		})
		if err := moduleB.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown recovered module: %v", err)
		}
		workerA.cancel()
		closeRelease()
		if workerA.done != nil {
			select {
			case <-workerA.done:
			case <-time.After(10 * time.Second):
				t.Fatal("stalled operation worker did not exit")
			}
		}

		attempts, err := repo.InputLogs(ctx, source, "")
		if err != nil || len(attempts) != 2 || attempts[0].Kind != string(operation.Operation) || attempts[1].Kind != string(operation.Operation) ||
			attempts[0].Outcome != string(api.StageStatusCompleted) || attempts[1].Outcome != string(api.StageStatusInterrupted) {
			t.Fatalf("recovered legacy operation input logs = %#v, %v", attempts, err)
		}
	})
}

type hookIDGenerator struct {
	base   sequenceIDGenerator
	onOpen func() error
}

func (g *hookIDGenerator) NewID(prefix string) (string, error) {
	if prefix == "input_open" && g.onOpen != nil {
		if err := g.onOpen(); err != nil {
			return "", err
		}
	}
	return g.base.NewID(prefix)
}

func TestOpenInputLogSurvivesPreReservationDeletion(t *testing.T) {
	t.Parallel()

	newFixture := func(t *testing.T) (*db.SQLiteRepository, *PersistentRepository, *logging.Logger, string) {
		t.Helper()
		ctx := t.Context()
		root := t.TempDir()
		dbPath := filepath.Join(root, "workflow.db")
		repo, err := db.OpenContext(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		if err := repo.MigrateContext(ctx); err != nil {
			t.Fatal(err)
		}
		persistent, err := NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		logger, err := logging.New(config.LoggingConfig{
			Level:          "warn",
			FileEnabled:    true,
			MaxTotalSizeMB: 1,
			MaxFiles:       2,
		}, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = logger.Close() })
		logger.SetConsoleOutput(io.Discard, io.Discard)
		return repo, persistent, logger, root
	}

	for _, tc := range []struct {
		name        string
		failOpen    bool
		wantOutcome string
	}{
		{name: "completed retry with verified source rebinding", wantOutcome: "completed"},
		{
			name:        "failed retry after pre-reservation delete",
			failOpen:    true,
			wantOutcome: "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			repo, persistent, logger, root := newFixture(t)
			requested := filepath.Join(root, "Alias", "Retry.Release.2026-GRP.mkv")
			verified := filepath.Join(root, "Canonical", "Retry.Release.2026-GRP.mkv")
			unrelated := filepath.Join(root, "Unrelated", "Stored.Release.2026-GRP.mkv")

			// Seed unrelated stored release + upload + input log that must stay untouched.
			if err := repo.Save(ctx, db.FileMetadata{Path: unrelated, Title: "Unrelated Stored"}); err != nil {
				t.Fatal(err)
			}
			if err := repo.CreateUploadRecord(ctx, db.UploadRecord{
				Tracker:    "BLU",
				Status:     "uploaded",
				SourcePath: unrelated,
			}); err != nil {
				t.Fatal(err)
			}
			unrelatedCtx, unrelatedID, finishUnrelated := logger.BeginInputLog(ctx, repo, unrelated, "preparation")
			logging.FromContext(unrelatedCtx, logger).Infof("unrelated release log")
			finishUnrelated("", "completed")

			// Seed older failed log-only attempt for the target source.
			oldCtx, oldID, finishOld := logger.BeginInputLog(ctx, repo, requested, "open input")
			logging.FromContext(oldCtx, logger).Warnf("older failed attempt detail")
			finishOld("", "failed")

			deletedAtBoundary := false
			verifyErr := errors.New("synthetic source verification failure")
			ids := &hookIDGenerator{
				onOpen: func() error {
					// Runs inside OpenInput right between beginInputLog and CompareAndSwapActiveInput.
					before, err := repo.InputLogs(ctx, requested, "")
					if err != nil {
						return fmt.Errorf("inspect attempts before pre-reservation delete: %w", err)
					}
					if len(before) != 2 {
						return fmt.Errorf("expected old + in-flight attempts before pre-reservation delete, got %#v", before)
					}
					if err := repo.DeleteInputLogs(ctx, requested); err != nil {
						return fmt.Errorf("pre-reservation DeleteInputLogs: %w", err)
					}
					after, err := repo.InputLogs(ctx, requested, "")
					if err != nil {
						return fmt.Errorf("inspect attempts after pre-reservation delete: %w", err)
					}
					if len(after) != 0 {
						return fmt.Errorf("expected both rows deleted at pre-reservation boundary, got %#v", after)
					}
					deletedAtBoundary = true
					return nil
				},
			}
			module, err := New(
				persistent,
				NewMemoryPrivateResourceStore(),
				testPreparer(),
				WithLogger(logger),
				WithClock(fixedClock{now: time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)}),
				WithIDGenerator(ids),
				WithActiveInputs(repo, func(verifyCtx context.Context, _ api.PrepareInput) (api.InputRecord, error) {
					logging.FromContext(verifyCtx, logger).Infof("new retry verification detail")
					if tc.failOpen {
						return api.InputRecord{}, verifyErr
					}
					return api.InputRecord{
						CanonicalPath: verified,
						SourceVersion: "retry-v1",
						Manifest:      []byte(`{"Identity":{"Digest":"retry-v1"}}`),
					}, nil
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = module.Shutdown(shutdownCtx)
			})

			_, openErr := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
				Input:          api.PrepareInput{SourcePath: requested},
				IdempotencyKey: "open-retry-race",
			})
			if tc.failOpen {
				if !errors.Is(openErr, verifyErr) {
					t.Fatalf("OpenInput err = %v, want %v", openErr, verifyErr)
				}
			} else if openErr != nil {
				t.Fatalf("OpenInput: %v", openErr)
			}
			if !deletedAtBoundary {
				t.Fatal("pre-reservation DeleteInputLogs boundary did not execute")
			}

			wantSource := verified
			if tc.failOpen {
				wantSource = requested
			}
			got, err := repo.InputLogs(ctx, wantSource, "")
			if err != nil || len(got) != 1 || got[0].ID == oldID || got[0].SourcePath != wantSource ||
				got[0].RequestedPath != requested || got[0].Kind != "open input" || got[0].Outcome != tc.wantOutcome {
				t.Fatalf("InputLogs(%q) after finish = %#v, %v", wantSource, got, err)
			}
			if gotOld, err := repo.InputLogs(ctx, "", oldID); err != nil || len(gotOld) != 0 {
				t.Fatalf("old deleted attempt reappeared: %#v, %v", gotOld, err)
			}
			for _, query := range []string{requested, wantSource} {
				gotRO, err := db.ReadOnlyInputLogs(ctx, repo.DBPath(), query)
				if err != nil || len(gotRO) != 1 || gotRO[0].ID != got[0].ID || gotRO[0].Outcome != tc.wantOutcome {
					t.Fatalf("ReadOnlyInputLogs(%q) = %#v, %v", query, gotRO, err)
				}
			}
			exported, err := logging.ReadInputLogs(ctx, logger, repo.DBPath(), got)
			if err != nil || !strings.Contains(exported.Text, "new retry verification detail") ||
				strings.Contains(exported.Text, "older failed attempt detail") {
				t.Fatalf("exported retry log = %#v, %v", exported, err)
			}

			// Unrelated release metadata, uploads, and logs remain intact.
			if meta, err := repo.GetByPath(ctx, unrelated); err != nil || meta.Title != "Unrelated Stored" {
				t.Fatalf("unrelated metadata changed: %#v, %v", meta, err)
			}
			if uploads, err := repo.ListUploadHistoryByPath(ctx, unrelated); err != nil || len(uploads) != 1 || uploads[0].Tracker != "BLU" {
				t.Fatalf("unrelated uploads changed: %#v, %v", uploads, err)
			}
			if unrelatedLogs, err := repo.InputLogs(ctx, unrelated, ""); err != nil || len(unrelatedLogs) != 1 || unrelatedLogs[0].ID != unrelatedID {
				t.Fatalf("unrelated input logs changed: %#v, %v", unrelatedLogs, err)
			}
		})
	}
}
