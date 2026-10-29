// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestModifiedReleaseDecisionIsSharedAndGenerationBound(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(strconv.FormatBool(confirmed), func(t *testing.T) {
			preparer := testPreparer()
			prepare := preparer.PrepareFunc
			var generation api.PreparedGeneration
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				result, err := prepare(ctx, input)
				generation++
				result.Release.Generation = generation
				return result, err
			}
			projector := trackerProjectionBuilderFunc(func(_ context.Context, release api.ReleaseSnapshot, subject api.UploadSubject, ids []api.TrackerID,
				_ map[api.TrackerID]api.TrackerProjectionInstructions, authorizations map[api.TrackerID]api.WorkflowFingerprint, mode api.WorkflowExecutionMode,
			) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
				set := api.TrackerReleaseProjectionSet{
					InputFingerprint:  testFingerprint(t, fmt.Sprint(release.Release.Generation)),
					PolicyFingerprint: testFingerprint(t, "modified-policy"),
					ExecutionMode:     mode,
				}
				for _, id := range ids {
					projection := testProjection(t, id, "Example.Release.2026-GRP")
					projection.ModifiedReleaseDecision = subject.ModifiedReleaseDecision
					failures := []api.RuleFailure{{
						Rule:        "modified_release",
						Reason:      "Verify source provenance",
						Disposition: api.RuleDispositionWaivable,
					}}
					if err := trackers.ApplyProjectionRuleFailures(&projection, failures, mode, authorizations[id], nil); err != nil {
						return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, set, fmt.Errorf("apply projection rule failures: %w", err)
					}
					set.Projections = append(set.Projections, projection)
					set.RequiredActions = append(set.RequiredActions, projection.RequiredActions...)
					set.Failures = append(set.Failures, projection.Failures...)
				}
				set.Status = finalizedProjectionStatus(set.Projections, set.RequiredActions, set.Failures)
				return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: slices.Clone(ids)}, set, nil
			})
			module, repository := newTestModule(t, preparer, WithTrackerProjectionBuilder(projector))
			current := executeCommand(t, module, CreateWorkflowCommand{})
			prepareRelease := func() {
				current = executeCommand(t, module, PrepareReleaseCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					Input:            api.PrepareInput{SourcePath: "Example.Release.2026-GRP", Force: true},
				})
			}
			project := func() {
				current = executeCommand(t, module, ProjectTrackersCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
				})
			}
			prepareRelease()
			project()
			if len(current.Workflow.RequiredActions) != 1 || !isModifiedReleaseAction(current.Workflow.RequiredActions[0]) {
				t.Fatalf("expected one release confirmation, got %#v", current.Workflow.RequiredActions)
			}
			action := current.Workflow.RequiredActions[0]
			current = executeCommand(t, module, ResolveActionCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Answer: api.RequiredActionAnswer{
					ActionID:         action.ID,
					WorkflowRevision: current.Workflow.Revision,
					Confirmed:        &confirmed,
				},
			})
			for _, projection := range current.Projections.Projections {
				if projection.ModifiedReleaseDecision == nil || *projection.ModifiedReleaseDecision != confirmed {
					t.Fatalf("decision not shared: %#v", projection)
				}
				if confirmed && !projection.DupeReady {
					t.Fatal("shared yes did not unblock tracker")
				}
				if !confirmed && projection.Readiness != api.ReadinessStatusIneligible {
					t.Fatal("shared no did not reject tracker")
				}
			}
			stored, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.ModifiedReleaseDecision == nil || stored.ModifiedReleaseDecision.Confirmed != confirmed {
				t.Fatal("decision was not persisted")
			}
			project()
			if len(current.Workflow.RequiredActions) != 0 {
				t.Fatal("same generation asked again")
			}
			prepareRelease()
			project()
			if len(current.Workflow.RequiredActions) != 1 || current.Workflow.RequiredActions[0].ID == action.ID {
				t.Fatal("new generation reused prior confirmation")
			}
			for _, projection := range current.Projections.Projections {
				if projection.ModifiedReleaseDecision != nil {
					t.Fatal("new generation inherited decision")
				}
			}
		})
	}
}
