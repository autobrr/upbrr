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

func TestReopenVerifiedInputRestoresAudioAnalysis(t *testing.T) {
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
	version := "verified-a"
	resource := &audioAnalysisReleaseProbe{}
	preparer := audioAnalysisPreparerForTest()
	prepare := preparer.PrepareFunc
	var generation api.PreparedGeneration
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		result, err := prepare(ctx, input)
		if err != nil {
			return result, err
		}
		generation++
		result.Release.Generation = generation
		return result, nil
	}
	module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer,
		WithAudioAnalysisBuilder(&audioAnalysisBuilderFake{resource: resource}),
		WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: version,
				Manifest:      []byte(`{"Identity":{"Digest":"` + version + `"}}`),
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
	input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")}
	first, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "open-first"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       first.WorkflowID,
		ExpectedRevision: 1,
		Input:            input,
		IdempotencyKey:   "prepare-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	analyzed, err := module.Execute(t.Context(), testOwnerID, AnalyzeAudioCommand{
		WorkflowID:       first.WorkflowID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     audioAnalysisInstructionsForTest(*prepared.Release),
		IdempotencyKey:   "analyze-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, first.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err := module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := module.OpenInput(t.Context(), "other-owner", OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-other-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.ReleaseInput(t.Context(), "other-owner", foreign.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err = module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.WorkflowID == first.WorkflowID {
		t.Fatal("reopening replaced the prior workflow history")
	}
	current, err := module.Current(t.Context(), testOwnerID, reopened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AudioAnalysis != nil || current.Workflow.AudioAnalysis != nil {
		t.Fatal("audio analysis was exposed before re-preparation")
	}
	preparedAgain, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       reopened.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preparedAgain.Workflow.AudioAnalysis == nil || !preparedAgain.Workflow.AudioAnalysisEnabled ||
		preparedAgain.AudioAnalysis == nil || preparedAgain.AudioAnalysis.WorkflowID != reopened.WorkflowID ||
		preparedAgain.AudioAnalysis.Release.Generation <= analyzed.AudioAnalysis.Release.Generation ||
		preparedAgain.AudioAnalysis.Release.Generation != preparedAgain.Release.Release.Generation {
		t.Fatalf("reopened audio analysis = %#v", preparedAgain.Workflow.AudioAnalysis)
	}
	content, err := module.AudioAnalysisArtifact(t.Context(), testOwnerID, reopened.WorkflowID,
		*preparedAgain.Workflow.AudioAnalysis, preparedAgain.AudioAnalysis.Tracks[0].Artifacts[0].ID)
	if err != nil {
		t.Fatalf("open restored audio artifact: %v", err)
	}
	if err := content.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resource.releaseCount() != 0 {
		t.Fatal("reopening the same source released its audio artifact")
	}
	otherInput := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Other.Release.2026.mkv")}
	other, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: reopened.Revision,
		Input:            otherInput,
		IdempotencyKey:   "open-other",
	})
	if err != nil {
		t.Fatal(err)
	}
	switchedBack, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: other.Revision,
		Input:            input,
		IdempotencyKey:   "switch-back",
	})
	if err != nil {
		t.Fatal(err)
	}
	if switchedBack.WorkflowID == first.WorkflowID || switchedBack.WorkflowID == reopened.WorkflowID {
		t.Fatalf("switch-back replaced prior workflow history: %s", switchedBack.WorkflowID)
	}
	current, err = module.Current(t.Context(), testOwnerID, switchedBack.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	preparedAfterSwitch, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       switchedBack.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-after-switch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preparedAfterSwitch.Workflow.AudioAnalysis == nil || preparedAfterSwitch.AudioAnalysis == nil ||
		preparedAfterSwitch.AudioAnalysis.WorkflowID != switchedBack.WorkflowID {
		t.Fatalf("switched-back audio analysis = %#v", preparedAfterSwitch.Workflow.AudioAnalysis)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, switchedBack.Revision); err != nil {
		t.Fatal(err)
	}
	legacyState, err := persistent.Load(t.Context(), testOwnerID, switchedBack.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	legacyState.Workflow.Revision++
	legacyState.Workflow.Release = nil
	legacyState.Workflow.AudioAnalysis = nil
	legacyState.Workflow.AudioAnalysisEnabled = false
	legacyRecord, err := workflowStateRecord(testOwnerID, legacyState)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE release_workflow_states
		SET revision = ?, state_json = ? WHERE owner_id = ? AND workflow_id = ?`,
		legacyRecord.Revision, legacyRecord.Payload, testOwnerID, switchedBack.WorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE input_workflow_associations
		SET audio_analysis_id = ? WHERE canonical_path = ? AND owner_id = ?`,
		preparedAfterSwitch.AudioAnalysis.ID, input.SourcePath, testOwnerID); err != nil {
		t.Fatal(err)
	}
	empty, err = module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	legacyOpen, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-legacy-refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyCurrent, err := module.Current(t.Context(), testOwnerID, legacyOpen.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	legacyPrepared, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       legacyOpen.WorkflowID,
		ExpectedRevision: legacyCurrent.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-legacy-refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacyPrepared.AudioAnalysis == nil || legacyPrepared.Workflow.AudioAnalysis == nil {
		t.Fatal("migrated cleared audio reference was not restored")
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, legacyOpen.Revision); err != nil {
		t.Fatal(err)
	}
	version = "verified-b"
	empty, err = module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.WorkflowID == first.WorkflowID {
		t.Fatal("changed source reused its previous workflow")
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
