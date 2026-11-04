// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

type trackerGuidanceHistoryKey struct{}

// withTrackerGuidanceHistory reuses retained preflight snapshots under the
// command lock. Composite feedback keeps history since the attempt started;
// direct stage workflows use the current prepared snapshot.
func withTrackerGuidanceHistory(ctx context.Context, state State) context.Context {
	var history []api.TrackerReleaseProjection
	for _, snapshot := range state.Projections {
		if snapshot.Preflight == nil {
			continue
		}
		if state.Composite != nil && state.Composite.StartedRevision > 0 {
			if snapshot.Revision < state.Composite.StartedRevision {
				continue
			}
		} else if state.Workflow.Release == nil || snapshot.Release != *state.Workflow.Release {
			continue
		}
		history = append(history, snapshot.Projections...)
	}
	return context.WithValue(ctx, trackerGuidanceHistoryKey{}, history)
}

// TrackerGuidanceWasPresented reports whether this attempt already published
// the same advisory. History is command-local, read-only, and absent for callers
// outside the workflow coordinator; no credentials or new state are retained.
func TrackerGuidanceWasPresented(ctx context.Context, tracker api.TrackerID, code, message string) bool {
	history, _ := ctx.Value(trackerGuidanceHistoryKey{}).([]api.TrackerReleaseProjection)
	for _, projection := range history {
		if projection.TrackerID != tracker {
			continue
		}
		for _, decision := range projection.PolicyDecisions {
			if decision.Code == code && decision.Message == message {
				return true
			}
		}
	}
	return false
}
