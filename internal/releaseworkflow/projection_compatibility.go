// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) staleTrackerPolicies(state State) ([]api.TrackerID, error) {
	// Confirmed/uncertain external outcomes retain their existing reconciliation
	// authority. The submission preparation fence still rejects obsolete names.
	if configImpactCompletedWorkflow(&state) || state.Workflow.Status == api.WorkflowStatusCanceled ||
		state.Workflow.UploadResult != nil || state.Workflow.TrackerProjections == nil ||
		slices.ContainsFunc(state.Workflow.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionReconcileSubmission && action.Status == api.RequiredActionStatusPending
		}) {
		return nil, nil
	}
	checker, ok := m.trackerProjector.(TrackerProjectionPolicyChecker)
	if !ok {
		return nil, nil
	}
	projection := currentSnapshot(state.Projections, state.Workflow.TrackerProjections.ID)
	if projection == nil {
		return nil, nil
	}
	selected := make([]api.TrackerID, 0, len(projection.Projections))
	for _, lane := range projection.Projections {
		selected = append(selected, lane.TrackerID)
	}
	var catalog api.TrackerCatalogSnapshot
	if ref := state.Workflow.TrackerCatalog; ref != nil {
		if retained := currentSnapshot(state.Catalogs, ref.ID); retained != nil {
			catalog = *retained
		}
	}
	stale, err := checker.StaleTrackerPolicies(catalog, withoutConfirmedSubmissions(selected, state.Workflow.SubmissionExclusions))
	if err != nil {
		return nil, fmt.Errorf("release workflow check retained tracker policies: %w", err)
	}
	return stale, nil
}

func invalidateStaleTrackerPolicies(state *State, stale []api.TrackerID, revision api.WorkflowRevision, now time.Time) {
	previous := state.Projections[state.Workflow.TrackerProjections.ID]
	next := previous
	next.ID = api.TrackerReleaseProjectionSetID(fmt.Sprintf("policy-projections-%s-%d", previous.ID, revision))
	next.Revision, next.CreatedAt = revision, now
	next.Projections = slices.Clone(previous.Projections)
	for index := range next.Projections {
		lane := &next.Projections[index]
		if slices.Contains(stale, lane.TrackerID) {
			lane.Readiness = api.ReadinessStatusStale
			lane.DupeReady, lane.UploadReady = false, false
		}
	}
	next.Status, next.Preflight = api.StageStatusStale, nil
	next.RequiredActions = filterConfigImpactActions(next.RequiredActions, stale)
	next.Failures = filterConfigImpactFailures(next.Failures, stale)
	state.Projections[next.ID] = next
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: next.ID, Revision: revision}
	state.Workflow.TrackerPreflight = nil
	state.Workflow.RequiredActions = filterConfigImpactActions(state.Workflow.RequiredActions, stale)
	invalidateDupeAndDownstream(&state.Workflow)
}
