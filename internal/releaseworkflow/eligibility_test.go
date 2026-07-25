// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDownstreamEligibleProjectionsAfterMediaExcludesOnlyFailedImageHostTracker(t *testing.T) {
	t.Parallel()

	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:   "ALPHA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		},
		{
			TrackerID:   "BETA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		},
	}}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{
		{
			TrackerID: "ALPHA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
		{
			TrackerID: "BETA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
	}}
	media := api.MediaArtifactSet{Failures: []api.WorkflowFailure{
		{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureImageHostUnavailable,
				Operation: api.OperationKindImageHosting,
				Message:   "Required image host failed.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "beta",
			Resource:  "pixhost",
		},
		{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureInternal,
				Operation: api.OperationKindMedia,
				Message:   "Unrelated media warning.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "ALPHA",
		},
	}}

	eligible := DownstreamEligibleProjectionsAfterMedia(projections, dupes, media)
	if len(eligible.Projections) != 1 || eligible.Projections[0].TrackerID != "ALPHA" {
		t.Fatalf("media-aware eligible projections = %#v", eligible.Projections)
	}
	failure, failed := TrackerImageHostFailure(media, "BETA")
	if !failed || failure.Resource != "pixhost" {
		t.Fatalf("tracker image-host failure = %#v, found=%t", failure, failed)
	}

	media.Failures = nil
	eligible = DownstreamEligibleProjectionsAfterMedia(projections, dupes, media)
	if len(eligible.Projections) != 2 {
		t.Fatalf("eligible projections after successful retry = %#v", eligible.Projections)
	}
}
