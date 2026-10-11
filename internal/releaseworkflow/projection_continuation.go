// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// continueTrackerProjection discovers local questions from the exact prepared
// generation. It never prepares, enriches, hydrates, or runs tracker preflight.
func (m *Module) continueTrackerProjection(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
	current CommandResult,
) (CommandResult, error) {
	if current.Release == nil || !continuationPreparationSatisfied(current.Release, request.Intent.Preparation) {
		return CommandResult{}, projectionPreparationRequired()
	}
	if current.FactInstructions != nil {
		currentCorrections, err := m.preparer.CorrectionsCurrent(ctx, current.Release.Release.Source.SourcePath, current.FactInstructions.CorrectionRevision)
		if err != nil {
			return CommandResult{}, fmt.Errorf("release workflow check projection corrections: %w", err)
		}
		if !currentCorrections {
			return CommandResult{}, projectionPreparationRequired()
		}
	}
	if checker, ok := m.trackerProjector.(TrackerProjectionCatalogChecker); ok && current.Projections != nil {
		catalogCurrent := false
		if current.Catalog != nil {
			var err error
			catalogCurrent, err = checker.CatalogCurrent(*current.Catalog)
			if err != nil {
				return CommandResult{}, fmt.Errorf("release workflow check projection catalog: %w", err)
			}
		}
		if !catalogCurrent {
			current.Projections = nil
		}
	}
	if continuationGoalReached(current, request) {
		// Even a retained projection must still have live exact-generation authority.
		_, err := m.preparer.ResolveUploadSubject(
			ctx,
			api.UploadSubjectInput{
				Release: api.ReleaseRef{SourcePath: current.Release.Release.Source.SourcePath, Generation: current.Release.Release.Generation},
			},
		)
		if err != nil {
			return CommandResult{}, fmt.Errorf("release workflow resolve current projection generation: %w", err)
		}
		return current, nil
	}
	command, _ := m.planContinuationCommand(request, current, m.clock.Now().UTC(), 0)
	if command == nil {
		return current, nil
	}
	operation, err := m.Start(ctx, ownerID, command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow continue tracker projection: %w", err)
	}
	return m.Current(ctx, ownerID, operation.WorkflowID)
}

// trackerProjectionGoalSatisfied accepts complete blocked-question sets, but
// filtered or stale lanes must be rebuilt before discovery is considered done.
func trackerProjectionGoalSatisfied(current CommandResult) bool {
	if current.Projections == nil || current.Selection == nil || current.Projections.Status == api.StageStatusStale {
		return false
	}
	expected := normalizeContinuationTrackerIDs(withoutConfirmedSubmissions(current.Selection.TrackerIDs, current.Workflow.SubmissionExclusions))
	if len(expected) == 0 || len(current.Projections.Projections) != len(expected) {
		return false
	}
	projected := make([]api.TrackerID, 0, len(current.Projections.Projections))
	for _, projection := range current.Projections.Projections {
		if projection.Readiness == api.ReadinessStatusStale {
			return false
		}
		projected = append(projected, projection.TrackerID)
	}
	return slices.Equal(normalizeContinuationTrackerIDs(projected), expected)
}

func projectionPreparationRequired() error {
	return api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureMissingPrerequisite,
		Operation: api.OperationKindDuplicateCheck,
		Message:   "Prepare the selected source before reviewing tracker questions.",
		Recovery:  api.OperationRecoveryCompletePrerequisite,
	}, ErrInvalidTransition)
}
