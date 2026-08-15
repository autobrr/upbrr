// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// Continue advances at most one backend-owned transition toward a desired goal.
// Repeated calls plus Current polling are the complete adapter orchestration contract.
func (m *Module) Continue(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
) (CommandResult, error) {
	if err := request.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow continue: %w", err)
	}
	if request.Authority == nil {
		instructions := request.Intent.FactInstructions
		if instructions == nil && request.Intent.Preparation != nil {
			instructions = &request.Intent.Preparation.Instructions
		}
		created, err := m.Execute(ctx, ownerID, CreateWorkflowCommand{
			Instructions:        *instructions,
			IdempotencyKey:      continuationIdempotencyKey(request.IdempotencyKey, "create", 0),
			TrackerDecisionMode: trackerDecisionModeFromContext(ctx, TrackerDecisionModePostDupeGate),
		})
		if err != nil {
			return CommandResult{}, err
		}
		if err := m.acceptContinuationIntent(ctx, ownerID, created.Workflow.ID, request); err != nil {
			return CommandResult{}, err
		}
		return m.Current(ctx, ownerID, created.Workflow.ID)
	}

	authority := *request.Authority
	current, err := m.Current(ctx, ownerID, authority.WorkflowID)
	if err != nil {
		return CommandResult{}, err
	}
	if current.Workflow.Revision != authority.ExpectedRevision {
		return CommandResult{}, ErrRevisionConflict
	}
	state, err := m.repository.Load(ctx, ownerID, authority.WorkflowID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow continue load tracker decision policy: %w", err)
	}
	trackerDecisionMode := normalizeTrackerDecisionMode(state.TrackerDecisionMode)
	if err := m.acceptContinuationIntent(ctx, ownerID, authority.WorkflowID, request); err != nil {
		return CommandResult{}, err
	}
	if request.Approval != nil { //nolint:staticcheck // Reject retained v1 authority explicitly.
		return CommandResult{}, fmt.Errorf("%w: final upload approval is no longer accepted", ErrInvalidTransition)
	}
	if current.Operation != nil && !isTerminalProgressStatus(current.Operation.Status) {
		return current, nil
	}
	if current.Workflow.Status == api.WorkflowStatusCanceled {
		return current, nil
	}
	if request.Intent.Preparation != nil && request.Intent.Preparation.Force && current.Release != nil &&
		!continuationPreparationSatisfied(current.Release, request.Intent.Preparation) {
		operation, startErr := m.Start(ctx, ownerID, ResetReleaseCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Input:            *request.Intent.Preparation,
			IdempotencyKey: continuationIdempotencyKey(
				request.IdempotencyKey,
				"reprepare",
				current.Workflow.Revision,
			),
		})
		if startErr != nil {
			return CommandResult{}, fmt.Errorf("release workflow continue reprepare: %w", startErr)
		}
		return m.Current(ctx, ownerID, operation.WorkflowID)
	}

	if updated, handled, factsErr := m.reconcileContinuationFacts(ctx, ownerID, request, current); handled || factsErr != nil {
		return updated, factsErr
	}
	if updated, handled, answerErr := m.resolveContinuationAnswer(
		ctx,
		ownerID,
		request,
		current,
		trackerDecisionMode,
	); handled || answerErr != nil {
		return updated, answerErr
	}
	if request.TrackerApproval != nil {
		result, approveErr := m.Execute(ctx, ownerID, ApproveTrackersCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Approval:         *request.TrackerApproval,
			IdempotencyKey: continuationIdempotencyKey(
				request.IdempotencyKey,
				"approve-trackers",
				current.Workflow.Revision,
			),
		})
		if approveErr != nil {
			return CommandResult{}, approveErr
		}
		return m.Current(ctx, ownerID, result.Workflow.ID)
	}
	if slices.ContainsFunc(current.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionApproveTrackers && action.Status == api.RequiredActionStatusPending
	}) {
		return current, nil
	}
	if continuationGoalReached(current, request) {
		return current, nil
	}

	command, stage := planContinuationCommand(request, current, m.clock.Now().UTC())
	if command == nil {
		return current, nil
	}
	if decision, ok := command.(DecideDuplicatesCommand); ok {
		result, executeErr := m.Execute(ctx, ownerID, decision)
		if executeErr != nil {
			return CommandResult{}, executeErr
		}
		return m.Current(ctx, ownerID, result.Workflow.ID)
	}
	operation, err := m.Start(ctx, ownerID, command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow continue %s: %w", stage, err)
	}
	return m.Current(ctx, ownerID, operation.WorkflowID)
}

func (m *Module) acceptContinuationIntent(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	request api.ContinueReleaseWorkflowRequest,
) error {
	fingerprint, err := api.CanonicalWorkflowFingerprint(request)
	if err != nil {
		return fmt.Errorf("release workflow continue fingerprint accepted intent: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("release workflow continue encode accepted intent: %w", err)
	}
	acceptedRevision := api.WorkflowRevision(0)
	if request.Authority != nil {
		acceptedRevision = request.Authority.ExpectedRevision
	}
	_, _, err = m.durability.AcceptIntent(ctx, api.ReleaseWorkflowIntentRecord{
		OwnerID:            strings.TrimSpace(ownerID),
		WorkflowID:         workflowID,
		IdempotencyKey:     continuationIdempotencyKey(request.IdempotencyKey, "intent", acceptedRevision),
		RequestFingerprint: fingerprint,
		Goal:               request.Goal,
		IntentPayload:      payload,
		AcceptedAt:         m.clock.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("release workflow continue persist accepted intent: %w", err)
	}
	return nil
}

func (m *Module) resolveContinuationAnswer(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
	current CommandResult,
	trackerDecisionMode TrackerDecisionMode,
) (CommandResult, bool, error) {
	for _, answer := range request.Answers {
		if _, ok := releaseNameConfirmationAction(current.Projections, answer.ActionID); !ok {
			continue
		}
		result, err := m.Execute(ctx, ownerID, ResolveActionCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Answer:           answer,
			IdempotencyKey: continuationIdempotencyKey(
				request.IdempotencyKey,
				"answer-"+string(answer.ActionID),
				current.Workflow.Revision,
			),
		})
		if err != nil {
			return CommandResult{}, true, err
		}
		updated, err := m.Current(ctx, ownerID, result.Workflow.ID)
		return updated, true, err
	}
	for _, action := range current.Workflow.RequiredActions {
		if action.Status != api.RequiredActionStatusPending {
			continue
		}
		if continuationUnattendedSkipsTrackerAction(request.Intent, action) {
			continue
		}
		if action.Kind == api.RequiredActionReprepare && request.Intent.Preparation != nil {
			continue
		}
		if continuationIntentResolvesAction(request.Intent, current, action) {
			continue
		}
		answerIndex := slices.IndexFunc(request.Answers, func(answer api.RequiredActionAnswer) bool {
			return answer.ActionID == action.ID
		})
		if answerIndex < 0 {
			if request.Goal == api.WorkflowGoalDuplicatesDecided &&
				action.Kind == api.RequiredActionProvideTrackerInput &&
				trackerDupeReady(current.Projections, action.TrackerID) {
				continue
			}
			if action.Kind == api.RequiredActionReviewDuplicates &&
				continuationHasDupeDecisionUpdate(request.Intent, current.Dupes) {
				continue
			}
			if continuationActionBlocksAllLanesForMode(current, action, trackerDecisionMode) {
				return current, true, nil
			}
			continue
		}
		result, err := m.Execute(ctx, ownerID, ResolveActionCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Answer:           request.Answers[answerIndex],
			IdempotencyKey: continuationIdempotencyKey(
				request.IdempotencyKey,
				"answer-"+string(action.ID),
				current.Workflow.Revision,
			),
		})
		if err != nil {
			return CommandResult{}, true, err
		}
		updated, err := m.Current(ctx, ownerID, result.Workflow.ID)
		return updated, true, err
	}
	return CommandResult{}, false, nil
}

func trackerDupeReady(projections *api.TrackerReleaseProjectionSet, trackerID api.TrackerID) bool {
	return projections != nil && slices.ContainsFunc(projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.TrackerID == trackerID &&
			projection.Readiness == api.ReadinessStatusReady &&
			projection.DupeReady
	})
}

func continuationHasDupeDecisionUpdate(intent api.WorkflowIntent, dupes *api.DupeAssessment) bool {
	if dupes == nil {
		return false
	}
	return slices.ContainsFunc(dupes.Results, func(result api.TrackerDupeAssessment) bool {
		decision, exists := intent.DuplicateDecisions[result.TrackerID]
		return exists &&
			(decision == api.DupeDecisionAccepted || decision == api.DupeDecisionIgnored) &&
			decision != result.Decision
	})
}

func continuationIntentResolvesAction(intent api.WorkflowIntent, current CommandResult, action api.RequiredAction) bool {
	switch action.Kind {
	case api.RequiredActionReviewDuplicates:
		decision, ok := intent.DuplicateDecisions[action.TrackerID]
		return ok && decision != "" && decision != api.DupeDecisionPending
	case api.RequiredActionProvideTrackerInput:
		if action.TrackerID == "" && intent.Media != nil && current.Media != nil {
			return slices.ContainsFunc(current.Media.RequiredActions, func(mediaAction api.RequiredAction) bool {
				return mediaAction.ID == action.ID
			})
		}
		_, ok := intent.ProjectionInstructions[action.TrackerID]
		return ok
	case api.RequiredActionAnswerQuestionnaire:
		instruction, ok := intent.ProjectionInstructions[action.TrackerID]
		return ok && len(instruction.Questionnaire) > 0
	case api.RequiredActionSelectPlaylist, api.RequiredActionSelectMetadata, api.RequiredActionConfirmRescan,
		legacyTrackerAuthActionKind, legacyTrackerTwoFactorActionKind, api.RequiredActionAuthorizeRules,
		api.RequiredActionResolveTrackerPreparation,
		api.RequiredActionApproveTrackers,
		api.RequiredActionApproveUpload, //nolint:staticcheck // Retained v1 actions cannot be satisfied by desired intent.
		api.RequiredActionReprepare,
		api.RequiredActionReconcileSubmission:
		return false
	}
	return false
}

func continuationActionBlocksAllLanes(current CommandResult, action api.RequiredAction) bool {
	if action.TrackerID == "" {
		return true
	}
	return !slices.ContainsFunc(projectTrackerLaneOutcomes(current), func(lane api.TrackerLaneOutcome) bool {
		return lane.TrackerID != action.TrackerID && laneCanAdvance(lane)
	})
}

func continuationActionBlocksAllLanesForMode(
	current CommandResult,
	action api.RequiredAction,
	mode TrackerDecisionMode,
) bool {
	if _, _, projectionAuthorization := projectionRuleAuthorizationAction(current.Projections, action.ID); projectionAuthorization {
		return true
	}
	if normalizeTrackerDecisionMode(mode) == TrackerDecisionModePostDupeGate &&
		action.Kind == api.RequiredActionReviewDuplicates {
		return true
	}
	return continuationActionBlocksAllLanes(current, action)
}

func (m *Module) reconcileContinuationFacts(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
	current CommandResult,
) (CommandResult, bool, error) {
	desired := request.Intent.FactInstructions
	if desired == nil && request.Intent.Preparation != nil {
		desired = &request.Intent.Preparation.Instructions
	}
	if desired == nil || current.FactInstructions == nil {
		return CommandResult{}, false, nil
	}
	desiredFingerprint, err := api.CanonicalWorkflowFingerprint(*desired)
	if err != nil {
		return CommandResult{}, true, fmt.Errorf("release workflow continue fingerprint facts: %w", err)
	}
	if desiredFingerprint == current.FactInstructions.Fingerprint {
		return CommandResult{}, false, nil
	}
	result, err := m.Execute(ctx, ownerID, ReplaceFactInstructionsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     *desired,
		IdempotencyKey: continuationIdempotencyKey(
			request.IdempotencyKey,
			"replace-facts",
			current.Workflow.Revision,
		),
	})
	if err != nil {
		return CommandResult{}, true, err
	}
	updated, err := m.Current(ctx, ownerID, result.Workflow.ID)
	return updated, true, err
}

func planContinuationCommand(
	request api.ContinueReleaseWorkflowRequest,
	current CommandResult,
	now time.Time,
) (Command, string) {
	workflowID := current.Workflow.ID
	revision := current.Workflow.Revision
	key := func(stage string) string {
		return continuationIdempotencyKey(request.IdempotencyKey, stage, revision)
	}
	if current.Release == nil {
		if request.Intent.Preparation == nil {
			return nil, "prepare"
		}
		return PrepareReleaseCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			Input:            *request.Intent.Preparation,
			IdempotencyKey:   key("prepare"),
		}, "prepare"
	}
	if request.Intent.Preparation != nil && !continuationPreparationSatisfied(current.Release, request.Intent.Preparation) {
		return ResetReleaseCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			Input:            *request.Intent.Preparation,
			IdempotencyKey:   key("reprepare"),
		}, "reprepare"
	}
	if workflowGoalRank(request.Goal) <= workflowGoalRank(api.WorkflowGoalPrepared) {
		return nil, ""
	}
	if current.Projections == nil || !trackerIntentMatches(request.Intent, current) {
		trackerIDs := append([]api.TrackerID(nil), request.Intent.TrackerIDs...)
		if len(trackerIDs) == 0 && current.Selection != nil {
			trackerIDs = append(trackerIDs, current.Selection.TrackerIDs...)
		}
		return ProjectTrackersCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			TrackerIDs:       trackerIDs,
			Instructions:     request.Intent.ProjectionInstructions,
			ExecutionMode:    request.Intent.ExecutionMode,
			IdempotencyKey:   key("project-trackers"),
		}, "project-trackers"
	}
	interaction := continuationInteractionMode(request.Intent)
	if !continuationPreflightCurrent(current, now) ||
		(interaction == api.InteractionModeUnattended &&
			preflightRequiresManualAction(current.Preflight, current.Projections)) {
		return PreflightTrackersCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			InputFingerprint: current.Projections.InputFingerprint,
			Interaction:      interaction,
			ExecutionMode:    request.Intent.ExecutionMode,
			IdempotencyKey:   key("preflight-trackers"),
		}, "preflight-trackers"
	}
	if workflowGoalRank(request.Goal) <= workflowGoalRank(api.WorkflowGoalTrackersAssessed) {
		return nil, ""
	}
	if !slices.ContainsFunc(current.Projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.Readiness == api.ReadinessStatusReady && projection.DupeReady
	}) {
		return nil, "no-eligible-trackers"
	}
	requiredDuplicateChecks := normalizedDuplicateCheckOrdinal(request.Intent.DuplicateCheckCount)
	dupesCurrent := current.Dupes != nil && current.Dupes.ProjectionSet.ID == current.Projections.ID &&
		current.Dupes.ProjectionSet.Revision == current.Projections.Revision && current.Dupes.ExpiresAt.After(now)
	if !dupesCurrent || normalizedDuplicateCheckOrdinal(current.Dupes.CheckOrdinal) < requiredDuplicateChecks {
		checkOrdinal := uint8(1)
		if dupesCurrent {
			checkOrdinal = normalizedDuplicateCheckOrdinal(current.Dupes.CheckOrdinal) + 1
		}
		return CheckDuplicatesCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			SkipRemote:       request.Intent.SkipRemoteDuplicates,
			CheckOrdinal:     checkOrdinal,
			IdempotencyKey:   key("check-duplicates"),
		}, "check-duplicates"
	}
	decisions := make(map[api.TrackerID]api.DupeDecision)
	for _, result := range current.Dupes.Results {
		decision, ok := request.Intent.DuplicateDecisions[result.TrackerID]
		if !ok || decision == api.DupeDecisionPending || decision == "" || decision == result.Decision {
			continue
		}
		decisions[result.TrackerID] = decision
	}
	if len(decisions) > 0 {
		return DecideDuplicatesCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			Decisions:        decisions,
			IdempotencyKey:   key("decide-duplicates"),
		}, "decide-duplicates"
	}
	if !duplicateDecisionsComplete(current.Dupes) {
		if !dupesAllowContinuation(current.Projections, current.Dupes) {
			return nil, "decide-duplicates"
		}
	}
	if workflowGoalRank(request.Goal) <= workflowGoalRank(api.WorkflowGoalDuplicatesDecided) {
		return nil, ""
	}
	if !dupesAllowContinuation(current.Projections, current.Dupes) {
		return nil, ""
	}
	if current.Media == nil || !continuationMediaCaptureSatisfied(current, request.Intent.Media) {
		if request.Intent.Media == nil {
			return nil, "capture-media"
		}
		return CaptureMediaCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			Instructions:     *request.Intent.Media,
			IdempotencyKey:   key("capture-media"),
		}, "capture-media"
	}
	if !stageSucceeded(current.Media.Status) {
		return nil, "capture-media"
	}
	if workflowGoalRank(request.Goal) <= workflowGoalRank(api.WorkflowGoalMediaReady) {
		return nil, ""
	}
	if !mediaRequirementsPrepared(current.Media) {
		var artifactIDs []api.PublicResourceID
		if request.Intent.MediaSelection != nil {
			artifactIDs = append(artifactIDs, request.Intent.MediaSelection.ArtifactIDs...)
		}
		return UploadMediaImagesCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			Media:            api.MediaArtifactSetRef{ID: current.Media.ID, Revision: current.Media.Revision},
			ArtifactIDs:      artifactIDs,
			IdempotencyKey:   key("prepare-image-requirements"),
		}, "prepare-image-requirements"
	}
	if !descriptionsHaveViableTracker(current.Descriptions) {
		if request.Intent.Descriptions == nil {
			return nil, "generate-descriptions"
		}
		return GenerateDescriptionsCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			Instructions:     *request.Intent.Descriptions,
			IdempotencyKey:   key("generate-descriptions"),
		}, "generate-descriptions"
	}
	if workflowGoalRank(request.Goal) <= workflowGoalRank(api.WorkflowGoalDescriptionsReady) {
		return nil, ""
	}
	if current.DryRun == nil || current.DryRun.NoSeed != request.Intent.NoSeed ||
		(len(request.Intent.UploadTrackerIDs) > 0 &&
			!slices.Equal(
				normalizeContinuationTrackerIDs(current.DryRun.TrackerIDs),
				normalizeContinuationTrackerIDs(request.Intent.UploadTrackerIDs),
			)) {
		return DryRunUploadsCommand{
			WorkflowID:       workflowID,
			ExpectedRevision: revision,
			NoSeed:           request.Intent.NoSeed,
			TrackerIDs:       append([]api.TrackerID(nil), request.Intent.UploadTrackerIDs...),
			IdempotencyKey:   key("review-uploads"),
		}, "review-uploads"
	}
	if request.Goal != api.WorkflowGoalUploaded {
		return nil, ""
	}
	return ExecuteUploadsCommand{
		WorkflowID:       workflowID,
		ExpectedRevision: revision,
		NoSeed:           request.Intent.NoSeed,
		TrackerIDs:       append([]api.TrackerID(nil), request.Intent.UploadTrackerIDs...),
		Interaction:      continuationInteractionMode(request.Intent),
		IdempotencyKey:   key("execute-uploads"),
	}, "execute-uploads"
}

func continuationPreflightCurrent(current CommandResult, now time.Time) bool {
	if current.Preflight == nil || current.Projections == nil || !current.Preflight.ExpiresAt.After(now) {
		return false
	}
	if current.Projections.Preflight != nil {
		return current.Workflow.TrackerPreflight != nil &&
			*current.Projections.Preflight == *current.Workflow.TrackerPreflight &&
			current.Preflight.ID == current.Workflow.TrackerPreflight.ID &&
			current.Preflight.Revision == current.Workflow.TrackerPreflight.Revision
	}
	return current.Preflight.ProjectionSet.ID == current.Projections.ID &&
		current.Preflight.ProjectionSet.Revision == current.Projections.Revision
}

// continuationMediaCaptureSatisfied treats explicit final selections as
// satisfied only when each screenshot index is retained.
func continuationMediaCaptureSatisfied(current CommandResult, desired *api.MediaCaptureInstructions) bool {
	if desired == nil || current.Media == nil {
		return desired == nil && current.Media != nil
	}
	if (desired.Purpose == "" || desired.Purpose == api.ScreenshotPurposeFinal) && desired.Selections != nil {
		retained := make(map[int]struct{}, len(current.Media.Artifacts))
		for _, artifact := range current.Media.Artifacts {
			if artifact.Kind == api.MediaArtifactScreenshot {
				retained[artifact.Index] = struct{}{}
			}
		}
		for _, selection := range desired.Selections {
			if _, ok := retained[selection.Index]; !ok {
				return false
			}
		}
		return true
	}
	expected, err := api.CanonicalWorkflowFingerprint(struct {
		Release      api.ReleaseRef
		ProjectionID api.TrackerReleaseProjectionSetID
		Revision     api.WorkflowRevision
		Instructions api.MediaCaptureInstructions
		Requirements api.WorkflowFingerprint
	}{
		Release:      current.Projections.ReleaseRef,
		ProjectionID: current.Projections.ID,
		Revision:     current.Projections.Revision,
		Instructions: *desired,
		Requirements: current.Media.RequirementsFingerprint,
	})
	if err == nil && current.Media.CaptureFingerprint == expected {
		return true
	}
	var screenshots, automaticMenus int
	for _, artifact := range current.Media.Artifacts {
		if !artifact.Selected {
			continue
		}
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			screenshots++
		case api.MediaArtifactDVDMenu:
			if artifact.Source == api.ScreenshotSelectionSourceDVDMenu {
				automaticMenus++
			}
		case api.MediaArtifactHostedImage:
		}
	}
	switch desired.Purpose {
	case api.ScreenshotPurposeMenu:
		if !desired.CaptureDVDMenus {
			return true
		}
		return automaticMenus > 0 && stageSucceeded(current.Media.Status)
	case "", api.ScreenshotPurposeFinal:
		required := desired.ScreenshotCount
		if desired.Selections != nil {
			required = len(desired.Selections)
		}
		if required > 0 {
			return screenshots >= required
		}
		return stageSucceeded(current.Media.Status)
	case api.ScreenshotPurposePreview:
		return false
	}
	return false
}

func workflowGoalRank(goal api.WorkflowGoal) int {
	switch goal {
	case api.WorkflowGoalPrepared:
		return 1
	case api.WorkflowGoalTrackersAssessed:
		return 2
	case api.WorkflowGoalDuplicatesDecided:
		return 3
	case api.WorkflowGoalMediaReady:
		return 4
	case api.WorkflowGoalDescriptionsReady:
		return 5
	case api.WorkflowGoalUploadReviewed, api.WorkflowGoalDryRun:
		return 6
	case api.WorkflowGoalUploaded:
		return 7
	default:
		return 0
	}
}

func trackerIntentMatches(intent api.WorkflowIntent, current CommandResult) bool {
	if current.Selection == nil || current.ProjectionInstructions == nil {
		return false
	}
	if current.Projections == nil ||
		api.NormalizeWorkflowExecutionMode(current.Projections.ExecutionMode) != api.NormalizeWorkflowExecutionMode(intent.ExecutionMode) {
		return false
	}
	if len(intent.TrackerIDs) > 0 {
		desired := normalizeContinuationTrackerIDs(intent.TrackerIDs)
		retained := normalizeContinuationTrackerIDs(current.Selection.TrackerIDs)
		if !slices.Equal(desired, retained) {
			return false
		}
	}
	if intent.ProjectionInstructions != nil {
		desired, desiredErr := api.CanonicalWorkflowFingerprint(effectiveProjectionInstructions(intent.ProjectionInstructions))
		retained, retainedErr := api.CanonicalWorkflowFingerprint(effectiveProjectionInstructions(current.ProjectionInstructions.Instructions))
		if desiredErr != nil || retainedErr != nil || desired != retained {
			return false
		}
	}
	return true
}

func effectiveProjectionInstructions(
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
) map[api.TrackerID]api.TrackerProjectionInstructions {
	effective := make(map[api.TrackerID]api.TrackerProjectionInstructions, len(instructions))
	for trackerID, instruction := range instructions {
		if projectionInstructionIsEmpty(instruction) {
			continue
		}
		effective[trackerID] = instruction
	}
	return effective
}

func projectionInstructionIsEmpty(instruction api.TrackerProjectionInstructions) bool {
	return instruction.UploadReleaseName.IsZero() &&
		len(instruction.AdditionalNames) == 0 &&
		len(instruction.Questionnaire) == 0 &&
		instruction.TrackerConfig.Anon == nil &&
		instruction.TrackerConfig.Draft == nil &&
		instruction.TrackerConfig.ModQ == nil &&
		instruction.TrackerConfig.Channel == nil &&
		instruction.TrackerSite.TIK.Foreign == nil &&
		instruction.TrackerSite.TIK.Opera == nil &&
		instruction.TrackerSite.TIK.Asian == nil &&
		instruction.TrackerSite.TIK.DiscType == nil
}

func continuationInteractionMode(intent api.WorkflowIntent) api.InteractionMode {
	if intent.Interaction != "" {
		return intent.Interaction
	}
	if intent.Preparation != nil && intent.Preparation.Controls.Interaction != "" {
		return intent.Preparation.Controls.Interaction
	}
	return api.InteractionModeInteractive
}

func continuationUnattendedSkipsTrackerAction(intent api.WorkflowIntent, action api.RequiredAction) bool {
	if continuationInteractionMode(intent) != api.InteractionModeUnattended || action.TrackerID == "" {
		return false
	}
	switch action.Kind {
	case legacyTrackerAuthActionKind,
		legacyTrackerTwoFactorActionKind,
		api.RequiredActionProvideTrackerInput,
		api.RequiredActionAnswerQuestionnaire,
		api.RequiredActionAuthorizeRules,
		api.RequiredActionResolveTrackerPreparation:
		return true
	case api.RequiredActionSelectPlaylist,
		api.RequiredActionSelectMetadata,
		api.RequiredActionConfirmRescan,
		api.RequiredActionReviewDuplicates,
		api.RequiredActionApproveTrackers,
		api.RequiredActionApproveUpload, //nolint:staticcheck // Retained v1 action remains a global blocker.
		api.RequiredActionReprepare,
		api.RequiredActionReconcileSubmission:
		return false
	}
	return false
}

func preflightRequiresManualAction(
	preflight *api.TrackerPreflightAssessment,
	projections *api.TrackerReleaseProjectionSet,
) bool {
	return preflight != nil && slices.ContainsFunc(preflight.Results, func(result api.TrackerPreflightResult) bool {
		return slices.ContainsFunc(result.RequiredActions, func(action api.RequiredAction) bool {
			if action.Status != "" && action.Status != api.RequiredActionStatusPending {
				return false
			}
			return projections == nil || !slices.ContainsFunc(projections.Projections, func(projection api.TrackerReleaseProjection) bool {
				return slices.ContainsFunc(projection.RequiredActions, func(current api.RequiredAction) bool {
					return current.ID == action.ID && current.Status == api.RequiredActionStatusResolved
				})
			})
		})
	})
}

func normalizeContinuationTrackerIDs(values []api.TrackerID) []api.TrackerID {
	normalized := make([]api.TrackerID, 0, len(values))
	for _, value := range values {
		value = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(value))))
		if value != "" && !slices.Contains(normalized, value) {
			normalized = append(normalized, value)
		}
	}
	slices.Sort(normalized)
	return normalized
}

func continuationGoalReached(current CommandResult, request api.ContinueReleaseWorkflowRequest) bool {
	if continuationInteractionMode(request.Intent) == api.InteractionModeUnattended &&
		preflightRequiresManualAction(current.Preflight, current.Projections) {
		return false
	}

	switch request.Goal {
	case api.WorkflowGoalPrepared:
		return current.Release != nil && continuationPreparationSatisfied(current.Release, request.Intent.Preparation)
	case api.WorkflowGoalTrackersAssessed:
		return current.Preflight != nil && current.Projections != nil
	case api.WorkflowGoalDuplicatesDecided:
		return duplicateDecisionsComplete(current.Dupes) &&
			normalizedDuplicateCheckOrdinal(current.Dupes.CheckOrdinal) >= normalizedDuplicateCheckOrdinal(request.Intent.DuplicateCheckCount) &&
			duplicateIntentMatches(current.Dupes, request.Intent.DuplicateDecisions)
	case api.WorkflowGoalMediaReady:
		return current.Media != nil &&
			stageSucceeded(current.Media.Status) &&
			continuationMediaCaptureSatisfied(current, request.Intent.Media)
	case api.WorkflowGoalDescriptionsReady:
		return descriptionsHaveViableTracker(current.Descriptions)
	case api.WorkflowGoalUploadReviewed, api.WorkflowGoalDryRun:
		return current.DryRun != nil && current.DryRun.NoSeed == request.Intent.NoSeed &&
			(len(request.Intent.UploadTrackerIDs) == 0 ||
				slices.Equal(
					normalizeContinuationTrackerIDs(current.DryRun.TrackerIDs),
					normalizeContinuationTrackerIDs(request.Intent.UploadTrackerIDs),
				))
	case api.WorkflowGoalUploaded:
		return current.UploadResult != nil
	default:
		return false
	}
}

func continuationPreparationSatisfied(current *api.ReleaseSnapshot, desired *api.PrepareInput) bool {
	if current == nil {
		return false
	}
	if desired == nil {
		return true
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(*desired)
	return err == nil && current.PreparationFingerprint == fingerprint
}

func normalizedDuplicateCheckOrdinal(value uint8) uint8 {
	if value == 0 {
		return 1
	}
	return value
}

func duplicateIntentMatches(dupes *api.DupeAssessment, decisions map[api.TrackerID]api.DupeDecision) bool {
	for trackerID, decision := range decisions {
		if decision == "" || decision == api.DupeDecisionPending {
			continue
		}
		index := slices.IndexFunc(dupes.Results, func(result api.TrackerDupeAssessment) bool {
			return result.TrackerID == trackerID
		})
		if index < 0 || dupes.Results[index].Decision != decision {
			return false
		}
	}
	return true
}

func continuationIdempotencyKey(base, stage string, revision api.WorkflowRevision) string {
	return fmt.Sprintf("%s:%s:%d", strings.TrimSpace(base), stage, revision)
}

func duplicateDecisionsComplete(dupes *api.DupeAssessment) bool {
	if dupes == nil || !stageSucceeded(dupes.Status) {
		return false
	}
	return !slices.ContainsFunc(dupes.Results, func(result api.TrackerDupeAssessment) bool {
		return result.Decision == api.DupeDecisionPending
	})
}

func mediaRequirementsPrepared(media *api.MediaArtifactSet) bool {
	if media == nil || !stageSucceeded(media.Status) {
		return false
	}
	if media.ImageRequirementsPrepared {
		return true
	}
	return !slices.ContainsFunc(media.Artifacts, func(artifact api.MediaArtifact) bool {
		return artifact.Selected && (artifact.Kind == api.MediaArtifactScreenshot || artifact.Kind == api.MediaArtifactDVDMenu)
	})
}
