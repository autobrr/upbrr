// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import "github.com/autobrr/upbrr/pkg/api"

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
