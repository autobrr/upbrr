// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionRuleAuthorizationActionRequiresExactState(t *testing.T) {
	t.Parallel()

	fingerprint := testFingerprint(t, "rule-acknowledgement")
	for _, tc := range []struct {
		name          string
		authorization api.WorkflowFingerprint
		status        api.RequiredActionStatus
		want          bool
	}{
		{
			name:   "pending",
			status: api.RequiredActionStatusPending,
			want:   true,
		},
		{
			name:          "resolved",
			authorization: fingerprint,
			status:        api.RequiredActionStatusResolved,
			want:          true,
		},
		{name: "resolved without authority", status: api.RequiredActionStatusResolved},
		{
			name:          "pending with authority",
			authorization: fingerprint,
			status:        api.RequiredActionStatusPending,
		},
		{
			name:          "stale rules",
			authorization: testFingerprint(t, "stale-rules"),
			status:        api.RequiredActionStatusResolved,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
				TrackerID:                    "ALPHA",
				WaivableRuleFingerprint:      fingerprint,
				RuleAuthorizationFingerprint: tc.authorization,
				RequiredActions: []api.RequiredAction{{
					ID:        "current-action",
					TrackerID: "ALPHA",
					Kind:      api.RequiredActionAuthorizeRules,
					Status:    tc.status,
				}},
			}}}
			if _, _, ok := projectionRuleAuthorizationAction(&projections, "current-action"); ok != tc.want {
				t.Fatalf("current rule action found=%t, want %t", ok, tc.want)
			}
			if _, _, ok := projectionRuleAuthorizationAction(&projections, "obsolete-action"); ok {
				t.Fatal("obsolete rule action was accepted")
			}
		})
	}
}

func TestResolveRuleAcknowledgementRejectsStrictInClientEvidence(t *testing.T) {
	t.Parallel()

	fingerprint := testFingerprint(t, "strict-duplicate-rule-acknowledgement")
	for _, confirmed := range []bool{true, false} {
		action := api.RequiredAction{
			ID:        "rule-action",
			Kind:      api.RequiredActionAuthorizeRules,
			TrackerID: "ALPHA",
			Status:    api.RequiredActionStatusPending,
		}
		projection := api.TrackerReleaseProjection{
			TrackerID:               "ALPHA",
			WaivableRuleFingerprint: fingerprint,
		}
		if !confirmed {
			action.Status = api.RequiredActionStatusResolved
			projection.RuleAuthorizationFingerprint = fingerprint
		}
		projection.RequiredActions = []api.RequiredAction{action}
		state := State{
			Workflow: api.ReleaseWorkflow{
				Revision:           2,
				TrackerProjections: &api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 2},
				Dupes:              &api.DupeAssessmentRef{ID: "dupes", Revision: 2},
			},
			Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{
				"projections": {Projections: []api.TrackerReleaseProjection{projection}},
			},
			Dupes: map[api.DupeAssessmentID]api.DupeAssessment{
				"dupes": {Results: []api.TrackerDupeAssessment{{
					TrackerID: "ALPHA",
					Decision:  api.DupeDecisionAccepted,
					Matches:   []api.DupeMatchProjection{{Reason: "in_client"}},
				}}},
			},
		}
		module := &Module{}
		_, err := module.resolveAction(context.Background(), testOwnerID, &state, 3, time.Now(), ResolveActionCommand{
			ExpectedRevision: 2,
			Answer: api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: 2,
				Confirmed:        &confirmed,
			},
		})
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("strict duplicate rule acknowledgement confirmed=%t: %v", confirmed, err)
		}
	}
}

func TestPreflightPreservesResolvedRuleAcknowledgement(t *testing.T) {
	t.Parallel()

	action := api.RequiredAction{
		Kind:      api.RequiredActionAuthorizeRules,
		Status:    api.RequiredActionStatusResolved,
		TrackerID: "ALPHA",
	}
	assessment := api.TrackerPreflightAssessment{Results: []api.TrackerPreflightResult{{
		TrackerID:       "ALPHA",
		State:           api.TrackerPreflightStateReady,
		RequiredActions: []api.RequiredAction{action},
	}}}
	projection := testProjection(t, "ALPHA", "Example.Release.2026-GRP")
	projection.RequiredActions = []api.RequiredAction{action}
	finalized := []api.TrackerReleaseProjection{projection}
	applyPreflightInteractionPolicy(api.InteractionModeUnattended, &assessment, finalized)
	module := &Module{ids: &sequenceIDGenerator{}}
	if err := module.stampPreflightActions(&assessment, 3, time.Now()); err != nil {
		t.Fatalf("stamp resolved acknowledgement: %v", err)
	}
	if assessment.Results[0].State != api.TrackerPreflightStateReady ||
		len(assessment.Results[0].RequiredActions) != 1 || assessment.Results[0].RequiredActions[0].Status != api.RequiredActionStatusResolved ||
		!finalized[0].DupeReady || !finalized[0].UploadReady {
		t.Fatalf("resolved rule acknowledgement changed preflight: %#v/%#v", assessment, finalized)
	}
}

func TestUnattendedContinuationDoesNotGrantPendingRuleAnswer(t *testing.T) {
	t.Parallel()

	action := api.RequiredAction{
		ID:               "pending-rule-action",
		Kind:             api.RequiredActionAuthorizeRules,
		Status:           api.RequiredActionStatusPending,
		TrackerID:        "ALPHA",
		WorkflowRevision: 2,
	}
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{Revision: 2, RequiredActions: []api.RequiredAction{action}},
		Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
			TrackerID:               "ALPHA",
			WaivableRuleFingerprint: testFingerprint(t, "pending-rules"),
			RequiredActions:         []api.RequiredAction{action},
		}}},
	}
	confirmed := true
	module := &Module{}
	_, handled, err := module.resolveContinuationAnswer(context.Background(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Intent: api.WorkflowIntent{Interaction: api.InteractionModeUnattended},
		Answers: []api.RequiredActionAnswer{{
			ActionID:         action.ID,
			WorkflowRevision: 2,
			Confirmed:        &confirmed,
		}},
	}, current, TrackerDecisionModePostDupeGate)
	if err != nil || handled {
		t.Fatalf("unattended rule answer was handled=%t: %v", handled, err)
	}
}

func TestSubtitleQuestionnaireUnattendedSkipsOnlyItsTracker(t *testing.T) {
	for _, mode := range []api.InteractionMode{api.InteractionModeUnattended, api.InteractionModeInteractive, api.InteractionModeUnattendedConfirm} {
		action := api.RequiredAction{
			Kind:      api.RequiredActionAnswerQuestionnaire,
			TrackerID: "PTP",
			Status:    api.RequiredActionStatusPending,
		}
		assessment := api.TrackerPreflightAssessment{Results: []api.TrackerPreflightResult{{
			TrackerID:       "PTP",
			State:           api.TrackerPreflightStateActionRequired,
			RequiredActions: []api.RequiredAction{action},
		}, {TrackerID: "OTHER", State: api.TrackerPreflightStateReady}}}
		projections := []api.TrackerReleaseProjection{testProjection(t, "PTP", "Example.Movie.2026-GRP"), testProjection(t, "OTHER", "Example.Movie.2026-GRP")}
		projections[0].RequiredActions = []api.RequiredAction{action}
		applyPreflightInteractionPolicy(mode, &assessment, projections)
		if !projections[1].UploadReady || assessment.Results[1].State != api.TrackerPreflightStateReady {
			t.Fatal("unrelated tracker blocked")
		}
		if mode == api.InteractionModeUnattended {
			if projections[0].UploadReady || len(projections[0].RequiredActions) != 0 || !continuationUnattendedSkipsTrackerAction(api.WorkflowIntent{Interaction: mode}, action) {
				t.Fatal("strict unattended did not skip review lane")
			}
		} else if len(projections[0].RequiredActions) != 1 {
			t.Fatal("interactive review was removed")
		}
	}
}
