// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

type inputReviewReferences struct {
	Release   api.ReleaseSnapshotRef
	Readiness *api.InputReadinessSnapshotRef
}

// retainInputReview preserves the last evaluated Input across repeated failed refreshes.
func retainInputReview(state *State) {
	if state.Workflow.Release == nil || (state.Workflow.InputReadiness == nil && state.InputReview != nil) {
		return
	}
	state.InputReview = &inputReviewReferences{
		Release:   *state.Workflow.Release,
		Readiness: state.Workflow.InputReadiness,
	}
}

func inputReviewContext(state *State) *api.InputReviewContext {
	ref := state.InputReview
	if ref == nil || state.Workflow.InputReadiness != nil {
		return nil
	}
	release, ok := state.Releases[ref.Release.ID]
	if !ok || release.Revision != ref.Release.Revision || release.WorkflowID != state.Workflow.ID {
		return nil
	}
	context := &api.InputReviewContext{
		Release:            release.Release,
		SelectedTrackerIDs: workflowTrackerScope(state, nil),
	}
	if ref.Readiness != nil {
		readiness, found := state.InputReadiness[ref.Readiness.ID]
		if !found || readiness.Revision != ref.Readiness.Revision || readiness.WorkflowID != state.Workflow.ID ||
			readiness.Release.SourcePath != release.Release.Source.SourcePath || readiness.Release.Generation != release.Release.Generation {
			return nil
		}
		for _, field := range readiness.Fields {
			if field.Status == api.InputReadinessFieldReady {
				continue
			}
			if len(field.TrackerIDs) > 0 {
				field.TrackerIDs = slices.DeleteFunc(slices.Clone(field.TrackerIDs), func(id api.TrackerID) bool {
					return !slices.Contains(context.SelectedTrackerIDs, id)
				})
				if len(field.TrackerIDs) == 0 {
					continue
				}
			}
			context.Fields = append(context.Fields, field)
		}
	}
	if len(context.Fields) == 0 && len(context.Release.MissingRequiredUploadFacts()) == 0 {
		return nil
	}
	return context
}
