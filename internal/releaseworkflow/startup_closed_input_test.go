// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestStartupClosesRetainedInputWithoutInspectingSource(t *testing.T) {
	for _, test := range []struct {
		name      string
		shutdown  bool
		remove    bool
		rename    bool
		composite bool
	}{
		{name: "normal shutdown with HDR", shutdown: true},
		{name: "forced termination with missing HDR source", remove: true},
		{
			name:      "forced termination with renamed composite source",
			rename:    true,
			composite: true,
		},
		{
			name:      "normal shutdown with inaccessible composite source",
			shutdown:  true,
			composite: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			clock := &mutableClock{now: time.Now().UTC()}
			repo := openActiveInputRecoveryRepository(ctx, t)
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			source := writeActiveInputRecoverySource(t, "source.mkv", "synthetic source")
			first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
			opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
				Input: api.PrepareInput{SourcePath: source}, IdempotencyKey: "open-first",
			})
			if err != nil {
				t.Fatal(err)
			}
			state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			before := state.Workflow.Revision
			state.Workflow.Revision++
			state.Workflow.UpdatedAt = clock.Now()
			if test.composite {
				state.Composite = &compositeUploadSession{Version: compositeUploadSessionVersion, TerminalReason: "feedback_required"}
			} else {
				state.HDRPendingRequest = &PendingHDRRequest{Instructions: api.HDRAnalysisInstructions{
					ProfileVersion: api.HDRAnalysisProfileVersion,
					Release:        api.ReleaseRef{SourcePath: source, Generation: 1},
					TargetIDs:      []string{api.HDRTargetID("first", "")},
				}}
			}
			authority := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
			if err := persistent.Save(authority, testOwnerID, before, state); err != nil {
				t.Fatal(err)
			}
			if test.shutdown {
				if err := first.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				// Expiry models loss of the former process without its shutdown hook.
				clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
			}
			if test.remove {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			}
			if test.rename {
				if err := os.Rename(source, filepath.Join(filepath.Dir(source), "renamed.mkv")); err != nil {
					t.Fatal(err)
				}
			}
			verifier := &hashingActiveInputVerifier{}
			restarted := newActiveInputRecoveryModule(t, persistent, repo, verifier, clock, "second-coordinator")
			checks := 0
			unavailable := errors.New("synthetic source is inaccessible")
			restarted.inputVerifier = func(context.Context, api.PrepareInput) (api.InputRecord, error) {
				checks++
				return api.InputRecord{}, unavailable
			}
			if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
				t.Fatal(err)
			}
			slot, err := repo.LoadActiveInput(ctx)
			if err != nil || slot.State != api.ActiveInputEmpty || checks != 0 {
				t.Fatalf("startup slot=%#v source checks=%d err=%v", slot, checks, err)
			}
			retained, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			if test.composite {
				if retained.Composite == nil || retained.Composite.TerminalReason != "feedback_required" {
					t.Fatal("lost composite checkpoint")
				}
			} else if retained.HDRPendingRequest == nil || retained.HDRPendingRequest.Instructions.Release.SourcePath != source {
				t.Fatal("lost retained HDR request")
			}
			if _, err := repo.LoadInputRecordByID(ctx, opened.InputID); err != nil {
				t.Fatalf("lost verified input record: %v", err)
			}
			if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
				t.Fatal(err)
			}
			if checks != 0 {
				t.Fatal("repeated startup inspected the old source")
			}
			third := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "third-coordinator")
			third.inputVerifier = restarted.inputVerifier
			if err := third.ResetIdleInputOnStartup(ctx); err != nil {
				t.Fatal(err)
			}
			stillEmpty, err := repo.LoadActiveInput(ctx)
			if err != nil || stillEmpty != slot || checks != 0 {
				t.Fatalf("repeated process restart changed empty input: %#v, checks=%d err=%v", stillEmpty, checks, err)
			}
			_, err = restarted.OpenInput(ctx, testOwnerID, OpenInputRequest{
				ExpectedRevision: slot.Revision,
				Input:            api.PrepareInput{SourcePath: source},
				IdempotencyKey:   "unavailable-open",
			})
			if !errors.Is(err, unavailable) || checks != 1 {
				t.Fatalf("explicit unavailable open: checks=%d err=%v", checks, err)
			}
			slot, err = repo.LoadActiveInput(ctx)
			if err != nil || slot.State != api.ActiveInputEmpty {
				t.Fatalf("failed open left active input: %#v, %v", slot, err)
			}
			restarted.inputVerifier = verifier.Verify
			valid := writeActiveInputRecoverySource(t, "other.mkv", "another synthetic source")
			if _, err := restarted.OpenInput(ctx, testOwnerID, OpenInputRequest{
				ExpectedRevision: slot.Revision,
				Input:            api.PrepareInput{SourcePath: valid},
				IdempotencyKey:   "valid-open",
			}); err != nil {
				t.Fatalf("explicit valid open: %v", err)
			}
			if verifier.calls != 1 {
				t.Fatalf("valid source verification count=%d", verifier.calls)
			}
		})
	}
}

func TestStartupDetachesLegacyRecoveryWithoutResolvingUncertainEffect(t *testing.T) {
	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "first-coordinator")
	created, err := first.Execute(ctx, testOwnerID, CreateWorkflowCommand{IdempotencyKey: "legacy-workflow"})
	if err != nil {
		t.Fatal(err)
	}
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          created.Workflow.ID,
		OperationID:         "legacy-operation",
		EffectID:            "legacy-effect",
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "TEST",
		SemanticFingerprint: "synthetic",
		StartedAt:           clock.Now(),
		UpdatedAt:           clock.Now(),
	}
	if _, _, err := persistent.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if slot, err := first.RecoverLegacyInput(ctx, testOwnerID, created.Workflow.ID); err != nil || !IsLegacyRecoverySlot(slot) {
		t.Fatalf("claim legacy recovery: %#v, err=%v", slot, err)
	}
	clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
	restarted := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "second-coordinator")
	if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	slot, err := repo.LoadActiveInput(ctx)
	if err != nil || slot.State != api.ActiveInputEmpty {
		t.Fatalf("legacy startup slot: %#v, err=%v", slot, err)
	}
	workflows, err := restarted.LegacyRecoveryWorkflowIDs(ctx, testOwnerID)
	if err != nil || len(workflows) != 1 || workflows[0] != created.Workflow.ID {
		t.Fatalf("retained recovery workflows: %#v, err=%v", workflows, err)
	}
	retained, err := persistent.Load(ctx, testOwnerID, created.Workflow.ID)
	if err != nil || len(retained.Workflow.RequiredActions) != 1 || retained.Workflow.RequiredActions[0].Kind != api.RequiredActionReconcileSubmission {
		t.Fatalf("retained recovery actions: %#v, err=%v", retained.Workflow.RequiredActions, err)
	}
	_, err = restarted.OpenInput(ctx, testOwnerID, OpenInputRequest{
		ExpectedRevision: slot.Revision,
		Input:            api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "other.mkv", "synthetic source")},
		IdempotencyKey:   "blocked-open",
	})
	if !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("new open bypassed uncertain effect: %v", err)
	}
}
