// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type skippedCaptureBuilder struct {
	mediaArtifactBuilderFunc
	add bool
}

func (b skippedCaptureBuilder) BuildIncremental(ctx context.Context, release api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
	instructions api.MediaCaptureInstructions, existing *api.MediaArtifactSet, private any, now time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, error) {
	if existing == nil {
		snapshot, _, err := b.Build(ctx, release, projections, instructions, now)
		return snapshot, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, err
	}
	snapshot, err := existing.Clone()
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("clone skipped capture media: %w", err)
	}
	if b.add {
		snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
			ID:       "extra",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		})
	}
	retained, _ := private.(RetainedMediaResource)
	return snapshot, retained, nil
}

func TestCaptureAfterSkippedHostingCanContinue(t *testing.T) {
	for _, add := range []bool{false, true} {
		name := "no-op"
		if add {
			name = "incremental addition"
		}
		t.Run(name, func(t *testing.T) {
			module, _, _ := newCompositeUploadTestModule(t)
			module.mediaBuilder = skippedCaptureBuilder{
				add: add,
				mediaArtifactBuilderFunc: func(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
					_ api.MediaCaptureInstructions, _ time.Time,
				) (api.MediaArtifactSet, any, error) {
					fingerprint, err := mediaRequirementsFingerprint(projections.Projections)
					return api.MediaArtifactSet{
						CaptureFingerprint:      testFingerprint(t, "skipped-capture"),
						RequirementsFingerprint: fingerprint,
						Status:                  api.StageStatusCompleted,
						Artifacts: []api.MediaArtifact{{
							ID:       "screen",
							Kind:     api.MediaArtifactScreenshot,
							Purpose:  api.ScreenshotPurposeFinal,
							Selected: true,
						}},
					}, nil, err
				},
			}
			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "skipped-capture")
			request.Trackers.Include = []api.TrackerID{"ALPHA"}
			request.ImageHosting.SkipUpload = new(true)
			started, err := module.StartUpload(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatal(err)
			}
			current := waitCompositeUploadTestOperation(t, module, started)
			current = approveCompositeUploadTrackers(t, module, current, []api.TrackerID{"ALPHA"}, "approve-skipped-capture")
			if current.Media == nil || !current.Media.ImageHostUploadSkipped {
				t.Fatalf("initial skip barrier missing: %+v", current.Media)
			}
			current = executeCommand(t, module, CaptureMediaCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Instructions:     api.MediaCaptureInstructions{ScreenshotCount: 1, Purpose: api.ScreenshotPurposeFinal},
			})
			if current.Media == nil || current.Media.Status != api.StageStatusCompleted ||
				current.Media.ImageHostUploadSkipped || current.Media.ImageRequirementsPrepared {
				t.Fatalf("capture left inconsistent preparation or blocked local media: %+v", current.Media)
			}
			current = executeCommand(t, module, UploadMediaImagesCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Media:            *current.Workflow.Media,
				SkipUpload:       true,
			})
			current = executeCommand(t, module, GenerateDescriptionsCommand{
				WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision,
			})
			if current.Descriptions == nil || current.Descriptions.Status != api.StageStatusCompleted {
				t.Fatalf("recapture could not continue to descriptions: %+v", current)
			}
		})
	}
}

func TestSkippedHostingKeepsExternalEffectReconciliationBlocked(t *testing.T) {
	t.Parallel()
	projections := []api.TrackerReleaseProjection{{TrackerID: "ALPHA", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}}}
	media := api.MediaArtifactSet{
		ImageRequirementsPrepared: true,
		ImageHostUploadSkipped:    true,
		Artifacts: []api.MediaArtifact{{
			ID:       "screen",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		}},
		Failures:        []api.WorkflowFailure{{TrackerID: "ALPHA", Failure: api.OperationFailure{Operation: api.OperationKindImageHosting, Code: api.OperationFailureUnknownOutcome}}},
		RequiredActions: []api.RequiredAction{{Kind: api.RequiredActionReconcileSubmission, Status: api.RequiredActionStatusPending}},
	}
	refreshMutatedMediaStatus(&media, projections, projections)
	if media.Status != api.StageStatusBlocked || len(media.RequiredActions) != 1 || len(media.Failures) != 1 {
		t.Fatalf("skip cleared unresolved external effect: %+v", media)
	}
}

func TestCaptureAfterSkippedHostingRequiresPendingReconciliation(t *testing.T) {
	module, repository, _ := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "capture-reconciliation")
	request.Trackers.Include = []api.TrackerID{"ALPHA"}
	request.ImageHosting.SkipUpload = new(true)
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	current = approveCompositeUploadTrackers(t, module, current, []api.TrackerID{"ALPHA"}, "approve-capture-reconciliation")
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	action := api.RequiredAction{
		ID:     "reconcile-images",
		Kind:   api.RequiredActionReconcileSubmission,
		Status: api.RequiredActionStatusPending,
	}
	state.Workflow.RequiredActions = []api.RequiredAction{action}
	media := state.Media[state.Workflow.Media.ID]
	media.RequiredActions = []api.RequiredAction{action}
	media.Failures = []api.WorkflowFailure{{Failure: api.OperationFailure{Operation: api.OperationKindImageHosting, Code: api.OperationFailureUnknownOutcome}}}
	state.Media[media.ID] = media
	for _, add := range []bool{false, true} {
		module.mediaBuilder = skippedCaptureBuilder{add: add}
		_, err := module.captureMedia(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, module.clock.Now(), CaptureMediaCommand{
			WorkflowID:       state.Workflow.ID,
			ExpectedRevision: state.Workflow.Revision,
			Instructions:     api.MediaCaptureInstructions{ScreenshotCount: 1, Purpose: api.ScreenshotPurposeFinal},
		})
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("capture addition=%t bypassed pending reconciliation: %v", add, err)
		}
		if len(state.Workflow.RequiredActions) != 1 || state.Workflow.RequiredActions[0].ID != action.ID ||
			len(state.Media[media.ID].Failures) != 1 {
			t.Fatal("capture removed pending reconciliation")
		}
	}
	// Tracker/client uncertainty can live only on the workflow, not its media.
	media.RequiredActions, media.Failures = nil, nil
	media.Comparisons = new(reviewedComparisonFixture(t))
	state.Media[media.ID] = media
	for _, action := range []string{"configure", "restore"} {
		working, err := cloneState(state)
		if err != nil {
			t.Fatal(err)
		}
		builder := &comparisonMediaFake{host: "synthetic"}
		module.mediaBuilder = builder
		instructions := api.ComparisonInstructions{Action: action}
		if action == "restore" {
			working.ComparisonMedia, working.Workflow.Media = working.Workflow.Media, nil
		} else {
			instructions.Plan = reviewedComparisonFixture(t)
		}
		if action == "restore" {
			_, err = module.restoreComparisons(t.Context(), testOwnerID, &working, working.Workflow.Revision+1, module.clock.Now())
		} else {
			_, err = module.editComparisons(t.Context(), testOwnerID, &working, working.Workflow.Revision+1, module.clock.Now(), EditComparisonsCommand{
				Media:        working.Workflow.Media,
				Instructions: instructions,
			})
		}
		if !errors.Is(err, ErrInvalidTransition) || builder.edits != 0 || builder.restores != 0 || len(working.Workflow.RequiredActions) != 1 {
			t.Fatalf("comparison %s bypassed workflow-only reconciliation: err=%v edits=%d restores=%d", action, err, builder.edits, builder.restores)
		}
	}
}
