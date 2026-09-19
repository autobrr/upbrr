// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestActiveInputOpenRefreshRollbackAndOwnerIsolation(t *testing.T) {
	t.Parallel()
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	verifications := 0
	fail := false
	module, err := New(persistent, NewMemoryPrivateResourceStore(), ReleasePreparerFunc{}, WithActiveInputs(repo,
		func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			verifications++
			if fail {
				return api.InputRecord{}, errors.New("synthetic source failure")
			}
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "verified",
				Manifest:      []byte(`{}`),
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.activeMu.Lock()
		cancel, done := module.activeCancel, module.activeDone
		module.activeMu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	request := OpenInputRequest{Input: api.PrepareInput{SourcePath: sourcePath}, IdempotencyKey: "first"}
	first, err := module.OpenInput(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != api.ActiveInputActive || first.WorkflowID == "" || verifications != 1 {
		t.Fatalf("open = %#v, calls %d", first, verifications)
	}
	if _, err := module.ActiveInput(t.Context(), "foreign"); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("foreign read = %v", err)
	}
	if _, err := module.OpenInput(t.Context(), testOwnerID, request); err != nil || verifications != 1 {
		t.Fatalf("idempotent open = %v, calls %d", err, verifications)
	}
	request.ExpectedRevision, request.IdempotencyKey = first.Revision, "refresh"
	refreshed, err := module.OpenInput(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.InputID != first.InputID || refreshed.WorkflowID != first.WorkflowID || verifications != 2 {
		t.Fatalf("refresh = %#v, calls %d", refreshed, verifications)
	}
	fail = true
	request.ExpectedRevision, request.IdempotencyKey = refreshed.Revision, "failed-switch"
	request.Input.SourcePath = filepath.Join(t.TempDir(), "other.mkv")
	if _, err := module.OpenInput(t.Context(), testOwnerID, request); err == nil {
		t.Fatal("failed verification accepted")
	}
	restored, err := module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.InputID != refreshed.InputID || restored.WorkflowID != refreshed.WorkflowID || restored.State != api.ActiveInputActive {
		t.Fatalf("rollback = %#v", restored)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, restored.Revision); err != nil {
		t.Fatal(err)
	}
	state, err := persistent.Load(t.Context(), testOwnerID, refreshed.WorkflowID)
	if err != nil {
		t.Fatalf("load released workflow: %v", err)
	}
	if state.SourcePath != sourcePath {
		t.Fatalf("released workflow source path = %q, want %q", state.SourcePath, sourcePath)
	}
}

func TestContinueInitialOpenRequestsExternalProviderRefresh(t *testing.T) {
	t.Parallel()
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	var verified []api.PrepareInput
	module, err := New(persistent, NewMemoryPrivateResourceStore(), testPreparer(), WithActiveInputs(repo,
		func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			verified = append(verified, input)
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "verified",
				Manifest:      []byte(`{}`),
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.activeMu.Lock()
		cancel, done := module.activeCancel, module.activeDone
		module.activeMu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
	_, err = module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "initial-open-refresh",
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{Preparation: &api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "source.mkv"),
		}},
	})
	if err != nil {
		t.Fatalf("continue initial open: %v", err)
	}
	if len(verified) != 1 || verified[0].ExternalFreshness != api.ExternalFreshnessRefresh {
		t.Fatalf("verified preparation inputs = %#v", verified)
	}
}

func TestClosedInputWorkflowSourceAssociatesTerminalHistoryPurge(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                string
		prepare             bool
		async               bool
		wantOperationStatus api.StageStatus
		wantWorkflowStatus  api.WorkflowStatus
		configure           func(*ReleasePreparerFunc)
	}{
		{name: "draft", wantWorkflowStatus: api.WorkflowStatusDraft},
		{
			name:               "playlist blocked",
			prepare:            true,
			wantWorkflowStatus: api.WorkflowStatusBlocked,
			configure: func(preparer *ReleasePreparerFunc) {
				preparer.PrepareFunc = func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
					return api.PrepareResult{}, &api.PlaylistSelectionRequiredError{
						SourcePath: input.SourcePath,
						Candidates: []api.PlaylistInfo{{ID: "disc:00001.mpls", File: "00001.mpls"}},
					}
				}
			},
		},
		{
			name:                "failed",
			prepare:             true,
			async:               true,
			wantOperationStatus: api.StageStatusFailed,
			wantWorkflowStatus:  api.WorkflowStatusDraft,
			configure: func(preparer *ReleasePreparerFunc) {
				preparer.PrepareFunc = func(context.Context, api.PrepareInput) (api.PrepareResult, error) {
					return api.PrepareResult{}, errors.New("synthetic preparation failure")
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repo, err := db.Open(filepath.Join(t.TempDir(), "history.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			preparer := testPreparer()
			if test.configure != nil {
				test.configure(&preparer)
			}
			module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer, WithActiveInputs(repo,
				func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
					return api.InputRecord{
						CanonicalPath: input.SourcePath,
						SourceVersion: "verified",
						Manifest:      []byte(`{"Identity":{"Digest":"verified"}}`),
					}, nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				module.activeMu.Lock()
				cancel, done := module.activeCancel, module.activeDone
				module.activeMu.Unlock()
				if cancel != nil {
					cancel()
					<-done
				}
			})

			sourcePath := filepath.Join(t.TempDir(), "source.mkv")
			input := api.PrepareInput{SourcePath: sourcePath}
			active, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "open-" + test.name})
			if err != nil {
				t.Fatalf("open input: %v", err)
			}
			if test.prepare {
				command := PrepareReleaseCommand{
					WorkflowID:       active.WorkflowID,
					ExpectedRevision: 1,
					Input:            input,
					IdempotencyKey:   "prepare-" + test.name,
				}
				if !test.async {
					if _, executeErr := module.Execute(t.Context(), testOwnerID, command); executeErr != nil {
						t.Fatalf("prepare workflow: %v", executeErr)
					}
				} else {
					operation, startErr := module.Start(t.Context(), testOwnerID, command)
					if startErr != nil {
						t.Fatalf("start preparation: %v", startErr)
					}
					terminal := waitForWorkflowOperation(t, module, active.WorkflowID, operation.ID, func(status api.WorkflowOperationStatus) bool {
						return isTerminalProgressStatus(status.Status)
					})
					if test.wantOperationStatus != "" && terminal.Status != test.wantOperationStatus {
						t.Fatalf("preparation terminal status = %#v", terminal)
					}
				}
			}
			state, stateErr := persistent.Load(t.Context(), testOwnerID, active.WorkflowID)
			if stateErr != nil {
				t.Fatalf("load retained workflow: %v", stateErr)
			}
			if state.Workflow.Status != test.wantWorkflowStatus {
				t.Fatalf("retained workflow status = %q, want %q", state.Workflow.Status, test.wantWorkflowStatus)
			}
			active, err = module.ActiveInput(t.Context(), testOwnerID)
			if err != nil {
				t.Fatalf("load active input: %v", err)
			}
			if err := module.ReleaseInput(t.Context(), testOwnerID, active.Revision); err != nil {
				t.Fatalf("release input: %v", err)
			}
			if err := repo.PurgeContentData(t.Context(), sourcePath); err != nil {
				t.Fatalf("purge closed %s workflow: %v", test.name, err)
			}
			if _, err := repo.LoadReleaseWorkflowState(t.Context(), testOwnerID, active.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
				t.Fatalf("workflow after purge = %v", err)
			}
		})
	}
}
