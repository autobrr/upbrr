// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadPolicyOnlyDuplicateReview(t *testing.T) {
	for _, decision := range []api.DupeDecision{api.DupeDecisionIgnored, api.DupeDecisionAccepted} {
		t.Run(string(decision), func(t *testing.T) {
			module, repository, uploads := newCompositeUploadTestModule(t)
			base := module.dupeBuilder
			module.dupeBuilder = dupeAssessmentBuilderFunc(func(
				ctx context.Context, subject api.DuplicateSubject, projections api.TrackerReleaseProjectionSet,
				preflight api.TrackerPreflightAssessment, now time.Time, skip bool,
			) (api.DupeAssessment, any, error) {
				assessment, evidence, err := base.Build(ctx, subject, projections, preflight, now, skip)
				if err != nil {
					return assessment, evidence, fmt.Errorf("build policy-only assessment: %w", err)
				}
				result := &assessment.Results[0]
				result.Matches = nil
				result.Search.Complete = true
				result.ReviewReasons = []api.DupeReason{{Code: "sd_availability", Message: "Equivalent HD retail availability requires review."}}
				result.Decision = api.DupeDecisionPending
				result.Status = api.StageStatusBlocked
				result.RequiredActions = []api.RequiredAction{{
					Kind:      api.RequiredActionReviewDuplicates,
					Status:    api.RequiredActionStatusPending,
					TrackerID: result.TrackerID,
					Prompt:    "Acknowledge policy evidence.",
				}}
				return assessment, evidence, nil
			})
			request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "policy-only-review")
			request.Duplicates.OnEvidence = api.ReleaseWorkflowDuplicateAsk
			count := 1
			request.Duplicates.CheckCount = &count
			started, err := module.StartUpload(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatal(err)
			}
			current := waitCompositeUploadTestOperation(t, module, started)
			action := pendingCompositeFeedbackAction(t, current, api.RequiredActionReviewDuplicates, "ALPHA")
			if uploads.execution != nil {
				t.Fatal("upload proceeded before policy acknowledgement")
			}
			current = submitCompositeFeedbackForTest(t, module, current, action, api.ReleaseWorkflowUploadFeedbackResponse{
				Kind:            api.ReleaseWorkflowUploadFeedbackDuplicateReview,
				DuplicateReview: &api.ReleaseWorkflowUploadDuplicateReview{TrackerID: "ALPHA", Decision: decision},
			}, "policy-review-answer")
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Workflow.Dupes == nil {
				t.Fatal("reviewed duplicate assessment was not retained")
			}
			retained := state.Dupes[state.Workflow.Dupes.ID].Results[0]
			if len(retained.Matches) != 0 || len(retained.ReviewReasons) != 1 || !retained.Search.Complete || retained.Decision != decision {
				t.Fatalf("policy-only evidence changed: %#v", retained)
			}
			approval := pendingCompositeTrackerApproval(t, current)
			eligible := []api.TrackerID{"BETA"}
			if decision == api.DupeDecisionIgnored {
				eligible = []api.TrackerID{"ALPHA", "BETA"}
			}
			var choices []api.TrackerID
			for _, option := range approval.Options {
				choices = append(choices, api.TrackerID(option.Value))
			}
			if !slices.Equal(choices, eligible) {
				t.Fatalf("approval choices = %v, want %v", choices, eligible)
			}
			completed := approveCompositeUploadTrackers(t, module, current, eligible, "approve-policy-review")
			if completed.UploadResult == nil || uploads.execution == nil {
				t.Fatal("eligible trackers did not continue after policy review")
			}
		})
	}
}
