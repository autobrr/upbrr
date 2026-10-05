// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeFeedbackRebuildsPendingDuplicateReview(t *testing.T) {
	for _, kind := range []api.ReleaseWorkflowUploadFeedbackKind{api.ReleaseWorkflowUploadFeedbackQuestionnaire, api.ReleaseWorkflowUploadFeedbackTrackerInput} {
		for _, duplicateTracker := range []api.TrackerID{"ALPHA", "BETA"} {
			t.Run(string(kind)+"/"+string(duplicateTracker), func(t *testing.T) {
				module, _, uploads := newCompositeUploadTestModule(t)
				duplicateBase := compositeUploadDuplicateBlockedBuilder(module.dupeBuilder, duplicateTracker, "similar_release")
				checks := 0
				module.dupeBuilder = dupeAssessmentBuilderFunc(func(ctx context.Context, subject api.DuplicateSubject, projections api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, now time.Time, skip bool) (api.DupeAssessment, any, error) {
					checks++
					assessment, evidence, err := duplicateBase.Build(ctx, subject, projections, preflight, now, skip)
					if err != nil {
						return api.DupeAssessment{}, nil, fmt.Errorf("build feedback duplicate assessment: %w", err)
					}
					for i := range assessment.Results {
						if assessment.Results[i].TrackerID == duplicateTracker {
							assessment.Results[i].Decision = api.DupeDecisionPending
							assessment.Results[i].Status = api.StageStatusBlocked
							assessment.Results[i].RequiredActions = []api.RequiredAction{{
								Kind:      api.RequiredActionReviewDuplicates,
								TrackerID: duplicateTracker,
								Prompt:    "Review duplicate evidence.",
							}}
						}
					}
					if checks == 1 {
						actionKind := api.RequiredActionAnswerQuestionnaire
						if kind == api.ReleaseWorkflowUploadFeedbackTrackerInput {
							actionKind = api.RequiredActionProvideTrackerInput
						}
						assessment.Results[0].RequiredActions = append(assessment.Results[0].RequiredActions, api.RequiredAction{
							Kind:      actionKind,
							TrackerID: "ALPHA",
							Prompt:    "Review tracker input.",
						})
					}
					return assessment, evidence, nil
				})
				request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "feedback-recheck")
				request.Duplicates.OnEvidence = api.ReleaseWorkflowDuplicateAsk
				started, err := module.StartUpload(t.Context(), testOwnerID, request)
				if err != nil {
					t.Fatal(err)
				}
				current := waitCompositeUploadTestOperation(t, module, started)
				actionKind := api.RequiredActionAnswerQuestionnaire
				if kind == api.ReleaseWorkflowUploadFeedbackTrackerInput {
					actionKind = api.RequiredActionProvideTrackerInput
				}
				action := pendingCompositeFeedbackAction(t, current, actionKind, "ALPHA")
				oldReview := pendingCompositeFeedbackAction(t, current, api.RequiredActionReviewDuplicates, duplicateTracker)
				if current.Dupes == nil {
					t.Fatal("initial duplicate evidence absent")
				}
				prior := current.Dupes.ID
				response := api.ReleaseWorkflowUploadFeedbackResponse{Kind: kind}
				if kind == api.ReleaseWorkflowUploadFeedbackQuestionnaire {
					response.Questionnaire = &api.ReleaseWorkflowUploadQuestionnaire{TrackerID: "ALPHA", Answers: map[string]*string{"trumpable_review": new("yes")}}
				} else {
					response.TrackerInput = &api.ReleaseWorkflowUploadTrackerInput{TrackerID: "ALPHA", Projection: api.ReleaseWorkflowUploadTrackerProjection{AdditionalNames: map[string]*string{"alternate": new("Example Alternate")}}}
				}
				resumed, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, current.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
					Action:         api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: current.Workflow.Revision},
					Response:       response,
					IdempotencyKey: "answer-feedback",
				})
				if err != nil {
					t.Fatal(err)
				}
				current = waitCompositeUploadTestOperation(t, module, resumed)
				if current.Dupes == nil || current.Dupes.ID == prior || checks < 2 {
					t.Fatalf("feedback did not refresh duplicate evidence: dupes=%#v checks=%d operation=%#v", current.Dupes, checks, current.Operation)
				}
				review := pendingCompositeFeedbackAction(t, current, api.RequiredActionReviewDuplicates, duplicateTracker)
				if review.ID == oldReview.ID {
					t.Fatal("obsolete duplicate review survived re-projection")
				}
				if current.TrackerApproval != nil || current.Media != nil || current.DryRun != nil || current.UploadResult != nil || uploads.execution != nil {
					t.Fatal("feedback granted downstream upload authority")
				}
				duplicateFeedback := api.ReleaseWorkflowUploadFeedback{
					Action:         api.ReleaseWorkflowUploadActionIdentity{ID: oldReview.ID, WorkflowRevision: current.Workflow.Revision},
					Response:       api.ReleaseWorkflowUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackDuplicateReview, DuplicateReview: &api.ReleaseWorkflowUploadDuplicateReview{TrackerID: duplicateTracker, Decision: api.DupeDecisionIgnored}},
					IdempotencyKey: "review-refreshed-duplicates",
				}
				if _, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, current.Workflow.ID, duplicateFeedback); !errors.Is(err, ErrRevisionConflict) {
					t.Fatalf("obsolete review error = %v", err)
				}
				duplicateFeedback.Action.ID = review.ID
				resumed, err = module.SubmitUploadFeedback(t.Context(), testOwnerID, current.Workflow.ID, duplicateFeedback)
				if err != nil {
					t.Fatal(err)
				}
				current = waitCompositeUploadTestOperation(t, module, resumed)
				pendingCompositeTrackerApproval(t, current)
				if current.TrackerApproval != nil || uploads.execution != nil {
					t.Fatal("duplicate review bypassed explicit tracker approval")
				}
				current = approveCompositeUploadTrackers(t, module, current, []api.TrackerID{"ALPHA", "BETA"}, "approve-refreshed-trackers")
				if current.UploadResult == nil || uploads.execution == nil || uploads.execution.executions != 1 {
					t.Fatalf("approved upload did not complete: %#v", current.Operation)
				}
			})
		}
	}
}

func pendingCompositeFeedbackAction(t *testing.T, current CommandResult, kind api.RequiredActionKind, tracker api.TrackerID) api.RequiredAction {
	t.Helper()
	index := slices.IndexFunc(current.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == kind && action.TrackerID == tracker && action.Status == api.RequiredActionStatusPending
	})
	if index < 0 {
		t.Fatalf("missing %s action for %s: %#v operation=%#v", kind, tracker, current.Continuation.RequiredActions, current.Operation)
	}
	return current.Continuation.RequiredActions[index]
}

func TestCompositeQuestionnaireRetainsDependentAnswersAndReviews(t *testing.T) {
	module, _, uploads := newCompositeUploadTestModuleConfigured(t, true, true)
	base := module.trackerProjector
	module.trackerProjector = trackerProjectionBuilderFunc(func(ctx context.Context, release api.ReleaseSnapshot, subject api.UploadSubject, ids []api.TrackerID, instructions map[api.TrackerID]api.TrackerProjectionInstructions, authorizations map[api.TrackerID]api.WorkflowFingerprint, mode api.WorkflowExecutionMode) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		catalog, runtime, selection, projections, err := base.Build(ctx, release, subject, ids, instructions, authorizations, mode)
		if err != nil {
			return catalog, runtime, selection, projections, fmt.Errorf("build dependent questionnaire projection: %w", err)
		}
		projection := &projections.Projections[0]
		if projection.UploadReady {
			answers := instructions["ALPHA"].Questionnaire
			projection.QuestionnaireAnswers = make(map[string]string)
			for key, value := range answers {
				if value != nil {
					projection.QuestionnaireAnswers[key] = *value
				}
			}
			missing := "trumpable_review"
			if projection.QuestionnaireAnswers[missing] == "yes" {
				missing = "subtitle_tag"
			}
			if projection.QuestionnaireAnswers[missing] == "" {
				action := api.RequiredAction{
					Kind:      api.RequiredActionAnswerQuestionnaire,
					TrackerID: "ALPHA",
					Prompt:    missing,
				}
				projection.RequiredActions = append(projection.RequiredActions, action)
				projections.RequiredActions = append(projections.RequiredActions, action)
			}
		}
		return catalog, runtime, selection, projections, nil
	})
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "sequential-questionnaire")
	request.Trackers.Include = []api.TrackerID{"ALPHA"}
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	action := pendingCompositeFeedbackAction(t, current, api.RequiredActionAuthorizeRules, "ALPHA")
	current = submitCompositeFeedbackForTest(t, module, current, action, api.ReleaseWorkflowUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackRuleAuthorization, RuleAuthorization: &api.ReleaseWorkflowUploadConfirmation{Confirmed: true}}, "acknowledge-rule")
	action = pendingCompositeFeedbackAction(t, current, api.RequiredActionProvideTrackerInput, "ALPHA")
	current = submitCompositeFeedbackForTest(t, module, current, action, api.ReleaseWorkflowUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackTrackerInput, TrackerInput: &api.ReleaseWorkflowUploadTrackerInput{TrackerID: "ALPHA", Projection: api.ReleaseWorkflowUploadTrackerProjection{UploadReleaseName: api.WorkflowPatch[string]{Present: true, Value: "Example.Release.2026.ALPHA-GRP"}}}}, "review-name")
	nameFingerprint := current.ProjectionInstructions.Instructions["ALPHA"].ConfirmedNameFingerprint
	ruleFingerprint := current.Projections.Projections[0].RuleAuthorizationFingerprint
	if nameFingerprint == "" || ruleFingerprint == "" {
		t.Fatal("fixture did not retain reviewed name and rule acknowledgement")
	}
	answers := map[string]*string{"trumpable_review": new("yes")}
	for _, key := range []string{"trumpable_review", "subtitle_tag"} {
		action = pendingCompositeFeedbackAction(t, current, api.RequiredActionAnswerQuestionnaire, "ALPHA")
		if action.Prompt != key {
			t.Fatalf("question = %q, want %q", action.Prompt, key)
		}
		if key == "subtitle_tag" {
			answers[key] = new("no_english_subtitles")
		}
		current = submitCompositeFeedbackForTest(t, module, current, action, api.ReleaseWorkflowUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackQuestionnaire, Questionnaire: &api.ReleaseWorkflowUploadQuestionnaire{TrackerID: "ALPHA", Answers: answers}}, "answer-"+key)
		if current.ProjectionInstructions == nil || current.ProjectionInstructions.Instructions["ALPHA"].ConfirmedNameFingerprint != nameFingerprint || current.Projections.Projections[0].RuleAuthorizationFingerprint != ruleFingerprint {
			t.Fatal("questionnaire feedback lost compatible review authority")
		}
	}
	if current.Dupes == nil || current.Dupes.ProjectionSet != *current.Workflow.TrackerProjections {
		t.Fatal("dependent answers did not produce current duplicate evidence")
	}
	projection := current.Projections.Projections[0]
	if projection.QuestionnaireAnswers["trumpable_review"] != "yes" || projection.QuestionnaireAnswers["subtitle_tag"] != "no_english_subtitles" {
		t.Fatalf("accepted answers = %#v", projection.QuestionnaireAnswers)
	}
	pendingCompositeTrackerApproval(t, current)
	if uploads.execution != nil || current.TrackerApproval != nil {
		t.Fatal("questionnaire answers bypassed upload approval")
	}
}

func submitCompositeFeedbackForTest(t *testing.T, module *Module, current CommandResult, action api.RequiredAction, response api.ReleaseWorkflowUploadFeedbackResponse, key string) CommandResult {
	t.Helper()
	resumed, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, current.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action:         api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: current.Workflow.Revision},
		Response:       response,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return waitCompositeUploadTestOperation(t, module, resumed)
}
