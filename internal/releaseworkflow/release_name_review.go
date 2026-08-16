// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

const releaseNameConfirmationDecisionCode = "release_name_confirmation"

func releaseNameConfirmationAction(
	projections *api.TrackerReleaseProjectionSet,
	actionID api.RequiredActionID,
) (api.RequiredAction, bool) {
	if projections == nil || actionID == "" {
		return api.RequiredAction{}, false
	}
	for _, projection := range projections.Projections {
		if !hasReleaseNameConfirmationDecision(projection) {
			continue
		}
		index := slices.IndexFunc(projection.RequiredActions, func(candidate api.RequiredAction) bool {
			return candidate.ID == actionID &&
				candidate.Kind == api.RequiredActionProvideTrackerInput &&
				candidate.TrackerID == projection.TrackerID
		})
		if index >= 0 {
			return projection.RequiredActions[index], true
		}
	}
	return api.RequiredAction{}, false
}

func hasReleaseNameConfirmationDecision(projection api.TrackerReleaseProjection) bool {
	return slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == releaseNameConfirmationDecisionCode &&
			(decision.Decision == "confirmation_required" || decision.Decision == "confirmed")
	})
}

func (m *Module) reviewTrackerReleaseName(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	action api.RequiredAction,
	answer api.RequiredActionAnswer,
) (CommandResult, error) {
	reviewedName, confirmed, err := validatedReleaseNameConfirmation(action, answer)
	if err != nil {
		return CommandResult{}, err
	}
	workflow := state.Workflow
	if m.trackerProjector == nil || workflow.Release == nil || workflow.TrackerCatalog == nil ||
		workflow.TrackerRuntime == nil || workflow.Selection == nil || workflow.ProjectionInstructions == nil ||
		workflow.TrackerProjections == nil || workflow.TrackerPreflight == nil || workflow.Dupes == nil {
		return CommandResult{}, fmt.Errorf("%w: tracker name review dependencies are incomplete", ErrInvalidTransition)
	}
	if confirmed && (workflow.TrackerApproval != nil || workflow.Media != nil || workflow.Descriptions != nil ||
		workflow.DryRun != nil || workflow.UploadResult != nil) {
		return CommandResult{}, fmt.Errorf("%w: tracker name must be reviewed before downstream preparation", ErrInvalidTransition)
	}
	release, releaseOK := state.Releases[workflow.Release.ID]
	catalog, catalogOK := state.Catalogs[workflow.TrackerCatalog.ID]
	runtime, runtimeOK := state.Runtimes[workflow.TrackerRuntime.ID]
	selection, selectionOK := state.Selections[workflow.Selection.ID]
	instructionSnapshot, instructionsOK := state.ProjectionInstructions[workflow.ProjectionInstructions.ID]
	currentProjections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	currentDupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	if !releaseOK || !catalogOK || !runtimeOK || !selectionOK || !instructionsOK || !projectionsOK || !dupesOK ||
		release.Revision != workflow.Release.Revision ||
		catalog.Revision != workflow.TrackerCatalog.Revision ||
		runtime.Revision != workflow.TrackerRuntime.Revision ||
		selection.Revision != workflow.Selection.Revision ||
		instructionSnapshot.Revision != workflow.ProjectionInstructions.Revision ||
		currentProjections.Revision != workflow.TrackerProjections.Revision ||
		currentDupes.Revision != workflow.Dupes.Revision ||
		currentProjections.Preflight == nil || *currentProjections.Preflight != *workflow.TrackerPreflight ||
		currentDupes.ProjectionSet != *workflow.TrackerProjections ||
		!currentDupes.ExpiresAt.After(now) {
		return CommandResult{}, fmt.Errorf("%w: tracker name review dependencies are stale", ErrInvalidTransition)
	}

	instructionSnapshot, err = instructionSnapshot.Clone()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow clone tracker projection instructions: %w", err)
	}
	if instructionSnapshot.Instructions == nil {
		instructionSnapshot.Instructions = make(map[api.TrackerID]api.TrackerProjectionInstructions)
	}
	instruction := instructionSnapshot.Instructions[action.TrackerID]
	if confirmed {
		instruction.UploadReleaseName = api.WorkflowPatch[string]{Present: true, Value: reviewedName}
	} else {
		instruction.UploadReleaseName = api.WorkflowPatch[string]{}
	}
	instructionSnapshot.Instructions[action.TrackerID] = instruction

	trackerNames := make([]string, len(selection.TrackerIDs))
	for index, trackerID := range selection.TrackerIDs {
		trackerNames[index] = string(trackerID)
	}
	subject, err := m.preparer.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:  currentProjections.ReleaseRef,
		Trackers: trackerNames,
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow resolve reviewed tracker name subject: %w", err)
	}
	projectionContext := logging.WithOperationLogger(ctx, api.NopLogger{})
	rebuiltCatalog, _, rebuiltSelection, rebuiltProjections, err := m.trackerProjector.Build(
		projectionContext,
		release,
		subject,
		selection.TrackerIDs,
		instructionSnapshot.Instructions,
		projectionRuleAuthorizations(currentProjections),
		currentProjections.ExecutionMode,
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow rebuild reviewed tracker name: %w", err)
	}
	if rebuiltCatalog.Fingerprint != catalog.Fingerprint ||
		!slices.Equal(rebuiltSelection.TrackerIDs, selection.TrackerIDs) {
		return CommandResult{}, fmt.Errorf("%w: tracker catalog or selection changed during name review", ErrInvalidTransition)
	}

	finalized, err := mergeReviewedReleaseNameProjections(
		currentProjections,
		rebuiltProjections,
		action.TrackerID,
		confirmed,
	)
	if err != nil {
		return CommandResult{}, err
	}
	instructionID, err := m.newID("projection_instructions")
	if err != nil {
		return CommandResult{}, err
	}
	instructionSnapshot.ID = api.TrackerProjectionInstructionSnapshotID(instructionID)
	instructionSnapshot.WorkflowID = workflow.ID
	instructionSnapshot.Revision = nextRevision
	instructionSnapshot.CreatedAt = now
	instructionSnapshot, err = instructionSnapshot.WithFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow reviewed tracker projection instructions: %w", err)
	}
	if err := instructionSnapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow reviewed tracker projection instructions: %w", err)
	}
	instructionRef := api.TrackerProjectionInstructionSnapshotRef{
		ID:       instructionSnapshot.ID,
		Revision: instructionSnapshot.Revision,
	}

	projectionID, err := m.newID("projections")
	if err != nil {
		return CommandResult{}, err
	}
	finalized.ID = api.TrackerReleaseProjectionSetID(projectionID)
	finalized.WorkflowID = workflow.ID
	finalized.Revision = nextRevision
	finalized.Instructions = &instructionRef
	finalized.CreatedAt = now
	if err := m.stampProjectionActions(&finalized, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	finalized.Status = finalizedProjectionStatus(finalized.Projections, finalized.RequiredActions, finalized.Failures)
	if err := finalized.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish reviewed tracker name: %w", err)
	}

	rebasedDupes, err := rebaseDupesForReviewedNames(currentDupes, finalized)
	if err != nil {
		return CommandResult{}, err
	}
	if err := m.stampDupeActions(&rebasedDupes, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	if err := validateDupeBuild(finalized, rebasedDupes); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow retain duplicate assessment after name review: %w", err)
	}
	privateEvidence, privateErr := m.private.Get(
		ownerID,
		workflow.ID,
		dupePrivateResourceID(currentDupes.ID),
		now,
	)
	if privateErr != nil && !errors.Is(privateErr, ErrPrivateResourceUnavailable) {
		return CommandResult{}, fmt.Errorf("release workflow load duplicate evidence for name review: %w", privateErr)
	}

	state.ProjectionInstructions[instructionSnapshot.ID] = instructionSnapshot
	state.Projections[finalized.ID] = finalized
	state.Workflow.ProjectionInstructions = &instructionRef
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{
		ID:       finalized.ID,
		Revision: finalized.Revision,
	}
	result, err := m.publishDupes(state, nextRevision, now, dupeAssessmentPublication{Snapshot: rebasedDupes})
	if err != nil {
		return CommandResult{}, err
	}
	if privateErr == nil && result.Dupes != nil {
		if err := m.private.Put(
			ownerID,
			workflow.ID,
			dupePrivateResourceID(result.Dupes.ID),
			privateEvidence,
			result.Dupes.ExpiresAt,
		); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow retain duplicate evidence after name review: %w", err)
		}
	}
	result.ProjectionInstructions = &instructionSnapshot
	result.Projections = &finalized
	return result, nil
}

func validatedReleaseNameConfirmation(
	action api.RequiredAction,
	answer api.RequiredActionAnswer,
) (string, bool, error) {
	if answer.Confirmed == nil || len(answer.SelectedValues) != 0 {
		return "", false, fmt.Errorf("%w: tracker name review requires an exact confirmation state", ErrInvalidTransition)
	}
	if !*answer.Confirmed {
		if action.Status != api.RequiredActionStatusResolved || answer.TextValue != nil {
			return "", false, fmt.Errorf("%w: only a resolved tracker name can be unconfirmed", ErrInvalidTransition)
		}
		return "", false, nil
	}
	if action.Status != api.RequiredActionStatusPending || answer.TextValue == nil {
		return "", false, fmt.Errorf("%w: tracker name review requires pending confirmed free text", ErrInvalidTransition)
	}
	value := strings.TrimSpace(*answer.TextValue)
	if value == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", false, fmt.Errorf("%w: reviewed tracker name is empty or contains control characters", ErrInvalidTransition)
	}
	return value, true, nil
}

func mergeReviewedReleaseNameProjections(
	current api.TrackerReleaseProjectionSet,
	rebuilt api.TrackerReleaseProjectionSet,
	trackerID api.TrackerID,
	confirmed bool,
) (api.TrackerReleaseProjectionSet, error) {
	if len(current.Projections) != len(rebuilt.Projections) {
		return api.TrackerReleaseProjectionSet{}, fmt.Errorf("%w: tracker projections changed during name review", ErrInvalidTransition)
	}
	rebuiltByTracker := make(map[api.TrackerID]api.TrackerReleaseProjection, len(rebuilt.Projections))
	for _, projection := range rebuilt.Projections {
		rebuiltByTracker[projection.TrackerID] = projection
	}
	finalized := current
	finalized.InputFingerprint = rebuilt.InputFingerprint
	finalized.PolicyFingerprint = rebuilt.PolicyFingerprint
	finalized.Projections = make([]api.TrackerReleaseProjection, 0, len(current.Projections))
	reviewed := false
	for _, previous := range current.Projections {
		next, ok := rebuiltByTracker[previous.TrackerID]
		if !ok {
			return api.TrackerReleaseProjectionSet{}, fmt.Errorf(
				"%w: tracker %s disappeared during name review",
				ErrInvalidTransition,
				previous.TrackerID,
			)
		}
		if !duplicateSemanticsEqual(previous, next) ||
			previous.CatalogFingerprint != next.CatalogFingerprint ||
			previous.ConfigFingerprint != next.ConfigFingerprint {
			return api.TrackerReleaseProjectionSet{}, fmt.Errorf(
				"%w: duplicate semantics changed for tracker %s during name review",
				ErrInvalidTransition,
				previous.TrackerID,
			)
		}
		if previous.TrackerID != trackerID {
			if previous.ProjectorFingerprint != next.ProjectorFingerprint {
				return api.TrackerReleaseProjectionSet{}, fmt.Errorf(
					"%w: unrelated tracker %s changed during name review",
					ErrInvalidTransition,
					previous.TrackerID,
				)
			}
			previous.InputFingerprint = next.InputFingerprint
			finalized.Projections = append(finalized.Projections, previous)
			continue
		}
		reviewed = true
		if previous.Readiness != api.ReadinessStatusReady || !previous.DupeReady ||
			next.Readiness != api.ReadinessStatusReady || !next.DupeReady ||
			len(previous.Failures) != 0 || len(next.Failures) != 0 {
			return api.TrackerReleaseProjectionSet{}, fmt.Errorf(
				"%w: tracker %s is not eligible for release-name review",
				ErrInvalidTransition,
				trackerID,
			)
		}
		confirmationAction, ok := releaseNameConfirmationActionForProjection(previous)
		if !ok ||
			(confirmed && (previous.UploadReady || !next.UploadReady ||
				confirmationAction.Status != api.RequiredActionStatusPending)) ||
			(!confirmed && (!previous.UploadReady || next.UploadReady ||
				confirmationAction.Status != api.RequiredActionStatusResolved)) {
			return api.TrackerReleaseProjectionSet{}, fmt.Errorf(
				"%w: tracker %s release-name confirmation state is stale",
				ErrInvalidTransition,
				trackerID,
			)
		}
		if confirmed {
			confirmationAction.Status = api.RequiredActionStatusResolved
			confirmationAction.Options = []api.RequiredActionOption{{Value: next.UploadReleaseName, Label: next.UploadReleaseName}}
			next.RequiredActions = append(next.RequiredActions, confirmationAction)
		} else {
			actionIndex := slices.IndexFunc(next.RequiredActions, func(candidate api.RequiredAction) bool {
				return candidate.Kind == api.RequiredActionProvideTrackerInput &&
					candidate.TrackerID == trackerID
			})
			if actionIndex < 0 {
				return api.TrackerReleaseProjectionSet{}, fmt.Errorf(
					"%w: tracker %s did not restore release-name confirmation",
					ErrInvalidTransition,
					trackerID,
				)
			}
			next.RequiredActions[actionIndex].ID = confirmationAction.ID
		}
		for _, decision := range previous.PolicyDecisions {
			if decision.Code == releaseNameConfirmationDecisionCode || slices.Contains(next.PolicyDecisions, decision) {
				continue
			}
			next.PolicyDecisions = append(next.PolicyDecisions, decision)
		}
		finalized.Projections = append(finalized.Projections, next)
	}
	if !reviewed {
		return api.TrackerReleaseProjectionSet{}, fmt.Errorf("%w: tracker name review target is absent", ErrInvalidTransition)
	}
	finalized.RequiredActions = nil
	for _, projection := range finalized.Projections {
		finalized.RequiredActions = append(finalized.RequiredActions, projection.RequiredActions...)
	}
	finalized.Failures = append([]api.WorkflowFailure(nil), current.Failures...)
	return finalized, nil
}

func releaseNameConfirmationActionForProjection(
	projection api.TrackerReleaseProjection,
) (api.RequiredAction, bool) {
	index := slices.IndexFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionProvideTrackerInput &&
			action.TrackerID == projection.TrackerID
	})
	if index < 0 || !hasReleaseNameConfirmationDecision(projection) {
		return api.RequiredAction{}, false
	}
	return projection.RequiredActions[index], true
}

func duplicateSemanticsEqual(left, right api.TrackerReleaseProjection) bool {
	return left.CriteriaFingerprint == right.CriteriaFingerprint &&
		left.DuplicateTargetFingerprint == right.DuplicateTargetFingerprint &&
		left.DuplicateSearchFingerprint == right.DuplicateSearchFingerprint &&
		left.DuplicatePolicyID == right.DuplicatePolicyID &&
		left.DuplicatePolicyFingerprint == right.DuplicatePolicyFingerprint
}

func rebaseDupesForReviewedNames(
	current api.DupeAssessment,
	projections api.TrackerReleaseProjectionSet,
) (api.DupeAssessment, error) {
	rebased, err := current.Clone()
	if err != nil {
		return api.DupeAssessment{}, fmt.Errorf("release workflow clone duplicate assessment for name review: %w", err)
	}
	projectionByTracker := make(map[api.TrackerID]api.TrackerReleaseProjection, len(projections.Projections))
	for _, projection := range projections.Projections {
		projectionByTracker[projection.TrackerID] = projection
	}
	for index := range rebased.Results {
		result := &rebased.Results[index]
		projection, ok := projectionByTracker[result.TrackerID]
		if !ok {
			return api.DupeAssessment{}, fmt.Errorf(
				"%w: duplicate result tracker %s is absent after name review",
				ErrInvalidTransition,
				result.TrackerID,
			)
		}
		fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
		if err != nil {
			return api.DupeAssessment{}, fmt.Errorf("fingerprint reviewed tracker projection %s: %w", projection.TrackerID, err)
		}
		result.UploadReleaseName = projection.UploadReleaseName
		result.ProjectionFingerprint = fingerprint
	}
	rebased.ProjectionSet = api.TrackerReleaseProjectionSetRef{
		ID:       projections.ID,
		Revision: projections.Revision,
	}
	return rebased, nil
}
