// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// ApplyConfigImpact returns the next durable workflow record after one
// effective configuration impact. It changes only the current workflow refs;
// retained historical snapshots and successful external outcomes remain audit
// evidence for compatible reuse.
func ApplyConfigImpact(record api.ReleaseWorkflowStateRecord, impact api.ConfigImpactDetail) (api.ReleaseWorkflowStateRecord, error) {
	state, err := decodeWorkflowState(record)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, err
	}
	if configImpactCompletedWorkflow(&state) {
		// Completed submissions remain immutable audit authority. Reopening one
		// under a later runtime could otherwise withdraw confirmed outcomes.
		return record, nil
	}
	if state.Workflow.Revision >= math.MaxInt64 {
		return api.ReleaseWorkflowStateRecord{}, errors.New("release workflow: revision exhausted during config activation")
	}
	switch impact.Kind {
	case api.ConfigImpactProvider:
		invalidatePreparedAndDownstream(&state.Workflow)
	case api.ConfigImpactTrackers:
		if len(impact.TrackerIDs) == 0 {
			invalidateTrackerAndDownstream(&state.Workflow)
		} else {
			invalidateTrackerLanes(&state, impact.TrackerIDs)
		}
	case api.ConfigImpactDescription:
		state.Workflow.Descriptions = nil
		invalidateUploadPlan(&state.Workflow)
	case api.ConfigImpactScreenshotSelection, api.ConfigImpactScreenshotCapture:
		state.Workflow.Media = nil
		state.Workflow.Descriptions = nil
		invalidateUploadPlan(&state.Workflow)
	case api.ConfigImpactImageHosting:
		// The retained snapshots remain available for history/reuse, but their
		// hosted links are scoped to the old account and host policy.
		state.Workflow.Media = nil
		state.Workflow.Descriptions = nil
		invalidateUploadPlan(&state.Workflow)
	case api.ConfigImpactClientInjection:
		// Submission receipts and prepared media are independent of the
		// configured client. Retain a terminal tracker result so a failed
		// injection can be retried against the new client configuration.
		state.Workflow.DryRun = nil
	case api.ConfigImpactPresentation:
		// Presentation has no workflow dependency or workflow revision.
		return record, nil
	default:
		return api.ReleaseWorkflowStateRecord{}, errors.New("release workflow: unknown config impact")
	}
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = nextConfigActivationTime(state.Workflow.UpdatedAt)
	if err := state.Workflow.Validate(); err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("release workflow: config impact produced invalid workflow: %w", err)
	}
	updated, err := workflowStateRecord(record.OwnerID, state)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, err
	}
	updated.CreationKey = record.CreationKey
	updated.CreationFingerprint = record.CreationFingerprint
	return updated, nil
}

func configImpactCompletedWorkflow(state *State) bool {
	if state.Workflow.Status != api.WorkflowStatusCompleted {
		return false
	}
	if state.Workflow.UploadResult == nil {
		return true
	}
	result, ok := state.UploadResults[state.Workflow.UploadResult.ID]
	return ok && result.Revision == state.Workflow.UploadResult.Revision && result.Status == api.StageStatusCompleted
}

func nextConfigActivationTime(previous time.Time) time.Time {
	next := previous.Add(time.Nanosecond)
	if next.After(previous) {
		return next
	}
	return previous
}

func invalidateTrackerLanes(state *State, trackerIDs []api.TrackerID) {
	if state.Workflow.TrackerProjections == nil {
		return
	}
	invalid := normalizeContinuationTrackerIDs(trackerIDs)
	if len(invalid) == 0 {
		invalidateTrackerAndDownstream(&state.Workflow)
		return
	}
	projectionRef := *state.Workflow.TrackerProjections
	projections, ok := state.Projections[projectionRef.ID]
	if !ok || projections.Revision != projectionRef.Revision {
		invalidateTrackerAndDownstream(&state.Workflow)
		return
	}
	projections.Projections = slices.DeleteFunc(slices.Clone(projections.Projections), func(projection api.TrackerReleaseProjection) bool {
		return slices.Contains(invalid, normalizeDownstreamTrackerID(projection.TrackerID))
	})
	if len(projections.Projections) == 0 {
		invalidateTrackerAndDownstream(&state.Workflow)
		return
	}
	nextRevision := state.Workflow.Revision + 1
	now := nextConfigActivationTime(state.Workflow.UpdatedAt)
	projections.ID = api.TrackerReleaseProjectionSetID(configImpactSnapshotID("projections", projectionRef.ID, nextRevision))
	projections.Revision = nextRevision
	projections.Preflight = nil
	projections.Status = api.StageStatusStale
	projections.RequiredActions = filterConfigImpactActions(projections.RequiredActions, invalid)
	projections.Failures = filterConfigImpactFailures(projections.Failures, invalid)
	projections.CreatedAt = now
	state.Projections[projections.ID] = projections
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: nextRevision}

	if state.Workflow.TrackerPreflight != nil {
		ref := *state.Workflow.TrackerPreflight
		if preflight, ok := state.Preflights[ref.ID]; ok && preflight.Revision == ref.Revision {
			preflight.Results = slices.DeleteFunc(slices.Clone(preflight.Results), func(result api.TrackerPreflightResult) bool {
				return slices.Contains(invalid, normalizeDownstreamTrackerID(result.TrackerID))
			})
			if len(preflight.Results) == 0 {
				state.Workflow.TrackerPreflight = nil
			} else {
				preflight.ID = api.TrackerPreflightAssessmentID(configImpactSnapshotID("preflight", ref.ID, nextRevision))
				preflight.Revision = nextRevision
				preflight.ProjectionSet = *state.Workflow.TrackerProjections
				preflight.Status = api.StageStatusStale
				preflight.CreatedAt = now
				preflight.ExpiresAt = now
				state.Preflights[preflight.ID] = preflight
				state.Workflow.TrackerPreflight = &api.TrackerPreflightAssessmentRef{ID: preflight.ID, Revision: nextRevision}
			}
		} else {
			state.Workflow.TrackerPreflight = nil
		}
	}
	if state.Workflow.Dupes != nil {
		ref := *state.Workflow.Dupes
		if dupes, ok := state.Dupes[ref.ID]; ok && dupes.Revision == ref.Revision {
			dupes.Results = slices.DeleteFunc(slices.Clone(dupes.Results), func(result api.TrackerDupeAssessment) bool {
				return slices.Contains(invalid, normalizeDownstreamTrackerID(result.TrackerID))
			})
			if len(dupes.Results) == 0 {
				state.Workflow.Dupes = nil
			} else {
				dupes.ID = api.DupeAssessmentID(configImpactSnapshotID("dupes", ref.ID, nextRevision))
				dupes.Revision = nextRevision
				dupes.ProjectionSet = *state.Workflow.TrackerProjections
				dupes.Preflight = state.Workflow.TrackerPreflight
				dupes.Status = api.StageStatusStale
				dupes.CreatedAt = now
				dupes.ExpiresAt = now
				state.Dupes[dupes.ID] = dupes
				state.Workflow.Dupes = &api.DupeAssessmentRef{ID: dupes.ID, Revision: nextRevision}
			}
		} else {
			state.Workflow.Dupes = nil
		}
	}
	state.Workflow.TrackerApproval = nil
	state.Workflow.Media = nil
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	state.Workflow.RequiredActions = filterConfigImpactActions(state.Workflow.RequiredActions, invalid)
	state.Workflow.Failures = filterConfigImpactFailures(state.Workflow.Failures, invalid)
	if state.Workflow.Status == api.WorkflowStatusBlocked && len(state.Workflow.RequiredActions) == 0 && len(state.Workflow.Failures) == 0 {
		state.Workflow.Status = api.WorkflowStatusActive
	}
}

func configImpactSnapshotID(kind string, previous any, revision api.WorkflowRevision) string {
	return fmt.Sprintf("config-%s-%v-%d", kind, previous, revision)
}

func filterConfigImpactActions(actions []api.RequiredAction, invalid []api.TrackerID) []api.RequiredAction {
	return slices.DeleteFunc(slices.Clone(actions), func(action api.RequiredAction) bool {
		return action.TrackerID != "" && slices.Contains(invalid, normalizeDownstreamTrackerID(action.TrackerID))
	})
}

func filterConfigImpactFailures(failures []api.WorkflowFailure, invalid []api.TrackerID) []api.WorkflowFailure {
	return slices.DeleteFunc(slices.Clone(failures), func(failure api.WorkflowFailure) bool {
		return failure.TrackerID != "" && slices.Contains(invalid, normalizeDownstreamTrackerID(failure.TrackerID))
	})
}
