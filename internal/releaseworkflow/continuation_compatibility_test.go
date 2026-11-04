// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinuePersistedPreparationCompatibilityAfterRestart(t *testing.T) {
	for _, test := range []struct {
		name, contract string
		forced         bool
	}{
		{name: "incompatible", contract: "prepared-release-v73"},
		{
			name:     "incompatible forced lineage",
			contract: "prepared-release-v73",
			forced:   true,
		},
		{name: "compatible", contract: preparedrelease.ContractVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			contract := test.contract
			ctx := t.Context()
			root := t.TempDir()
			source := filepath.Join(root, "Example.Movie.2026.mkv")
			if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			collector := &continuationCompatibilityCollector{}
			open := func() (*Module, *PersistentRepository, *db.SQLiteRepository) {
				t.Helper()
				database, err := db.Open(filepath.Join(root, "workflow.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				if err := database.Migrate(); err != nil {
					t.Fatal(err)
				}
				preparer, err := preparedrelease.New(database, continuationCompatibilityIdentity{}, collector)
				if err != nil {
					t.Fatal(err)
				}
				repository, err := NewPersistentRepository(database)
				if err != nil {
					t.Fatal(err)
				}
				module, err := New(repository, NewMemoryPrivateResourceStore(), preparer)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = module.Shutdown(context.Background())
					_ = database.Close()
				})
				return module, repository, database
			}
			first, repository, database := open()
			input := api.PrepareInput{
				SourcePath: source,
				Force:      test.forced,
				Instructions: api.ReleaseFactInstructions{
					Identity:    api.ExternalIDOverrides{TMDBID: new(1234567)},
					ReleaseName: api.ReleaseNameOverrides{Edition: new("Manual Cut")},
				},
			}
			current := executeCommand(t, first, CreateWorkflowCommand{Instructions: input.Instructions})
			current = executeCommand(t, first, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
				TrackerIDs:       []api.TrackerID{"BETA"},
			})
			state, err := repository.Load(ctx, testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Model bytes written by an older build without changing the accepted lineage.
			snapshot := *current.Release
			snapshot.Release.Compatibility.ContractVersion = contract
			snapshot.Fingerprint, err = snapshot.ComputeFingerprint()
			if err != nil {
				t.Fatal(err)
			}
			state.Releases[snapshot.ID] = snapshot
			state.Workflow.Revision++
			if err := repository.Save(ctx, testOwnerID, current.Workflow.Revision, state); err != nil {
				t.Fatal(err)
			}
			if err := database.CommitPreparedRelease(ctx, snapshot.Release); err != nil {
				t.Fatal(err)
			}
			if err := first.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, repository, _ := open()
			current, err = restarted.Current(ctx, testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			input.Force = false
			request := api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
				IdempotencyKey: "restart-continuation",
				Goal:           api.WorkflowGoalInputReady,
				Intent:         api.WorkflowIntent{Preparation: &input},
			}
			state, err = repository.Load(ctx, testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if contract != preparedrelease.ContractVersion {
				for _, goal := range []api.WorkflowGoal{api.WorkflowGoalPrepared, api.WorkflowGoalInputReady, api.WorkflowGoalTrackersProjected, api.WorkflowGoalInputReady} {
					request.Goal = goal
					request.IdempotencyKey = "restart-" + string(goal)
					_, err := restarted.Continue(ctx, testOwnerID, request)
					failure, ok := api.AsOperationFailure(err)
					if !ok || failure.Code != api.OperationFailureIncompatibleGeneration || failure.Recovery != api.OperationRecoveryReprepare {
						t.Fatalf("incompatible continuation goal=%s: failure=%+v err=%v", goal, failure, err)
					}
				}
				unchanged, err := repository.Load(ctx, testOwnerID, current.Workflow.ID)
				if err != nil || !reflect.DeepEqual(unchanged, state) || collector.calls.Load() != 1 {
					t.Fatalf("rejected continuation changed retained state or collected facts: calls=%d err=%v", collector.calls.Load(), err)
				}
				input.Force = true
				request.Goal = api.WorkflowGoalPrepared
				request.IdempotencyKey = "explicit-reprepare"
				recovery, err := restarted.Continue(ctx, testOwnerID, request)
				if err != nil || recovery.Operation == nil {
					t.Fatalf("explicit continuation reprepare: %v", err)
				}
				waitForWorkflowOperation(t, restarted, current.Workflow.ID, recovery.Operation.ID, func(status api.WorkflowOperationStatus) bool {
					return isTerminalProgressStatus(status.Status)
				})
				current, err = restarted.Current(ctx, testOwnerID, current.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
				if current.Release.Release.Generation != snapshot.Release.Generation+1 || collector.calls.Load() != 2 {
					t.Fatal("explicit preparation did not replace the incompatible generation once")
				}
			}
			current, err = restarted.Current(ctx, testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Release.Release.Compatibility.ContractVersion != preparedrelease.ContractVersion ||
				current.Corrections == nil || current.Corrections.Corrections.ReleaseName.Edition == nil || *current.Corrections.Corrections.ReleaseName.Edition != "Manual Cut" {
				t.Fatalf("preparation lost compatibility or manual correction: %+v", current)
			}
			preparedRef := *current.Workflow.Release
			for range 2 {
				request.Goal = api.WorkflowGoalPrepared
				request.IdempotencyKey = "compatible-prepared"
				request.Authority.ExpectedRevision = current.Workflow.Revision
				current, err = restarted.Continue(ctx, testOwnerID, request)
				if err != nil || *current.Workflow.Release != preparedRef {
					t.Fatalf("compatible continuation replaced its generation: %v", err)
				}
			}
			request.Goal = api.WorkflowGoalInputReady
			request.IdempotencyKey = "compatible-readiness"
			request.Authority.ExpectedRevision = current.Workflow.Revision
			continued, err := restarted.Continue(ctx, testOwnerID, request)
			if err != nil || continued.Operation == nil {
				t.Fatalf("continue compatible readiness: %v", err)
			}
			waitForWorkflowOperation(t, restarted, current.Workflow.ID, continued.Operation.ID, func(status api.WorkflowOperationStatus) bool {
				return isTerminalProgressStatus(status.Status)
			})
			current, err = restarted.Current(ctx, testOwnerID, current.Workflow.ID)
			if err != nil || current.InputReadiness == nil || *current.Workflow.Release != preparedRef || !slices.Equal(current.InputReadiness.SelectedTrackerIDs, []api.TrackerID{"BETA"}) {
				t.Fatalf("compatible hydration did not retain exact authority: readiness=%+v err=%v", current.InputReadiness, err)
			}
			retained, err := repository.Load(ctx, testOwnerID, current.Workflow.ID)
			if err != nil || !slices.Equal(retained.TrackerScope, []api.TrackerID{"BETA"}) {
				t.Fatalf("selected scope changed: %v", err)
			}
			wantCalls := int32(1)
			if contract != preparedrelease.ContractVersion {
				wantCalls++
			}
			if collector.calls.Load() != wantCalls {
				t.Fatal("repeated continuation recollected compatible preparation")
			}
		})
	}
}

type continuationCompatibilityCollector struct{ calls atomic.Int32 }

func (c *continuationCompatibilityCollector) Collect(_ context.Context, request preparationstate.Request) (preparedrelease.CollectedFacts, error) {
	c.calls.Add(1)
	return preparedrelease.CollectedFacts{
		Naming:    api.NamingFacts{Filename: filepath.Base(request.Manifest.SourcePath), ReleaseName: "Example.Movie.2026.1080p-GRP"},
		Resources: preparedrelease.CollectedResources{SourcePath: request.Manifest.SourcePath, VideoPath: request.Manifest.SourcePath},
	}, nil
}

type continuationCompatibilityIdentity struct{}

func (continuationCompatibilityIdentity) Resolve(_ context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	now := time.Now().UTC()
	return externalidentity.Result{
		Identity: api.ExternalIdentity{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			TMDBID:     1234567,
			Category:   api.CanonicalCategoryMovie,
			Conflict:   api.IdentityConflictNone,
			Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceExplicit, Category: api.IdentityProvenanceProvider},
			Resolution: api.IdentityResolutionKey{
				SourceFingerprint: request.SourceFingerprint,
				IntentFingerprint: "continuation",
				ContractVersion:   externalidentity.ContractVersion,
			},
			ResolvedAt: now,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			UpdatedAt:  now,
		},
	}, nil
}

func (*continuationCompatibilityCollector) HydratePrivateResources(_ context.Context, request preparationstate.Request) (preparedrelease.CollectedResources, error) {
	return preparedrelease.CollectedResources{SourcePath: request.Manifest.SourcePath, VideoPath: request.Manifest.SourcePath}, nil
}

func TestIncompatibleContinuationPreservesReviewAndExternalEffectAuthority(t *testing.T) {
	ctx := t.Context()
	preparer := testPreparer()
	base := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		result, err := base(ctx, input)
		result.Release.Compatibility.ContractVersion = "prepared-release-v73"
		return result, err
	}
	module, repository := newTestModule(t, preparer)
	input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.2026.mkv")}
	current := executeCommand(t, module, CreateWorkflowCommand{})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
	})
	state, err := repository.Load(ctx, testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Workflow.Revision++
	action, err := module.newReconcileAction(state.Workflow.Revision, module.clock.Now(), "BETA", api.WorkflowExternalEffectTrackerSubmission, "BETA", "Check the remote outcome.")
	if err != nil {
		t.Fatal(err)
	}
	state.Workflow.RequiredActions = []api.RequiredAction{action, {
		ID:               "retained-approval",
		Kind:             api.RequiredActionApproveTrackers,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: state.Workflow.Revision,
		Prompt:           "Approve the reviewed trackers.",
		CreatedAt:        module.clock.Now(),
	}}
	state.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{
		TrackerID:   "ALPHA",
		Reason:      "confirmed upload",
		ConfirmedAt: module.clock.Now(),
	}}
	state.Workflow.Status = api.WorkflowStatusBlocked
	if err := repository.Save(ctx, testOwnerID, current.Workflow.Revision, state); err != nil {
		t.Fatal(err)
	}
	effects := []api.ReleaseWorkflowEffectRecord{
		{
			OwnerID:     testOwnerID,
			WorkflowID:  current.Workflow.ID,
			OperationID: "retained-operation",
			EffectID:    "tracker-attempt",
			Kind:        string(api.WorkflowExternalEffectTrackerSubmission),
			ScopeID:     "BETA",
			StartedAt:   module.clock.Now(),
		},
		{
			OwnerID:     testOwnerID,
			WorkflowID:  current.Workflow.ID,
			OperationID: "retained-operation",
			EffectID:    "client-attempt",
			Kind:        string(api.WorkflowExternalEffectClientInjection),
			ScopeID:     "client:BETA",
			StartedAt:   module.clock.Now(),
		},
	}
	for _, effect := range effects {
		if _, _, err := repository.BeginEffect(ctx, effect); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.MarkOperationEffectsUnknown(ctx, testOwnerID, current.Workflow.ID, "retained-operation", module.clock.Now()); err != nil {
		t.Fatal(err)
	}
	request := api.ContinueReleaseWorkflowRequest{
		Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: state.Workflow.Revision},
		IdempotencyKey: "retained-safety",
		Goal:           api.WorkflowGoalUploaded,
		Intent:         api.WorkflowIntent{Preparation: &input},
	}
	if _, err := module.Continue(ctx, "other-owner", request); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	request.Authority.ExpectedRevision--
	if _, err := module.Continue(ctx, testOwnerID, request); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	request.Authority.ExpectedRevision++
	for range 2 {
		_, err := module.Continue(ctx, testOwnerID, request)
		failure, ok := api.AsOperationFailure(err)
		if !ok || failure.Recovery != api.OperationRecoveryReprepare {
			t.Fatalf("incompatible recovery: %v", err)
		}
	}
	retained, err := repository.Load(ctx, testOwnerID, current.Workflow.ID)
	if err != nil || !reflect.DeepEqual(retained, state) {
		t.Fatalf("continuation changed reviews, confirmed outcomes or authority: %v", err)
	}
	for _, effect := range effects {
		prior, replayed, err := repository.BeginEffect(ctx, effect)
		if !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) || replayed || prior.EffectID != effect.EffectID || prior.Status != api.WorkflowEffectStatusUnknown {
			t.Fatalf("unknown effect fence changed: prior=%+v replayed=%t err=%v", prior, replayed, err)
		}
	}
}
