// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

type trackerGuidanceHistoryKey struct{}

// withTrackerGuidanceHistory reuses retained preflight snapshots under the
// command lock, so reprojection does not reset advisory presentation history.
func withTrackerGuidanceHistory(ctx context.Context, state State) context.Context {
	return context.WithValue(ctx, trackerGuidanceHistoryKey{}, state.Projections)
}

// TrackerGuidanceWasPresented reports whether this workflow already published
// the same advisory. History is command-local, read-only, and absent for callers
// outside the workflow coordinator; no credentials or new state are retained.
func TrackerGuidanceWasPresented(ctx context.Context, tracker api.TrackerID, code, message string) bool {
	history, _ := ctx.Value(trackerGuidanceHistoryKey{}).(map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet)
	for _, snapshot := range history {
		if snapshot.Preflight == nil {
			continue
		}
		for _, projection := range snapshot.Projections {
			if projection.TrackerID != tracker {
				continue
			}
			for _, decision := range projection.PolicyDecisions {
				if decision.Code == code && decision.Message == message {
					return true
				}
			}
		}
	}
	return false
}
