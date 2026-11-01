// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"crypto/sha256"
	"encoding/hex"
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
		foreign   bool
		unfenced  bool
		kind      api.WorkflowExternalEffectKind
	}{
		{name: "same source", blocked: true},
		{name: "unrelated source", other: true},
		{name: "foreign tracker owner", foreign: true},
		{
			name:     "foreign unfenced tracker owner",
			foreign:  true,
			unfenced: true,
			blocked:  true,
		},
		{
			name:    "foreign client injection owner",
			foreign: true,
			kind:    api.WorkflowExternalEffectClientInjection,
			blocked: true,
		},
		{
			name:    "foreign image hosting owner",
			foreign: true,
			kind:    api.WorkflowExternalEffectImageHosting,
			blocked: true,
		},
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
			const sourceContents = "synthetic source A"
			sourceA := writeActiveInputRecoverySource(t, "Example.A.2026.mkv", sourceContents)
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
			kind := test.kind
			if kind == "" {
				kind = api.WorkflowExternalEffectTrackerSubmission
			}
			effect := api.ReleaseWorkflowEffectRecord{
				OwnerID:             testOwnerID,
				WorkflowID:          opened.WorkflowID,
				OperationID:         "upload-A",
				EffectID:            "submission-A",
				Kind:                string(kind),
				ScopeID:             "ALPHA",
				SemanticFingerprint: "synthetic-A",
				StartedAt:           time.Now().UTC(),
				UpdatedAt:           time.Now().UTC(),
			}
			if kind == api.WorkflowExternalEffectTrackerSubmission && !test.unfenced {
				digest := sha256.Sum256([]byte(sourceContents))
				identity, err := api.NewSubmissionContentIdentity(api.SubmissionContentScopeSingleFile, []api.SubmissionContentFile{{
					Size: int64(len(sourceContents)), SHA256: hex.EncodeToString(digest[:]),
				}})
				if err != nil {
					t.Fatal(err)
				}
				effect.Submission = &api.SubmissionFenceAuthority{
					ContentIdentity: identity,
					TrackerSite:     "ALPHA|https://alpha.example/",
					CoordinatorID:   opened.CoordinatorID,
					Fence:           opened.Fence,
				}
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
			nextOwner := testOwnerID
			if test.foreign {
				nextOwner = "other-owner"
				if _, err := module.RecoverLegacyInput(ctx, testOwnerID, opened.WorkflowID); err != nil {
					t.Fatal(err)
				}
				if err := module.ResetIdleInputOnStartup(ctx); err != nil {
					t.Fatal(err)
				}
			}
			closed, err := repo.LoadActiveInput(ctx)
			if err != nil || closed.State != api.ActiveInputEmpty {
				t.Fatalf("input was not detached: slot=%+v err=%v", closed, err)
			}
			source := sourceA
			if test.other {
				source = sourceB
			}
			next, err := module.OpenInput(ctx, nextOwner, OpenInputRequest{
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
			if err := module.ReleaseInput(ctx, nextOwner, next.Revision); err != nil {
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
