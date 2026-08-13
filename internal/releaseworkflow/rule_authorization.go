// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// projectionRuleAuthorizationAction finds a pending authorization action bound
// to a projection whose current waivable rules remain unauthorized.
func projectionRuleAuthorizationAction(
	projections *api.TrackerReleaseProjectionSet,
	actionID api.RequiredActionID,
) (api.TrackerReleaseProjection, api.RequiredAction, bool) {
	if projections == nil || actionID == "" {
		return api.TrackerReleaseProjection{}, api.RequiredAction{}, false
	}
	for _, projection := range projections.Projections {
		if projection.WaivableRuleFingerprint == "" || projection.RuleAuthorizationFingerprint != "" {
			continue
		}
		index := slices.IndexFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
			return action.ID == actionID &&
				action.Kind == api.RequiredActionAuthorizeRules &&
				action.TrackerID == projection.TrackerID &&
				action.Status == api.RequiredActionStatusPending
		})
		if index >= 0 {
			return projection, projection.RequiredActions[index], true
		}
	}
	return api.TrackerReleaseProjection{}, api.RequiredAction{}, false
}

// authorizeTrackerRules validates explicit confirmation and current snapshot
// lineage, then reprojects with server-held authority for the accepted rules.
func (m *Module) authorizeTrackerRules(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	projection api.TrackerReleaseProjection,
	action api.RequiredAction,
	answer api.RequiredActionAnswer,
) (CommandResult, error) {
	if answer.Confirmed == nil || !*answer.Confirmed || answer.TextValue != nil || len(answer.SelectedValues) > 0 {
		return CommandResult{}, fmt.Errorf("%w: rule authorization requires explicit confirmation", ErrInvalidTransition)
	}
	workflow := state.Workflow
	if workflow.Selection == nil || workflow.ProjectionInstructions == nil || workflow.TrackerProjections == nil {
		return CommandResult{}, fmt.Errorf("%w: rule authorization dependencies are incomplete", ErrInvalidTransition)
	}
	selection, selectionOK := state.Selections[workflow.Selection.ID]
	instructionSnapshot, instructionsOK := state.ProjectionInstructions[workflow.ProjectionInstructions.ID]
	currentProjections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	workflowActionIndex := slices.IndexFunc(workflow.RequiredActions, func(candidate api.RequiredAction) bool {
		return candidate.ID == action.ID &&
			candidate.Kind == api.RequiredActionAuthorizeRules &&
			candidate.TrackerID == projection.TrackerID &&
			candidate.Status == api.RequiredActionStatusPending
	})
	if !selectionOK || !instructionsOK || !projectionsOK ||
		selection.Revision != workflow.Selection.Revision ||
		instructionSnapshot.Revision != workflow.ProjectionInstructions.Revision ||
		currentProjections.Revision != workflow.TrackerProjections.Revision ||
		currentProjections.Instructions == nil || *currentProjections.Instructions != *workflow.ProjectionInstructions ||
		workflowActionIndex < 0 || workflow.RequiredActions[workflowActionIndex].WorkflowRevision != workflow.Revision ||
		action.TrackerID != projection.TrackerID {
		return CommandResult{}, fmt.Errorf("%w: rule authorization dependencies are stale", ErrInvalidTransition)
	}
	authorizations := projectionRuleAuthorizations(currentProjections)
	authorizations[projection.TrackerID] = projection.WaivableRuleFingerprint
	m.logger.Infof("release workflow: accepted tracker rule authorization tracker=%s decision=authorized", projection.TrackerID)
	return m.projectTrackersWithRuleAuthorizations(ctx, ownerID, state, nextRevision, now, ProjectTrackersCommand{
		WorkflowID:       workflow.ID,
		ExpectedRevision: workflow.Revision,
		TrackerIDs:       append([]api.TrackerID(nil), selection.TrackerIDs...),
		Instructions:     instructionSnapshot.Instructions,
		ExecutionMode:    currentProjections.ExecutionMode,
	}, authorizations)
}

// projectionRuleAuthorizations returns only recorded authorizations that still
// match their projection's current waivable rules.
func projectionRuleAuthorizations(projections api.TrackerReleaseProjectionSet) map[api.TrackerID]api.WorkflowFingerprint {
	authorizations := make(map[api.TrackerID]api.WorkflowFingerprint, len(projections.Projections))
	for _, projection := range projections.Projections {
		if projection.RuleAuthorizationFingerprint != "" &&
			projection.RuleAuthorizationFingerprint == projection.WaivableRuleFingerprint {
			authorizations[projection.TrackerID] = projection.RuleAuthorizationFingerprint
		}
	}
	return authorizations
}
