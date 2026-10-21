// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMetadataDuplicateReuseStagesOnlyFreshAssessment(t *testing.T) {
	for _, scenario := range []string{"current", "expired", "missing assessment", "wrong revision"} {
		t.Run(scenario, func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			baseline := *state.Workflow.Dupes
			prior := state.Dupes[baseline.ID]
			switch scenario {
			case "expired":
				prior.ExpiresAt = module.clock.Now()
				state.Dupes[baseline.ID] = prior
			case "missing assessment":
				delete(state.Dupes, baseline.ID)
			case "wrong revision":
				prior.Revision++
				state.Dupes[baseline.ID] = prior
			}
			stageMetadataDuplicateReuse(&state, module.clock.Now())
			pending := state.PendingDuplicateReuse
			if (pending != nil) != (scenario == "current") {
				t.Fatalf("pending=%#v for %s", pending, scenario)
			}
			if pending != nil && (!pending.MetadataRefresh || pending.Assessment != baseline || pending.EquivalentMetadataTo != nil) {
				t.Fatalf("staging supplied certified authority prematurely: %#v", pending)
			}
		})
	}
}

func TestMetadataDuplicateReuseRequiresExactCertifiedGeneration(t *testing.T) {
	for _, scenario := range []string{"certified", "no certificate", "wrong target", "expired", "missing evidence", "changed execution", "extra pass"} {
		t.Run(scenario, func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			prior := state.Dupes[state.Workflow.Dupes.ID]
			projections := state.Projections[prior.ProjectionSet.ID]
			projections.ReleaseRef.Generation++
			target := projections.ReleaseRef
			state.PendingDuplicateReuse = &PendingDuplicateReuse{
				Assessment:           *state.Workflow.Dupes,
				MetadataRefresh:      true,
				EquivalentMetadataTo: &target,
			}
			state.Workflow.Dupes = nil
			command := CheckDuplicatesCommand{CheckOrdinal: prior.CheckOrdinal}
			switch scenario {
			case "no certificate":
				state.PendingDuplicateReuse.EquivalentMetadataTo = nil
			case "wrong target":
				wrong := target
				wrong.Generation++
				state.PendingDuplicateReuse.EquivalentMetadataTo = &wrong
			case "expired":
				prior.ExpiresAt = module.clock.Now()
				state.Dupes[prior.ID] = prior
			case "missing evidence":
				module.private.Delete(testOwnerID, state.Workflow.ID, dupePrivateResourceID(prior.ID))
			case "changed execution":
				projections.ExecutionMode = api.WorkflowExecutionModeDebug
			case "extra pass":
				command.CheckOrdinal = normalizedDuplicateCheckOrdinal(prior.CheckOrdinal) + 1
			}
			reuse, err := module.pendingDuplicateReuse(testOwnerID, &state, projections, command, module.clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if (reuse != nil) != (scenario == "certified") {
				t.Fatalf("reuse=%#v for %s", reuse, scenario)
			}
			if reuse != nil && (reuse.EquivalentMetadataTo == nil || *reuse.EquivalentMetadataTo != target || reuse.PrivateEvidence == nil) {
				t.Fatalf("certified reuse lost exact target or private evidence: %#v", reuse)
			}
			if state.Workflow.Dupes != nil {
				t.Fatal("candidate reuse became current duplicate authority")
			}
		})
	}
}

func TestMetadataDuplicateReusePreparationCertificateAndProjection(t *testing.T) {
	for _, scenario := range []string{"exact certificate", "missing certificate", "wrong predecessor", "forced preparation", "provider refresh"} {
		t.Run(scenario, func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			baseline := *state.Workflow.Dupes
			prior := state.Dupes[baseline.ID].ReleaseRef
			exclusion := api.SubmissionExclusion{TrackerID: "GAMMA", Reason: "already_uploaded"}
			state.Workflow.SubmissionExclusions = []api.SubmissionExclusion{exclusion}
			expected := state.Workflow.Revision
			state.Workflow.Revision++
			if err := repository.Save(t.Context(), testOwnerID, expected, state); err != nil {
				t.Fatal(err)
			}
			preparer := testPreparer()
			prepare := preparer.PrepareFunc
			preparations := 0
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				preparations++
				result, err := prepare(ctx, input)
				result.Release.Generation = prior.Generation + 1
				if scenario != "missing certificate" {
					certificate := prior
					if scenario == "wrong predecessor" {
						certificate.Generation++
					}
					result.EquivalentPreviousRelease = &certificate
				}
				return result, err
			}
			module.preparer = preparer
			input := api.PrepareInput{SourcePath: prior.SourcePath}
			if scenario == "forced preparation" {
				input.Force = true
			}
			if scenario == "provider refresh" {
				input.ExternalFreshness = api.ExternalFreshnessRefresh
			}
			command := PrepareReleaseCommand{

				WorkflowID:       state.Workflow.ID,
				ExpectedRevision: state.Workflow.Revision,

				Input:          input,
				IdempotencyKey: "metadata-refresh",
			}
			current = executeCommand(t, module, command)
			replayed := executeCommand(t, module, command)
			if preparations != 1 || replayed.Workflow.Revision != current.Workflow.Revision {
				t.Fatal("preparation replay repeated work or changed committed authority")
			}
			state, err = repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Workflow.SubmissionExclusions) != 1 || state.Workflow.SubmissionExclusions[0] != exclusion {
				t.Fatal("metadata preparation lost the confirmed successful submission exclusion")
			}
			pending := state.PendingDuplicateReuse
			if (pending != nil) != (scenario == "exact certificate") {
				t.Fatalf("pending=%#v for %s", pending, scenario)
			}
			if state.Workflow.Dupes != nil {
				t.Fatal("preparation restored duplicate authority without fresh projection/preflight")
			}
			if pending == nil {
				return
			}
			release, err := currentReleaseSnapshot(&state)
			if err != nil {
				t.Fatal(err)
			}
			target := api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
			if pending.Assessment != baseline || pending.EquivalentMetadataTo == nil || *pending.EquivalentMetadataTo != target {
				t.Fatalf("certificate was not bound to exact new release: %#v", pending)
			}
			current = executeCommand(t, module, ProjectTrackersCommand{

				WorkflowID:       state.Workflow.ID,
				ExpectedRevision: state.Workflow.Revision,

				TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
			})
			state, err = repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Workflow.SubmissionExclusions) != 1 || state.Workflow.SubmissionExclusions[0] != exclusion {
				t.Fatal("fresh projection lost the confirmed successful submission exclusion")
			}
			if state.PendingDuplicateReuse == nil || state.PendingDuplicateReuse.Assessment != baseline || state.Workflow.Dupes != nil {
				t.Fatalf("fresh projection lost candidate or restored premature authority: %#v", state.PendingDuplicateReuse)
			}
		})
	}
}
