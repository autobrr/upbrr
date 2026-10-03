// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/otw"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinueReleaseWorkflowAcknowledgesOTWRulesAfterSiblingDuplicateCheck(t *testing.T) {
	t.Parallel()

	registry := trackerspkg.NewRegistry()
	for _, descriptor := range []trackerspkg.Descriptor{
		{
			Name:       "OTW",
			Definition: workflowImageHostPolicyDefinition{name: "OTW"},
			Validation: otw.ValidationPolicy(),
		},
		{Name: "ALPHA", Definition: workflowImageHostPolicyDefinition{name: "ALPHA"}},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	projector, err := trackerspkg.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	const releaseName = "Example.Release.2026.1080p.WEB-DL-GRP"
	var generation atomic.Uint64
	generation.Store(1)
	preparer := releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			if input.Force {
				generation.Add(1)
			}
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: api.PreparedGeneration(generation.Load()),
				Source:     api.SourceManifest{SourcePath: input.SourcePath},
				Naming:     api.NamingFacts{ReleaseName: releaseName},
			}}, nil
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{ReleaseName: releaseName}, nil
		},
		SubjectFunc: func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
			return api.UploadSubject{
				SourcePath:  input.Release.SourcePath,
				FileList:    []string{input.Release.SourcePath},
				ReleaseName: releaseName,
				Source:      "Web",
				Type:        "WEBDL",
				Identity: api.ExternalIdentity{
					SourcePath: input.Release.SourcePath,
					Generation: input.Release.Generation,
					Category:   api.CanonicalCategoryMovie,
					TMDBID:     1234567,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: input.Release.SourcePath,
					Generation: input.Release.Generation,
					TMDB: &api.TMDBMetadata{
						TMDBID: 1234567,
						Title:  "Example Release",
						Year:   2026,
						Genres: "Drama",
					},
				},
				Release: api.ReleaseInfo{
					Title:      "Example Release",
					Year:       2026,
					Resolution: "1080p",
				},
			}, nil
		},
		DuplicateFunc: func(_ context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
			return api.DuplicateSubject{SourcePath: input.Release.SourcePath, ReleaseName: releaseName}, nil
		},
	}
	dupes := &workflowDupeServiceFake{results: []api.DupeCheckResult{
		{
			Tracker: "OTW",
			Status:  "completed",
			Search:  api.DupeSearchEvidence{Complete: true},
		},
		{
			Tracker: "ALPHA",
			Status:  "completed",
			Search:  api.DupeSearchEvidence{Complete: true},
		},
	}}
	module, err := releaseworkflow.New(
		releaseworkflow.NewMemoryRepository(),
		releaseworkflow.NewMemoryPrivateResourceStore(),
		preparer,
		releaseworkflow.WithTrackerProjectionBuilder(projector),
		releaseworkflow.WithTrackerPreflightBuilder(workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}),
		releaseworkflow.WithDupeAssessmentBuilder(workflowDupeBuilder{service: dupes}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
	core := &Core{workflow: module, logger: api.NopLogger{}}
	ctx := releaseworkflow.WithTrackerDecisionMode(t.Context(), releaseworkflow.TrackerDecisionModeWebUIControls)
	const owner = "rule-acknowledgement-owner"
	intent := api.WorkflowIntent{
		Preparation: &api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), releaseName+".mkv")},
		TrackerIDs:  []api.TrackerID{"OTW", "ALPHA"},
	}
	current, err := core.ContinueReleaseWorkflow(ctx, owner, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "open-rule-workflow",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent:         intent,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(key string, answers ...api.RequiredActionAnswer) api.ContinueReleaseWorkflowRequest {
		return api.ContinueReleaseWorkflowRequest{
			Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
			IdempotencyKey: key,
			Goal:           api.WorkflowGoalDuplicatesDecided,
			Intent:         intent,
			Answers:        answers,
		}
	}
	settle := func(key string, answers ...api.RequiredActionAnswer) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			previousRevision := current.Workflow.Revision
			current, err = core.ContinueReleaseWorkflow(ctx, owner, request(key, answers...))
			if err != nil {
				t.Fatalf("continue %s: %v (cause: %v)", key, err, errors.Unwrap(err))
			}
			for current.Operation != nil && (current.Operation.Status == api.StageStatusQueued ||
				current.Operation.Status == api.StageStatusRunning || current.Operation.Status == api.StageStatusPending ||
				current.Operation.Status == api.StageStatusReady) {
				if !time.Now().Before(deadline) {
					t.Fatalf("%s operation did not finish: %#v", key, current.Operation)
				}
				time.Sleep(time.Millisecond)
				current, err = core.CurrentReleaseWorkflow(ctx, owner, current.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			current, err = core.CurrentReleaseWorkflow(ctx, owner, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Operation != nil && current.Operation.Status == api.StageStatusFailed {
				t.Fatalf("%s operation failed: %#v", key, current.Operation)
			}
			if current.Workflow.Revision == previousRevision {
				return
			}
		}
		t.Fatalf("%s continuation did not settle", key)
	}
	projection := func() api.TrackerReleaseProjection {
		t.Helper()
		if current.Projections != nil {
			for _, candidate := range current.Projections.Projections {
				if candidate.TrackerID == "OTW" {
					return candidate
				}
			}
		}
		t.Fatalf("missing OTW projection: %#v", current)
		return api.TrackerReleaseProjection{}
	}
	action := func(confirmed bool) api.RequiredAction {
		t.Helper()
		actions := current.Workflow.RequiredActions
		status := api.RequiredActionStatusPending
		if !confirmed {
			actions = projection().RequiredActions
			status = api.RequiredActionStatusResolved
		}
		for _, candidate := range actions {
			if candidate.Kind == api.RequiredActionAuthorizeRules && candidate.TrackerID == "OTW" && candidate.Status == status {
				return candidate
			}
		}
		t.Fatalf("missing OTW acknowledgement confirmed=%t: %#v", confirmed, actions)
		return api.RequiredAction{}
	}

	settle("check-sibling-duplicates")
	pending := action(true)
	if pending.WorkflowRevision >= current.Workflow.Revision {
		t.Fatalf("fixture did not retain an earlier action: action=%d workflow=%d", pending.WorkflowRevision, current.Workflow.Revision)
	}
	if current.Dupes == nil || !slices.ContainsFunc(current.Dupes.Results, func(result api.TrackerDupeAssessment) bool {
		return result.TrackerID == "ALPHA" && result.Decision == api.DupeDecisionNoMatch
	}) || len(dupes.projections) != 1 || dupes.projections[0].TrackerID != "ALPHA" {
		t.Fatalf("eligible sibling did not finish its duplicate check: dupes=%#v checked=%#v", current.Dupes, dupes.projections)
	}
	if !slices.ContainsFunc(projection().PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == "genre" && decision.Disposition == api.RuleDispositionWaivable && decision.Blocking
	}) {
		t.Fatalf("OTW genre warning missing: %#v", projection())
	}
	intent = api.WorkflowIntent{Interaction: api.InteractionModeInteractive}
	stale := request("stale-acknowledgement", api.RequiredActionAnswer{
		ActionID:         pending.ID,
		WorkflowRevision: current.Workflow.Revision,
		Confirmed:        new(true),
	})
	stale.Authority.ExpectedRevision = pending.WorkflowRevision
	if _, err := core.ContinueReleaseWorkflow(ctx, owner, stale); !errors.Is(err, releaseworkflow.ErrRevisionConflict) {
		t.Fatalf("stale client revision error = %v", err)
	}

	staleAnswer := request("stale-answer-revision", api.RequiredActionAnswer{
		ActionID:         pending.ID,
		WorkflowRevision: pending.WorkflowRevision,
		Confirmed:        new(true),
	})
	if _, err := core.ContinueReleaseWorkflow(ctx, owner, staleAnswer); !errors.Is(err, releaseworkflow.ErrRevisionConflict) {
		t.Fatalf("stale answer revision error = %v", err)
	}

	for _, confirmed := range []bool{true, false, true} {
		selectedAction := action(confirmed)
		key := "acknowledge-" + string(selectedAction.ID)
		settle(key, api.RequiredActionAnswer{
			ActionID:         selectedAction.ID,
			WorkflowRevision: current.Workflow.Revision,
			Confirmed:        &confirmed,
		})
		otwProjection := projection()
		if (otwProjection.RuleAuthorizationFingerprint != "") != confirmed || otwProjection.DupeReady != confirmed {
			t.Fatalf("OTW acknowledgement confirmed=%t: %#v", confirmed, otwProjection)
		}
		if confirmed && otwProjection.RuleAuthorizationFingerprint != otwProjection.WaivableRuleFingerprint {
			t.Fatalf("OTW acknowledgement lost exact warning authority: %#v", otwProjection)
		}
		if current.Dupes == nil || !slices.ContainsFunc(current.Dupes.Results, func(result api.TrackerDupeAssessment) bool {
			return result.TrackerID == "OTW" && ((confirmed && result.Decision == api.DupeDecisionNoMatch) ||
				(!confirmed && result.Decision == api.DupeDecisionSkipped))
		}) {
			t.Fatalf("OTW duplicate state after acknowledgement confirmed=%t: %#v", confirmed, current.Dupes)
		}
		if !confirmed {
			settle("obsolete-grant-after-revocation", api.RequiredActionAnswer{
				ActionID:         pending.ID,
				WorkflowRevision: current.Workflow.Revision,
				Confirmed:        new(true),
			})
			if projection().RuleAuthorizationFingerprint != "" {
				t.Fatal("obsolete action restored revoked rule authority")
			}
		}
	}

	previousGeneration := current.Release.Release.Generation
	obsoleteAction := action(false)
	intent.Preparation = &api.PrepareInput{SourcePath: current.Release.Release.Source.SourcePath, Force: true}
	intent.TrackerIDs = []api.TrackerID{"OTW", "ALPHA"}
	settle("new-prepared-generation")
	if current.Release.Release.Generation <= previousGeneration || projection().RuleAuthorizationFingerprint != "" {
		t.Fatalf("new preparation retained old authority: generation=%d projection=%#v", current.Release.Release.Generation, projection())
	}
	settle("obsolete-grant-after-repreparation", api.RequiredActionAnswer{
		ActionID:         obsoleteAction.ID,
		WorkflowRevision: current.Workflow.Revision,
		Confirmed:        new(true),
	})
	if projection().RuleAuthorizationFingerprint != "" || action(true).ID == obsoleteAction.ID {
		t.Fatalf("obsolete generation action authorized current warnings: %#v", projection())
	}
}
