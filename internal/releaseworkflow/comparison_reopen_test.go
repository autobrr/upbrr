// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonExplicitReopenAfterShutdownRetainsOnlyPendingComparisons(t *testing.T) {
	repo := openActiveInputRecoveryRepository(t.Context(), t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	private := NewMemoryPrivateResourceStore()
	builder := &comparisonMediaFake{}
	preparer := testPreparer()
	prepare := preparer.PrepareFunc
	var generation api.PreparedGeneration
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		result, err := prepare(ctx, input)
		if err != nil {
			return result, err
		}
		generation++
		result.Release.Generation = generation
		return persistActiveInputTestRelease(ctx, repo, result)
	}
	version := "unchanged-source"
	projector := trackerProjectionBuilderFunc(func(_ context.Context, _ api.ReleaseSnapshot, _ api.UploadSubject,
		trackers []api.TrackerID, _ map[api.TrackerID]api.TrackerProjectionInstructions,
		_ map[api.TrackerID]api.WorkflowFingerprint, _ api.WorkflowExecutionMode,
	) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: trackers}, testProjectionSet(t), nil
	})
	newModule := func(epoch string) *Module {
		t.Helper()
		module, err := New(persistent, private, preparer, WithProcessEpoch(epoch), WithTrackerProjectionBuilder(projector),
			WithTrackerPreflightBuilder(readyPreflightBuilder(t)), WithDupeAssessmentBuilder(readyDupeBuilder(t)),
			WithMediaArtifactBuilder(builder), WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
				manifest, err := json.Marshal(api.VerifiedInputSource{
					Manifest: api.SourceManifest{SourcePath: input.SourcePath}, Identity: api.SourceContentIdentity{Digest: version},
				})
				if err != nil {
					return api.InputRecord{}, fmt.Errorf("encode comparison input verification: %w", err)
				}
				return api.InputRecord{
					CanonicalPath: input.SourcePath,
					SourceVersion: version,
					Manifest:      manifest,
				}, nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
		return module
	}
	prepareAndCheck := func(module *Module, workflow api.WorkflowID, input api.PrepareInput) CommandResult {
		t.Helper()
		current, err := module.Current(t.Context(), testOwnerID, workflow)
		if err != nil {
			t.Fatal(err)
		}
		current = executeCommand(t, module, PrepareReleaseCommand{
			WorkflowID:       workflow,
			ExpectedRevision: current.Workflow.Revision,
			Input:            input,
		})
		current = executeCommand(t, module, ProjectTrackersCommand{
			WorkflowID:       workflow,
			ExpectedRevision: current.Workflow.Revision,
			TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
		})
		current = executeCommand(t, module, PreflightTrackersCommand{WorkflowID: workflow, ExpectedRevision: current.Workflow.Revision})
		return executeCommand(t, module, CheckDuplicatesCommand{WorkflowID: workflow, ExpectedRevision: current.Workflow.Revision})
	}
	first := newModule("comparison-first")
	input := api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "Example.Release.2026.mkv", "synthetic source")}
	opened, err := first.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "comparison-first"})
	if err != nil {
		t.Fatal(err)
	}
	current := prepareAndCheck(first, opened.WorkflowID, input)
	plan := reviewedComparisonFixture(t)
	plan.Sources[0].Label = "retained correction"
	plan.Rows = append(plan.Rows, api.ComparisonRow{ID: "excluded", Excluded: true})
	current = executeCommand(t, first, EditComparisonsCommand{
		WorkflowID:       opened.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     api.ComparisonInstructions{Action: "configure", Plan: plan},
	})
	current = executeCommand(t, first, EditComparisonsCommand{
		WorkflowID:       opened.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            current.Workflow.Media,
		Instructions:     api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: current.Media.Comparisons.Fingerprint},
	})
	previous := *current.Workflow.Media
	state, err := persistent.Load(t.Context(), testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.Media[previous.ID]
	snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
		ID:       "ordinary",
		Kind:     api.MediaArtifactScreenshot,
		Selected: true,
	})
	state.Media[previous.ID] = snapshot
	ctx, err := first.activeMutationContext(t.Context(), testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	expected := state.Workflow.Revision
	state.Workflow.Revision++
	if err := persistent.Save(ctx, testOwnerID, expected, state); err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := newModule("comparison-second")
	if err := second.ResetIdleInputOnStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	empty, err := second.ActiveInput(t.Context(), testOwnerID)
	if err != nil || empty.State != api.ActiveInputEmpty || builder.restores != 0 || builder.uploads != 0 {
		t.Fatalf("startup restored input or uploaded images without an explicit open: %v", err)
	}
	reopened, err := second.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		Input:            input,
		ExpectedRevision: empty.Revision,
		IdempotencyKey:   "comparison-reopen",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = persistent.Load(t.Context(), testOwnerID, reopened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.WorkflowID == opened.WorkflowID || state.Workflow.Media != nil || state.Workflow.Release != nil || state.ComparisonMedia == nil {
		t.Fatal("explicit reopen lost pending comparisons or activated old workflow authority")
	}
	pending := state.Media[state.ComparisonMedia.ID]
	if pending.WorkflowID != reopened.WorkflowID || pending.ID == previous.ID || pending.Comparisons.ApprovedFingerprint != "" ||
		len(pending.Artifacts) != 2 || pending.TrackerApproval != nil || pending.ImageRequirementsPrepared {
		t.Fatal("pending adoption retained ordinary media, old identity or review authority")
	}
	private.Delete(testOwnerID, opened.WorkflowID, mediaPrivateResourceID(previous.ID))
	current = prepareAndCheck(second, reopened.WorkflowID, input)
	current = executeCommand(t, second, RestoreComparisonsCommand{WorkflowID: reopened.WorkflowID, ExpectedRevision: current.Workflow.Revision})
	if builder.restores != 1 || builder.uploads != 0 || current.Media == nil || current.Media.Comparisons == nil {
		t.Fatal("fresh generation could not independently restore pending comparisons")
	}
	restored := current.Media.Comparisons
	if restored.Sources[0].Label != "retained correction" || len(restored.Rows) != 2 || !restored.Rows[1].Excluded ||
		restored.Rows[0].Cells[0].ArtifactID != "source-image" || restored.Rows[0].Cells[0].ContentSHA256 != plan.Rows[0].Cells[0].ContentSHA256 ||
		ComparisonReviewSatisfied(restored) {
		t.Fatal("reopen lost edited rows, pixels or the fresh review requirement")
	}
	if _, err := second.execute(t.Context(), testOwnerID, UploadMediaImagesCommand{
		WorkflowID:       reopened.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            *current.Workflow.Media,
		Host:             "synthetic",
		IdempotencyKey:   "unreviewed-reopen",
	}); !errors.Is(err, ErrInvalidTransition) || builder.uploads != 0 {
		t.Fatalf("reopen bypassed fresh comparison review: %v", err)
	}
	if err := second.ReleaseInput(t.Context(), testOwnerID, reopened.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err = second.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	version = "changed-source"
	changed, err := second.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		Input:            input,
		ExpectedRevision: empty.Revision,
		IdempotencyKey:   "comparison-changed-source",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = persistent.Load(t.Context(), testOwnerID, changed.WorkflowID)
	if err != nil || state.ComparisonMedia != nil {
		t.Fatalf("changed source adopted prior comparisons: %v", err)
	}
}
