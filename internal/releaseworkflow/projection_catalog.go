// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) projectionCatalogStale(current CommandResult) (bool, error) {
	checker, ok := m.trackerProjector.(TrackerProjectionCatalogChecker)
	if !ok || current.Projections == nil || current.Workflow.Status == api.WorkflowStatusCompleted || current.Workflow.UploadResult != nil {
		return false, nil
	}
	// Preserve the authority needed to reconcile uncertain external effects.
	if slices.ContainsFunc(current.Workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionReconcileSubmission && action.Status == api.RequiredActionStatusPending
	}) {
		return false, nil
	}
	if current.Catalog == nil {
		return true, nil
	}
	valid, err := checker.CatalogCurrent(*current.Catalog)
	if err != nil {
		return false, fmt.Errorf("release workflow check projection catalog: %w", err)
	}
	return !valid, nil
}

// planCatalogRefresh replaces obsolete tracker authority before goals or pending
// actions can reuse it. Submitted and uncertain outcomes retain their authority;
// source facts and reusable artifacts retain their owners.
func (m *Module) planCatalogRefresh(request api.ContinueReleaseWorkflowRequest, current CommandResult, now time.Time) (Command, error) {
	if workflowGoalRank(request.Goal) < workflowGoalRank(api.WorkflowGoalTrackersProjected) {
		return nil, nil
	}
	stale, err := m.projectionCatalogStale(current)
	if err != nil || !stale {
		return nil, err
	}
	current.Projections = nil
	command, _ := planContinuationCommand(request, current, now)
	return command, nil
}
