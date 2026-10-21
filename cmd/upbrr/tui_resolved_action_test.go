// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolvedRuleHistoryPreservesActualAuthFailureLaneColor(t *testing.T) {
	const epoch = "resolved-rule-current-process"
	for _, code := range []api.OperationFailureCode{api.OperationFailureTrackerAuthRequired, api.OperationFailureTrackerAuthUnavailable} {
		t.Run(string(code), func(t *testing.T) {
			// The real preflight builder preserves only the resolved rule action
			// on both auth failures. Its producer tests assert this exact shape.
			action := api.RequiredAction{
				ID:        "acknowledged-rules",
				Kind:      api.RequiredActionAuthorizeRules,
				Status:    api.RequiredActionStatusResolved,
				TrackerID: "ALPHA",
			}
			failures := []api.WorkflowFailure{{TrackerID: "ALPHA", Failure: api.OperationFailure{Code: code}}}
			projections := api.TrackerReleaseProjectionSet{
				ID:       "auth-projections",
				Revision: 1,
				Projections: []api.TrackerReleaseProjection{{
					TrackerID:       "ALPHA",
					Readiness:       api.ReadinessStatusBlocked,
					RequiredActions: []api.RequiredAction{action},
					Failures:        failures,
				}},
			}
			preflight := api.TrackerPreflightAssessment{
				ID:       "auth-preflight",
				Revision: 1,
				Results: []api.TrackerPreflightResult{{
					TrackerID:       "ALPHA",
					State:           api.TrackerPreflightStateRetryable,
					RequiredActions: []api.RequiredAction{action},
					Failures:        failures,
				}},
			}
			repository := releaseworkflow.NewMemoryRepository()
			_, _, err := repository.Create(t.Context(), cliWorkflowOwnerID, "auth-history", "", releaseworkflow.State{
				ProcessEpoch: epoch,
				Workflow: api.ReleaseWorkflow{
					ID:                 "auth-history-workflow",
					Revision:           1,
					TrackerProjections: &api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
					TrackerPreflight:   &api.TrackerPreflightAssessmentRef{ID: preflight.ID, Revision: preflight.Revision},
				},
				Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{projections.ID: projections},
				Preflights:  map[api.TrackerPreflightAssessmentID]api.TrackerPreflightAssessment{preflight.ID: preflight},
			})
			if err != nil {
				t.Fatal(err)
			}
			module, err := releaseworkflow.New(repository, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{},
				releaseworkflow.WithProcessEpoch(epoch))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := module.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			current, err := module.Current(t.Context(), cliWorkflowOwnerID, "auth-history-workflow")
			if err != nil {
				t.Fatal(err)
			}
			if len(current.Continuation.TrackerOutcomes) != 1 {
				t.Fatal("current query did not materialize the auth failure lane")
			}
			outcome := current.Continuation.TrackerOutcomes[0]
			if outcome.Lifecycle != api.OperationLifecycleTerminal || outcome.Disposition != api.WorkflowDispositionFailed ||
				len(outcome.RequiredActions) != 1 || outcome.RequiredActions[0].Status != api.RequiredActionStatusResolved {
				t.Fatal("real continuation did not preserve failed outcome plus resolved history")
			}
			bridge := newCLITUIBridge()
			session := &cliWorkflowSession{
				current:       current,
				streams:       cliIO{presenter: bridge},
				uploadRequest: api.Request{Trackers: []string{"ALPHA"}},
			}
			session.publishPresentation(t.Context())
			lane := bridge.latest.Lanes[0]
			if lane.Status != cliLaneBlocked || lane.State == "Awaiting decision" || lane.Reason != string(code) {
				t.Fatal("resolved acknowledgement replaced the retained auth blocker")
			}
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, false)
			model.Update(tea.WindowSizeMsg{Width: 232, Height: 60})
			model.Update(cliBridgeUpdate{view: &bridge.latest})
			if !strings.Contains(model.panels[cliPanelTrackers].View(), "\x1b[31mALPHA") {
				t.Fatal("auth-blocked tracker name lost its red color")
			}
		})
	}
}
