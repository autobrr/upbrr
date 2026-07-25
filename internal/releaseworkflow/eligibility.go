// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ProjectionEligibleForDownstream reports whether retained projection and
// duplicate state permit media, description, and upload-plan work.
func ProjectionEligibleForDownstream(
	projection api.TrackerReleaseProjection,
	dupe api.TrackerDupeAssessment,
	hasDupe bool,
) bool {
	if !projection.UploadReady || projection.Readiness != api.ReadinessStatusReady || !hasDupe {
		return false
	}
	if dupe.Status == api.StageStatusFailed || dupe.Decision == api.DupeDecisionPending || dupe.Decision == api.DupeDecisionAccepted {
		return false
	}
	return true
}

// DownstreamEligibleProjections applies the shared retained eligibility
// predicate while preserving projection order and snapshot identity.
func DownstreamEligibleProjections(
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
) api.TrackerReleaseProjectionSet {
	dupeByTracker := make(map[api.TrackerID]api.TrackerDupeAssessment, len(dupes.Results))
	for _, result := range dupes.Results {
		dupeByTracker[result.TrackerID] = result
	}
	eligible := projections
	eligible.Projections = make([]api.TrackerReleaseProjection, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		dupe, exists := dupeByTracker[projection.TrackerID]
		if ProjectionEligibleForDownstream(projection, dupe, exists) {
			eligible.Projections = append(eligible.Projections, projection)
		}
	}
	return eligible
}

// TrackerImageHostFailure returns the terminal image-host failure that blocks
// one tracker in the retained media snapshot.
func TrackerImageHostFailure(media api.MediaArtifactSet, trackerID api.TrackerID) (api.WorkflowFailure, bool) {
	trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
	if trackerID == "" {
		return api.WorkflowFailure{}, false
	}
	for _, failure := range media.Failures {
		if failure.Failure.Operation != api.OperationKindImageHosting {
			continue
		}
		failedTracker := api.TrackerID(strings.ToUpper(strings.TrimSpace(string(failure.TrackerID))))
		if failedTracker == trackerID {
			return failure, true
		}
	}
	return api.WorkflowFailure{}, false
}

// DownstreamEligibleProjectionsAfterMedia removes trackers with terminal
// tracker-scoped image-host failures from the regular post-dupe eligible set.
func DownstreamEligibleProjectionsAfterMedia(
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	media api.MediaArtifactSet,
) api.TrackerReleaseProjectionSet {
	eligible := DownstreamEligibleProjections(projections, dupes)
	eligible.Projections = slices.DeleteFunc(eligible.Projections, func(projection api.TrackerReleaseProjection) bool {
		_, failed := TrackerImageHostFailure(media, projection.TrackerID)
		return failed
	})
	return eligible
}
