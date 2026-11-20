// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonImportAllowsPlannedOrdinaryCapture(t *testing.T) {
	action := api.RequiredAction{
		ID:     "capture-images",
		Kind:   api.RequiredActionProvideTrackerInput,
		Status: api.RequiredActionStatusPending,
	}
	current := CommandResult{
		Media:        &api.MediaArtifactSet{RequiredActions: []api.RequiredAction{action}},
		Projections:  &api.TrackerReleaseProjectionSet{},
		Continuation: api.WorkflowContinuation{RequiredActions: []api.RequiredAction{action}},
	}
	session := &compositeUploadSession{}
	if compositeUploadPendingAction(current, session) == nil {
		t.Fatal("missing media intent bypassed required input")
	}
	session.Intent.Media = &api.MediaCaptureInstructions{ScreenshotCount: 2}
	if compositeUploadPendingAction(current, session) != nil {
		t.Fatal("imported media blocked the planned ordinary screenshot capture")
	}
	current.Media.Artifacts = []api.MediaArtifact{
		{Kind: api.MediaArtifactScreenshot, Selected: true},
		{Kind: api.MediaArtifactScreenshot, Selected: true},
	}
	if compositeUploadPendingAction(current, session) == nil {
		t.Fatal("completed capture bypassed the remaining media readiness block")
	}
}

func TestCompositeComparisonImportRequiresBoundReviewBeforeHosting(t *testing.T) {
	module, _, uploads := newCompositeUploadTestModule(t)
	builder := &comparisonMediaFake{host: "synthetic"}
	module.mediaBuilder = builder
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var images []StagedMediaContent
	for row := 1; row <= 2; row++ {
		for column, label := range []string{"source", "encode", "filtered"} {
			images = append(images, StagedMediaContent{
				Name:        fmt.Sprintf("%02d-%02d-%s.png", row, column+1, label),
				ContentType: "image/png",
				Bytes:       encoded.Bytes(),
			})
		}
	}
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "comparison-import")
	index := 2
	request.Media.Screenshots = api.ReleaseWorkflowUploadScreenshots{
		ComparisonPaths:        []string{"prepared-comparisons"},
		ComparisonPrimaryIndex: &index,
		ComparisonReuseMain:    true,
	}
	ctx := WithCompositeUploadMediaInputs(t.Context(), CompositeUploadMediaInputs{Comparisons: images})
	started, err := module.StartUpload(ctx, testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	current = approveCompositeUploadTrackers(t, module, current, []api.TrackerID{"ALPHA"}, "approve-comparison-trackers")
	if current.Media == nil || current.Media.Comparisons == nil {
		t.Fatalf("comparison images were flattened or lost: operation=%+v", current.Operation)
	}
	plan := current.Media.Comparisons
	if len(plan.Sources) != 3 || len(plan.Rows) != 2 || plan.MainSourceID != "2" || !plan.ReuseMainScreenshots ||
		ComparisonMediaReviewSatisfied(*current.Media) ||
		builder.uploads != 0 ||
		uploads.execution != nil {
		t.Fatalf("unreviewed import reached hosting: plan=%+v host calls=%d", plan, builder.uploads)
	}
	var action api.RequiredAction
	for _, candidate := range current.Continuation.RequiredActions {
		if candidate.Kind == api.RequiredActionReviewComparisons {
			action = candidate
		}
	}
	if action.ID == "" {
		t.Fatalf("missing explicit comparison review action: %+v", current.Continuation)
	}
	feedback := api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: current.Workflow.Revision},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackComparisonApproval,
			ComparisonApproval: &api.ReleaseWorkflowUploadComparisonApproval{Confirmed: true, ExpectedFingerprint: "stale"}},
		IdempotencyKey: "comparison-approval",
	}
	if _, err := module.SubmitUploadFeedback(
		t.Context(),
		testOwnerID,
		current.Workflow.ID,
		feedback,
	); !errors.Is(err, ErrInvalidTransition) ||
		builder.uploads != 0 {
		t.Fatalf("stale image approval reached host: %v calls=%d", err, builder.uploads)
	}
	feedback.Response.ComparisonApproval.ExpectedFingerprint = plan.Fingerprint
	feedback.IdempotencyKey = "comparison-current-approval"
	resumed, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, current.Workflow.ID, feedback)
	if err != nil {
		t.Fatal(err)
	}
	current = waitCompositeUploadTestOperation(t, module, resumed)
	if current.Media == nil || !ComparisonMediaReviewSatisfied(*current.Media) || builder.uploads != 1 {
		t.Fatalf("reviewed comparison did not continue: media=%+v host calls=%d", current.Media, builder.uploads)
	}
}
