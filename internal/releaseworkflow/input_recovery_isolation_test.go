// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestOpenInputScopesDetachedUncertaintyToItsSource(t *testing.T) {
	for _, test := range []struct {
		name      string
		other     bool
		missing   bool
		ambiguous bool
		blocked   bool
	}{
		{name: "same source", blocked: true},
		{name: "unrelated source", other: true},
		{
			name:    "missing authority",
			other:   true,
			missing: true,
			blocked: true,
		},
		{
			name:      "ambiguous authority",
			other:     true,
			ambiguous: true,
			blocked:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			repo := openActiveInputRecoveryRepository(ctx, t)
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			verifier := &hashingActiveInputVerifier{}
			module := newActiveInputRecoveryModule(t, persistent, repo, verifier, &mutableClock{now: time.Now().UTC()}, "queue-isolation")
			sourceA := writeActiveInputRecoverySource(t, "Example.A.2026.mkv", "synthetic source A")
			sourceB := writeActiveInputRecoverySource(t, "Example.B.2026.mkv", "synthetic source B")
			opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{Input: api.PrepareInput{SourcePath: sourceA}, IdempotencyKey: "open-A"})
			if err != nil {
				t.Fatal(err)
			}
			authority := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
			if test.missing || test.ambiguous {
				state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				prior := state.Workflow.Revision
				state.Workflow.Revision++
				if test.missing {
					state.SourcePath, state.PreparationInput = "", nil
				} else {
					state.PreparationInput.SourcePath = sourceB
				}
				if err := persistent.Save(authority, testOwnerID, prior, state); err != nil {
					t.Fatal(err)
				}
			}
			effect := api.ReleaseWorkflowEffectRecord{
				OwnerID:             testOwnerID,
				WorkflowID:          opened.WorkflowID,
				OperationID:         "upload-A",
				EffectID:            "submission-A",
				Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
				ScopeID:             "ALPHA",
				SemanticFingerprint: "synthetic-A",
				StartedAt:           time.Now().UTC(),
				UpdatedAt:           time.Now().UTC(),
			}
			if _, _, err := persistent.BeginEffect(authority, effect); err != nil {
				t.Fatal(err)
			}
			if err := persistent.MarkOperationEffectsUnknown(authority, testOwnerID, opened.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err := module.ReleaseInput(ctx, testOwnerID, opened.Revision); err != nil {
				t.Fatal(err)
			}
			closed, err := repo.LoadActiveInput(ctx)
			if err != nil {
				t.Fatal(err)
			}
			source := sourceA
			if test.other {
				source = sourceB
			}
			next, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
				ExpectedRevision: closed.Revision,
				Input:            api.PrepareInput{SourcePath: source},
				IdempotencyKey:   "open-next",
			})
			if test.blocked {
				if !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) || verifier.calls != 1 {
					t.Fatalf("affected/unknown source admission: err=%v verifications=%d", err, verifier.calls)
				}
				return
			}
			if err != nil || next.WorkflowID == opened.WorkflowID || verifier.calls != 2 {
				t.Fatalf("unrelated source did not open independently: slot=%+v err=%v verifications=%d", next, err, verifier.calls)
			}
			if _, err := module.RecoverLegacyInput(ctx, testOwnerID, opened.WorkflowID); !errors.Is(err, api.ErrActiveInputBusy) {
				t.Fatalf("A recovery replaced active B: %v", err)
			}
			if err := module.ReleaseInput(ctx, testOwnerID, next.Revision); err != nil {
				t.Fatal(err)
			}
			ids, err := module.LegacyRecoveryWorkflowIDs(ctx, testOwnerID)
			if err != nil || !slices.Equal(ids, []api.WorkflowID{opened.WorkflowID}) {
				t.Fatalf("B release lost A recovery: ids=%v err=%v", ids, err)
			}
			if _, err := module.RecoverLegacyInput(ctx, testOwnerID, opened.WorkflowID); err != nil {
				t.Fatal(err)
			}
			current, err := module.Current(ctx, testOwnerID, opened.WorkflowID)
			if err != nil || len(current.Workflow.RequiredActions) != 1 || current.Workflow.RequiredActions[0].EffectScopeID != "ALPHA" {
				t.Fatalf("A lost its exact reconciliation: current=%+v err=%v", current.Workflow, err)
			}
		})
	}
}
