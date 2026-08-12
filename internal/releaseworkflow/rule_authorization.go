// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

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
	cloned, err := instructionSnapshot.Clone()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow clone rule authorization instructions: %w", err)
	}
	if cloned.Instructions == nil {
		cloned.Instructions = make(map[api.TrackerID]api.TrackerProjectionInstructions)
	}
	instruction := cloned.Instructions[projection.TrackerID]
	instruction.AuthorizedRuleFingerprint = projection.WaivableRuleFingerprint
	cloned.Instructions[projection.TrackerID] = instruction
	authorizations := make(map[api.TrackerID]api.WorkflowFingerprint)
	for trackerID, candidate := range cloned.Instructions {
		if candidate.AuthorizedRuleFingerprint != "" {
			authorizations[trackerID] = candidate.AuthorizedRuleFingerprint
		}
	}
	return m.projectTrackersWithRuleAuthorizations(ctx, ownerID, state, nextRevision, now, ProjectTrackersCommand{
		WorkflowID:       workflow.ID,
		ExpectedRevision: workflow.Revision,
		TrackerIDs:       append([]api.TrackerID(nil), selection.TrackerIDs...),
		Instructions:     cloned.Instructions,
		ExecutionMode:    currentProjections.ExecutionMode,
	}, authorizations)
}

func trustedProjectionInstructions(
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
	authorizations map[api.TrackerID]api.WorkflowFingerprint,
) (map[api.TrackerID]api.TrackerProjectionInstructions, error) {
	cloned, err := (api.TrackerProjectionInstructionSnapshot{Instructions: instructions}).Clone()
	if err != nil {
		return nil, fmt.Errorf("clone projection instructions: %w", err)
	}
	for trackerID, instruction := range cloned.Instructions {
		instruction.AuthorizedRuleFingerprint = ""
		cloned.Instructions[trackerID] = instruction
	}
	cloned, err = cloned.Normalize()
	if err != nil {
		return nil, fmt.Errorf("normalize projection instructions: %w", err)
	}
	if cloned.Instructions == nil {
		cloned.Instructions = make(map[api.TrackerID]api.TrackerProjectionInstructions)
	}
	for trackerID, fingerprint := range authorizations {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if trackerID == "" || fingerprint == "" {
			continue
		}
		instruction := cloned.Instructions[trackerID]
		instruction.AuthorizedRuleFingerprint = fingerprint
		cloned.Instructions[trackerID] = instruction
	}
	cloned, err = cloned.Normalize()
	if err != nil {
		return nil, fmt.Errorf("normalize trusted rule authorization: %w", err)
	}
	return cloned.Instructions, nil
}
