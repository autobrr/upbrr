// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestQuestionnaireDuplicateCandidatesRequireExactFreshServerEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*State)
		want   bool
	}{
		{"current answer edit", func(*State) {}, true},
		{"other edit", func(s *State) { s.PendingDuplicateReuse.QuestionnaireOnly = false }, false},
		{"changed generation", func(s *State) { a := s.Dupes["dupes"]; a.ReleaseRef.Generation++; s.Dupes["dupes"] = a }, false},
		{"expired", func(s *State) { a := s.Dupes["dupes"]; a.ExpiresAt = time.Time{}; s.Dupes["dupes"] = a }, false},
		{"wrong projection", func(s *State) {
			a := s.Dupes["dupes"]
			a.Results[0].ProjectionFingerprint = "wrong"
			s.Dupes["dupes"] = a
		}, false},
		{"invalidated tracker", func(s *State) { s.PendingDuplicateReuse.InvalidatedTrackers = []api.TrackerID{"LST"} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now()
			projection := api.TrackerReleaseProjection{TrackerID: "LST"}
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				t.Fatal(err)
			}
			state := &State{
				PendingDuplicateReuse: &PendingDuplicateReuse{QuestionnaireOnly: true, Assessment: api.DupeAssessmentRef{ID: "dupes", Revision: 1}},
				Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{"projections": {
					ID:          "projections",
					Revision:    1,
					Projections: []api.TrackerReleaseProjection{projection},
				}},
				Dupes: map[api.DupeAssessmentID]api.DupeAssessment{"dupes": {
					ID:            "dupes",
					Revision:      1,
					ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1},
					ExpiresAt:     now.Add(time.Hour),
					Results: []api.TrackerDupeAssessment{{
						TrackerID:             "LST",
						ProjectionFingerprint: fingerprint,
						FreshUntil:            now.Add(time.Hour),
						Matches:               []api.DupeMatchProjection{{ID: "candidate", EvidenceFingerprint: "evidence"}},
					}},
				}},
			}
			test.mutate(state)
			got := questionnaireDuplicateCandidates(state, api.ReleaseSnapshot{}, now)
			if (len(got["LST"]) != 0) != test.want {
				t.Fatalf("candidates = %+v; want available %t", got, test.want)
			}
		})
	}
}

func TestDuplicateQuestionnaireFeedbackRequiresCurrentCandidateQuestions(t *testing.T) {
	current := CommandResult{
		Workflow:    api.ReleaseWorkflow{ID: "workflow"},
		Projections: &api.TrackerReleaseProjectionSet{ID: "projections", Revision: 1},
		Dupes: &api.DupeAssessment{
			WorkflowID:    "workflow",
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1},
			Results:       []api.TrackerDupeAssessment{{TrackerID: "LST", Questionnaire: []api.TrackerQuestionnaireRequirement{{Key: "candidate-bound"}}}},
		},
	}
	action := api.RequiredAction{Kind: api.RequiredActionReviewDuplicates, TrackerID: "LST"}
	response := compositeUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackQuestionnaire, TrackerID: "LST"}
	if !duplicateQuestionnaireFeedbackAllowed(current, action, response) {
		t.Fatal("current optional comparison rejected")
	}
	current.Dupes.ProjectionSet.Revision++
	if duplicateQuestionnaireFeedbackAllowed(current, action, response) {
		t.Fatal("stale comparison accepted")
	}
	current.Dupes.ProjectionSet.Revision--
	response.TrackerID = "OTHER"
	if duplicateQuestionnaireFeedbackAllowed(current, action, response) {
		t.Fatal("other tracker accepted")
	}
	response.TrackerID = "LST"
	current.Dupes.Results[0].Questionnaire = nil
	if duplicateQuestionnaireFeedbackAllowed(current, action, response) {
		t.Fatal("questions invented for ordinary duplicate")
	}
}

func TestCompositeDuplicateComparisonReprojectsBoundCandidatesAndRechecks(t *testing.T) {
	module, _, uploads := newCompositeUploadTestModule(t)
	baseProjector := module.trackerProjector
	bound := false
	module.trackerProjector = trackerProjectionBuilderFunc(func(ctx context.Context, release api.ReleaseSnapshot, subject api.UploadSubject, ids []api.TrackerID, instructions map[api.TrackerID]api.TrackerProjectionInstructions, authorizations map[api.TrackerID]api.WorkflowFingerprint, mode api.WorkflowExecutionMode) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		if len(instructions["ALPHA"].Questionnaire) > 0 {
			matches := subject.TrackerDuplicateReviewCandidates["ALPHA"]
			bound = len(matches) == 1 && matches[0].ID == "candidate" && matches[0].EvidenceFingerprint == "current"
		}
		return baseProjector.Build(ctx, release, subject, ids, instructions, authorizations, mode)
	})
	baseDupe := compositeUploadDuplicateBlockedBuilder(module.dupeBuilder, "ALPHA", "similar_release")
	checks := 0
	module.dupeBuilder = dupeAssessmentBuilderFunc(func(ctx context.Context, subject api.DuplicateSubject, projections api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, now time.Time, skip bool) (api.DupeAssessment, any, error) {
		checks++
		assessment, evidence, err := baseDupe.Build(ctx, subject, projections, preflight, now, skip)
		for index := range assessment.Results {
			result := &assessment.Results[index]
			if result.TrackerID != "ALPHA" {
				continue
			}
			result.Decision = api.DupeDecisionPending
			result.Status = api.StageStatusBlocked
			result.RequiredActions = []api.RequiredAction{{
				Kind:      api.RequiredActionReviewDuplicates,
				TrackerID: "ALPHA",
				Prompt:    "Review duplicate evidence.",
			}}
			result.Matches = []api.DupeMatchProjection{{
				ID:                  "candidate",
				EvidenceFingerprint: "current",
				Relation:            api.DupeRelationManualReview,
				Reason:              "lst_internal_comparison_required",
			}}
			result.Questionnaire = []api.TrackerQuestionnaireRequirement{{Key: "candidate-bound", Kind: "text"}}
		}
		if err != nil {
			return api.DupeAssessment{}, nil, fmt.Errorf("build comparison fixture: %w", err)
		}
		return assessment, evidence, nil
	})
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "candidate-comparison")
	request.Duplicates.OnEvidence = api.ReleaseWorkflowDuplicateAsk
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	action := pendingCompositeFeedbackAction(t, current, api.RequiredActionReviewDuplicates, "ALPHA")
	prior := current.Dupes.ID
	current = submitCompositeFeedbackForTest(t, module, current, action, api.ReleaseWorkflowUploadFeedbackResponse{
		Kind:          api.ReleaseWorkflowUploadFeedbackQuestionnaire,
		Questionnaire: &api.ReleaseWorkflowUploadQuestionnaire{TrackerID: "ALPHA", Answers: map[string]*string{"candidate-bound": new("reviewed")}},
	}, "candidate-answer")
	if !bound || checks < 2 || current.Dupes == nil || current.Dupes.ID == prior {
		t.Fatalf("comparison context=%t checks=%d dupes=%+v", bound, checks, current.Dupes)
	}
	if uploads.execution != nil || current.TrackerApproval != nil {
		t.Fatal("reviewing comparison approved upload")
	}
}
