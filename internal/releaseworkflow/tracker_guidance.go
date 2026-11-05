// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

// withTrackerGuidanceHistory reuses retained preflight snapshots under the
// command lock. Composite feedback keeps history since the attempt started;
// prior upload results end an attempt, including direct failed-upload retries.
// Direct stage workflows also require the current prepared snapshot.
func withTrackerGuidanceHistory(ctx context.Context, state State) context.Context {
	var lastUpload api.WorkflowRevision
	for _, result := range state.UploadResults {
		lastUpload = max(lastUpload, result.Revision)
	}
	var history []api.TrackerReleaseProjection
	for _, snapshot := range state.Projections {
		if snapshot.Preflight == nil || snapshot.Revision <= lastUpload {
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
	return api.WithTrackerGuidanceHistory(ctx, history)
}
