// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type duplicateReuseBuilderFake struct {
	build  dupeAssessmentBuilderFunc
	full   int
	reuses []DuplicateAssessmentReuse
	err    error
}

func (b *duplicateReuseBuilderFake) Build(ctx context.Context, subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, now time.Time, skip bool,
) (api.DupeAssessment, any, error) {
	b.full++
	return b.build(ctx, subject, projections, preflight, now, skip)
}

func (b *duplicateReuseBuilderFake) BuildWithReuse(ctx context.Context, subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, now time.Time, skip bool,
	reuse DuplicateAssessmentReuse,
) (api.DupeAssessment, any, error) {
	b.reuses = append(b.reuses, reuse)
	if b.err != nil {
		return api.DupeAssessment{}, nil, b.err
	}
	return b.build(ctx, subject, projections, preflight, now, skip)
}

func newDuplicateReuseWorkflow(t *testing.T) (*Module, *MemoryRepository, *duplicateReuseBuilderFake, CommandResult) {
	t.Helper()
	projector := trackerProjectionBuilderFunc(func(_ context.Context, _ api.ReleaseSnapshot, _ api.UploadSubject,
		trackerIDs []api.TrackerID, _ map[api.TrackerID]api.TrackerProjectionInstructions,
		authorizations map[api.TrackerID]api.WorkflowFingerprint, mode api.WorkflowExecutionMode,
	) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		snapshot := testProjectionSet(t)
		snapshot.ExecutionMode = mode
		for index := range snapshot.Projections {
			projection := &snapshot.Projections[index]
			projection.WaivableRuleFingerprint = testFingerprint(t, string(projection.TrackerID)+"-rules")
			projection.PolicyDecisions = []api.TrackerPolicyDecision{{
				Code:        "language_rule",
				Disposition: api.RuleDispositionWaivable,
				Decision:    "authorization_required",
				Blocking:    true,
			}}
			status := api.RequiredActionStatusPending
			if authorizations[projection.TrackerID] == projection.WaivableRuleFingerprint {
				projection.RuleAuthorizationFingerprint = projection.WaivableRuleFingerprint
				projection.PolicyDecisions[0].Decision = "authorized"
				projection.PolicyDecisions[0].Blocking = false
				status = api.RequiredActionStatusResolved
			} else {
				projection.DupeReady = false
				projection.UploadReady = false
				projection.Readiness = api.ReadinessStatusBlocked
			}
			projection.RequiredActions = []api.RequiredAction{{
				Kind:   api.RequiredActionAuthorizeRules,
				Status: status,
				Prompt: "Acknowledge tracker warnings?",
			}}
			snapshot.RequiredActions = append(snapshot.RequiredActions, projection.RequiredActions...)
		}
		snapshot.Status = finalizedProjectionStatus(snapshot.Projections, snapshot.RequiredActions, nil)
		return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: trackerIDs}, snapshot, nil
	})
	builder := &duplicateReuseBuilderFake{build: readyDupeBuilder(t)}
	module, repository := newTestModule(t, testPreparer(), WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(readyPreflightBuilder(t)), WithDupeAssessmentBuilder(builder))
	result := executeCommand(t, module, CreateWorkflowCommand{})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.mkv")},
	})
	result = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
	})
	for _, trackerID := range []api.TrackerID{"ALPHA", "BETA"} {
		result = resolveDuplicateReuseRule(t, module, result, trackerID, true)
	}
	result = executeCommand(t, module, PreflightTrackersCommand{WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision})
	result = executeCommand(t, module, CheckDuplicatesCommand{WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision})
	return module, repository, builder, result
}

func resolveDuplicateReuseRule(t *testing.T, module *Module, result CommandResult, trackerID api.TrackerID, confirmed bool) CommandResult {
	t.Helper()
	state, err := module.repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, projection := range state.Projections[state.Workflow.TrackerProjections.ID].Projections {
		if projection.TrackerID == trackerID {
			return executeCommand(t, module, ResolveActionCommand{
				WorkflowID:       result.Workflow.ID,
				ExpectedRevision: result.Workflow.Revision,
				Answer: api.RequiredActionAnswer{
					ActionID:         projection.RequiredActions[0].ID,
					WorkflowRevision: result.Workflow.Revision,
					Confirmed:        &confirmed,
				},
			})
		}
	}
	t.Fatalf("missing tracker %s", trackerID)
	return CommandResult{}
}

func TestDuplicateReuseUnionsRuleChangesUntilSuccessfulPass(t *testing.T) {
	t.Parallel()
	module, repository, builder, result := newDuplicateReuseWorkflow(t)
	baseline := *result.Workflow.Dupes
	for _, confirmed := range []bool{false, true} {
		for _, trackerID := range []api.TrackerID{"ALPHA", "BETA"} {
			result = resolveDuplicateReuseRule(t, module, result, trackerID, confirmed)
		}
	}
	result = executeCommand(t, module, PreflightTrackersCommand{WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision})
	state, err := repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.PendingDuplicateReuse == nil || state.PendingDuplicateReuse.Assessment != baseline ||
		!slices.Equal(state.PendingDuplicateReuse.InvalidatedTrackers, []api.TrackerID{"ALPHA", "BETA"}) || state.Workflow.Dupes != nil {
		t.Fatalf("rule changes lost baseline or retained authority: pending=%#v dupes=%#v", state.PendingDuplicateReuse, state.Workflow.Dupes)
	}
	if builder.full != 1 || len(builder.reuses) != 0 {
		t.Fatalf("rule changes repeated duplicate search: full=%d reuse=%d", builder.full, len(builder.reuses))
	}
	command := CheckDuplicatesCommand{WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision}
	for _, failure := range []string{"build", "save"} {
		if failure == "build" {
			builder.err = errors.New("synthetic duplicate build failure")
		} else {
			builder.err = nil
			module.repository = &failOnceSaveRepository{Repository: repository}
		}
		if _, err := module.execute(t.Context(), testOwnerID, command); err == nil {
			t.Fatalf("expected %s failure", failure)
		}
		state, err = repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
		if err != nil || state.PendingDuplicateReuse == nil || state.PendingDuplicateReuse.Assessment != baseline || state.Workflow.Dupes != nil {
			t.Fatalf("%s failure lost pending reuse: %#v, %v", failure, state.PendingDuplicateReuse, err)
		}
		if _, err := module.private.Get(testOwnerID, result.Workflow.ID, dupePrivateResourceID(baseline.ID), module.clock.Now()); err != nil {
			t.Fatalf("%s failure discarded baseline evidence: %v", failure, err)
		}
	}
	result = executeCommand(t, module, command)
	if builder.full != 1 || len(builder.reuses) != 3 {
		t.Fatalf("duplicate builder calls: full=%d reuse=%d", builder.full, len(builder.reuses))
	}
	for _, reuse := range builder.reuses {
		if reuse.Assessment.ID != baseline.ID || reuse.Assessment.Revision != baseline.Revision || reuse.PrivateEvidence == nil ||
			!slices.Equal(reuse.InvalidatedTrackers, []api.TrackerID{"ALPHA", "BETA"}) {
			t.Fatalf("builder received incorrect reuse: %#v", reuse)
		}
	}
	state, err = repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
	if err != nil || state.PendingDuplicateReuse != nil || result.Workflow.Dupes == nil || *result.Workflow.Dupes == baseline {
		t.Fatalf("successful pass did not replace pending authority: %#v, %v", state.PendingDuplicateReuse, err)
	}
	if _, err := module.private.Get(testOwnerID, result.Workflow.ID, dupePrivateResourceID(baseline.ID), module.clock.Now()); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("successful pass retained baseline private evidence: %v", err)
	}
	if _, err := module.private.Get(testOwnerID, result.Workflow.ID, dupePrivateResourceID(result.Dupes.ID), module.clock.Now()); err != nil {
		t.Fatalf("successful pass lost current evidence: %v", err)
	}
}

func TestDuplicateReuseFallsBackWhenEvidenceCannotBeReused(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing evidence", "expired evidence", "extra pass"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			module, _, builder, result := newDuplicateReuseWorkflow(t)
			baseline := *result.Workflow.Dupes
			result = resolveDuplicateReuseRule(t, module, result, "ALPHA", false)
			result = resolveDuplicateReuseRule(t, module, result, "ALPHA", true)
			result = executeCommand(t, module, PreflightTrackersCommand{WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision})
			command := CheckDuplicatesCommand{WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision}
			switch scenario {
			case "missing evidence":
				module.private.Delete(testOwnerID, result.Workflow.ID, dupePrivateResourceID(baseline.ID))
			case "expired evidence":
				if err := module.private.Put(testOwnerID, result.Workflow.ID, dupePrivateResourceID(baseline.ID), "expired", module.clock.Now()); err != nil {
					t.Fatal(err)
				}
			case "extra pass":
				command.CheckOrdinal = 2
			}
			result = executeCommand(t, module, command)
			if builder.full != 2 || len(builder.reuses) != 0 || result.Dupes.CheckOrdinal != normalizedDuplicateCheckOrdinal(command.CheckOrdinal) {
				t.Fatalf("fallback did not run fresh pass: full=%d reuse=%d ordinal=%d", builder.full, len(builder.reuses), result.Dupes.CheckOrdinal)
			}
		})
	}
}

func TestDuplicateReuseInvalidatedByNewPreparationOrProjection(t *testing.T) {
	t.Parallel()
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "projection", true: "preparation"}[prepare], func(t *testing.T) {
			t.Parallel()
			module, repository, _, result := newDuplicateReuseWorkflow(t)
			baseline := *result.Workflow.Dupes
			result = resolveDuplicateReuseRule(t, module, result, "ALPHA", false)
			if prepare {
				result = executeCommand(t, module, PrepareReleaseCommand{
					WorkflowID:       result.Workflow.ID,
					ExpectedRevision: result.Workflow.Revision,
					Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Changed.Release.mkv"), Force: true},
				})
			} else {
				result = executeCommand(t, module, ProjectTrackersCommand{
					WorkflowID:       result.Workflow.ID,
					ExpectedRevision: result.Workflow.Revision,
					TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
				})
			}
			state, err := repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
			if err != nil || state.PendingDuplicateReuse != nil {
				t.Fatalf("new inputs retained pending duplicate reuse: %#v, %v", state.PendingDuplicateReuse, err)
			}
			if _, err := module.private.Get(testOwnerID, result.Workflow.ID, dupePrivateResourceID(baseline.ID), module.clock.Now()); !errors.Is(err, ErrPrivateResourceUnavailable) {
				t.Fatalf("new inputs retained obsolete evidence: %v", err)
			}
		})
	}
}

func TestDuplicateReuseInvalidatedByConfigImpact(t *testing.T) {
	t.Parallel()
	for _, kind := range []api.ConfigImpact{api.ConfigImpactProvider, api.ConfigImpactTrackers} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			module, repository, _, result := newDuplicateReuseWorkflow(t)
			result = resolveDuplicateReuseRule(t, module, result, "ALPHA", false)
			state, err := repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			record, err := workflowStateRecord(testOwnerID, state)
			if err != nil {
				t.Fatal(err)
			}
			record, err = ApplyConfigImpact(record, api.ConfigImpactDetail{Kind: kind, TrackerIDs: []api.TrackerID{"ALPHA"}})
			if err != nil {
				t.Fatal(err)
			}
			state, err = decodeWorkflowState(record)
			if err != nil || state.PendingDuplicateReuse != nil {
				t.Fatalf("config impact retained pending reuse: %#v, %v", state.PendingDuplicateReuse, err)
			}
		})
	}
}

func TestDuplicateReuseRejectsRuleActionWithPendingStrictEvidence(t *testing.T) {
	t.Parallel()
	for _, confirmed := range []bool{false, true} {
		module, repository, _, result := newDuplicateReuseWorkflow(t)
		baseline := *result.Workflow.Dupes
		result = resolveDuplicateReuseRule(t, module, result, "ALPHA", false)
		state, err := repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		dupes := state.Dupes[baseline.ID]
		dupes.Results[1].Decision = api.DupeDecisionAccepted
		dupes.Results[1].Matches = []api.DupeMatchProjection{{Reason: "in_client"}}
		state.Dupes[baseline.ID] = dupes
		projections := state.Projections[state.Workflow.TrackerProjections.ID]
		projection := &projections.Projections[1]
		action := projection.RequiredActions[0]
		if confirmed {
			projection.RuleAuthorizationFingerprint = ""
			projection.RequiredActions[0].Status = api.RequiredActionStatusPending
			state.Projections[projections.ID] = projections
			for index := range state.Workflow.RequiredActions {
				if state.Workflow.RequiredActions[index].ID == action.ID {
					state.Workflow.RequiredActions[index].Status = api.RequiredActionStatusPending
				}
			}
		}
		_, err = module.resolveAction(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, module.clock.Now(), ResolveActionCommand{
			ExpectedRevision: state.Workflow.Revision,
			Answer: api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: state.Workflow.Revision,
				Confirmed:        &confirmed,
			},
		})
		if !errors.Is(err, ErrInvalidTransition) || state.PendingDuplicateReuse == nil {
			t.Fatalf("pending strict evidence accepted rule change confirmed=%t: %v", confirmed, err)
		}
	}
}
