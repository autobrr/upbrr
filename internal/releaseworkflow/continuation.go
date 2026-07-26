// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	goalReasonAvailable            = "available"
	goalReasonOperationActive      = "operation_active"
	goalReasonWorkflowCanceled     = "workflow_canceled"
	goalReasonWorkflowComplete     = "workflow_complete"
	goalReasonPreparationRequired  = "preparation_required"
	goalReasonTrackersRequired     = "trackers_required"
	goalReasonMediaRequired        = "media_required"
	goalReasonDescriptionsRequired = "descriptions_required"
)

func projectWorkflowContinuation(current CommandResult) api.WorkflowContinuation {
	return projectWorkflowContinuationForState(current, nil, time.Time{})
}

func projectWorkflowContinuationForState(
	current CommandResult,
	state *State,
	now time.Time,
) api.WorkflowContinuation {
	lanes := projectTrackerLaneOutcomes(current)
	lifecycle, disposition := reduceWorkflowOutcome(current, lanes)
	actions := projectContinuationRequiredActions(current, state, now)
	if len(actions) > len(current.Workflow.RequiredActions) && lifecycle != api.OperationLifecycleQueued &&
		lifecycle != api.OperationLifecycleRunning {
		lifecycle = api.OperationLifecycleWaiting
		if disposition != api.WorkflowDispositionPartial {
			disposition = api.WorkflowDispositionNeedsAction
		}
	}
	return api.WorkflowContinuation{
		Lifecycle:       lifecycle,
		Disposition:     disposition,
		Refs:            exactRefs(current.Workflow),
		TrackerOutcomes: lanes,
		RequiredActions: actions,
		AvailableGoals:  projectAvailableGoals(current),
		Events:          projectWorkflowEvents(current),
	}
}

func projectContinuationRequiredActions(
	current CommandResult,
	state *State,
	now time.Time,
) []api.RequiredAction {
	actions := append([]api.RequiredAction(nil), current.Workflow.RequiredActions...)
	if state == nil {
		return actions
	}
	action, _, err := projectedTrackerApprovalAction(state, now)
	if err != nil || action == nil ||
		slices.ContainsFunc(actions, func(candidate api.RequiredAction) bool { return candidate.ID == action.ID }) {
		return actions
	}
	actions = append(actions, *action)
	return actions
}

func projectTrackerLaneOutcomes(current CommandResult) []api.TrackerLaneOutcome {
	if current.Projections == nil {
		return nil
	}
	refs := exactRefs(current.Workflow)
	lanes := make([]api.TrackerLaneOutcome, 0, len(current.Projections.Projections))
	index := make(map[api.TrackerID]int, len(current.Projections.Projections))
	for _, projection := range current.Projections.Projections {
		lane := api.TrackerLaneOutcome{
			TrackerID:   projection.TrackerID,
			DisplayName: projection.DisplayName,
			Goal:        api.WorkflowGoalTrackersAssessed,
			Lifecycle:   api.OperationLifecycleReady,
			Disposition: api.WorkflowDispositionNone,
			Refs:        refs,
		}
		applyLaneEvidence(&lane, projection.RequiredActions, projection.Failures)
		if projection.Readiness == api.ReadinessStatusIneligible || projection.Readiness == api.ReadinessStatusStale {
			lane.Lifecycle = api.OperationLifecycleTerminal
			lane.Disposition = api.WorkflowDispositionFailed
		}
		index[lane.TrackerID] = len(lanes)
		lanes = append(lanes, lane)
	}

	if current.Preflight != nil {
		for _, result := range current.Preflight.Results {
			lane := trackerLane(lanes, index, result.TrackerID)
			if lane == nil {
				continue
			}
			applyLaneEvidence(lane, result.RequiredActions, result.Failures)
			switch result.State {
			case api.TrackerPreflightStateReady:
				advanceLane(lane, api.WorkflowGoalTrackersAssessed)
			case api.TrackerPreflightStateActionRequired:
				lane.Lifecycle = api.OperationLifecycleWaiting
				lane.Disposition = api.WorkflowDispositionNeedsAction
			case api.TrackerPreflightStateRetryable, api.TrackerPreflightStateExpired:
				lane.Lifecycle = api.OperationLifecycleTerminal
				lane.Disposition = api.WorkflowDispositionFailed
				lane.Retryable = true
			case api.TrackerPreflightStateFailed:
				lane.Lifecycle = api.OperationLifecycleTerminal
				lane.Disposition = api.WorkflowDispositionFailed
			}
		}
	}

	if current.Dupes != nil {
		for _, result := range current.Dupes.Results {
			lane := trackerLane(lanes, index, result.TrackerID)
			if lane == nil {
				continue
			}
			applyLaneEvidence(lane, result.RequiredActions, result.Failures)
			switch {
			case result.Decision == api.DupeDecisionPending || len(result.RequiredActions) > 0:
				lane.Lifecycle = api.OperationLifecycleWaiting
				lane.Disposition = api.WorkflowDispositionNeedsAction
			case result.Decision == api.DupeDecisionAccepted:
				lane.Lifecycle = api.OperationLifecycleTerminal
				lane.Disposition = api.WorkflowDispositionFailed
			case stageSucceeded(result.Status):
				advanceLane(lane, api.WorkflowGoalDuplicatesDecided)
			case stageFailed(result.Status):
				lane.Lifecycle = api.OperationLifecycleTerminal
				lane.Disposition = api.WorkflowDispositionFailed
				lane.Retryable = true
			}
		}
	}

	if current.Media != nil && stageSucceeded(current.Media.Status) {
		for laneIndex := range lanes {
			if laneCanAdvance(lanes[laneIndex]) {
				advanceLane(&lanes[laneIndex], api.WorkflowGoalMediaReady)
			}
		}
		applyTrackerFailures(lanes, index, current.Media.Failures)
	}

	if current.Descriptions != nil {
		seen := make(map[api.TrackerID]struct{}, len(current.Descriptions.TrackerResults))
		for _, result := range current.Descriptions.TrackerResults {
			seen[result.TrackerID] = struct{}{}
			lane := trackerLane(lanes, index, result.TrackerID)
			if lane == nil {
				continue
			}
			if stageSucceeded(result.Status) {
				advanceLane(lane, api.WorkflowGoalDescriptionsReady)
			} else if stageFailed(result.Status) {
				lane.Lifecycle = api.OperationLifecycleTerminal
				lane.Disposition = api.WorkflowDispositionFailed
				lane.Retryable = true
			}
		}
		for laneIndex := range lanes {
			if _, ok := seen[lanes[laneIndex].TrackerID]; ok || !laneCanAdvance(lanes[laneIndex]) {
				continue
			}
			if projectionRequiresDescription(current.Projections, lanes[laneIndex].TrackerID) {
				continue
			}
			advanceLane(&lanes[laneIndex], api.WorkflowGoalDescriptionsReady)
		}
		applyTrackerFailures(lanes, index, current.Descriptions.Failures)
	}

	if current.DryRun != nil {
		for _, report := range current.DryRun.Reports {
			lane := trackerLane(lanes, index, report.TrackerID)
			if lane == nil {
				continue
			}
			lane.Failures = append(lane.Failures, report.Failures...)
			if stageSucceeded(report.Status) && stageSucceeded(report.ClientInjection.Status) {
				advanceLane(lane, api.WorkflowGoalDryRun)
				lane.Disposition = api.WorkflowDispositionSucceeded
			} else {
				lane.Lifecycle = api.OperationLifecycleTerminal
				lane.Disposition = api.WorkflowDispositionFailed
				lane.Retryable = true
			}
		}
	}

	if current.UploadResult != nil {
		for _, result := range current.UploadResult.Results {
			lane := trackerLane(lanes, index, result.TrackerID)
			if lane == nil {
				continue
			}
			lane.Failures = append(lane.Failures, result.Failures...)
			lane.Lifecycle = api.OperationLifecycleTerminal
			if stageSucceeded(result.Status) {
				lane.Goal = api.WorkflowGoalUploaded
				lane.Disposition = api.WorkflowDispositionSucceeded
			} else {
				lane.Disposition = api.WorkflowDispositionFailed
				lane.Retryable = true
			}
		}
	}

	applyOperationItems(lanes, index, current.Operation)
	return lanes
}

func exactRefs(workflow api.ReleaseWorkflow) api.WorkflowExactRefs {
	return api.WorkflowExactRefs{
		Release:         workflow.Release,
		Projections:     workflow.TrackerProjections,
		Preflight:       workflow.TrackerPreflight,
		Dupes:           workflow.Dupes,
		TrackerApproval: workflow.TrackerApproval,
		Media:           workflow.Media,
		Descriptions:    workflow.Descriptions,
		DryRun:          workflow.DryRun,
		UploadResult:    workflow.UploadResult,
	}
}

func trackerLane(
	lanes []api.TrackerLaneOutcome,
	index map[api.TrackerID]int,
	trackerID api.TrackerID,
) *api.TrackerLaneOutcome {
	laneIndex, ok := index[trackerID]
	if !ok {
		return nil
	}
	return &lanes[laneIndex]
}

func laneCanAdvance(lane api.TrackerLaneOutcome) bool {
	return lane.Disposition != api.WorkflowDispositionFailed &&
		lane.Disposition != api.WorkflowDispositionCanceled &&
		lane.Disposition != api.WorkflowDispositionNeedsAction
}

func advanceLane(lane *api.TrackerLaneOutcome, goal api.WorkflowGoal) {
	if !laneCanAdvance(*lane) {
		return
	}
	lane.Goal = goal
	lane.Lifecycle = api.OperationLifecycleReady
	lane.Disposition = api.WorkflowDispositionNone
	lane.RequiredActions = nil
}

func applyLaneEvidence(lane *api.TrackerLaneOutcome, actions []api.RequiredAction, failures []api.WorkflowFailure) {
	lane.RequiredActions = append(lane.RequiredActions, actions...)
	lane.Failures = append(lane.Failures, failures...)
	if len(actions) > 0 {
		lane.Lifecycle = api.OperationLifecycleWaiting
		lane.Disposition = api.WorkflowDispositionNeedsAction
		return
	}
	if len(failures) > 0 {
		lane.Lifecycle = api.OperationLifecycleTerminal
		lane.Disposition = api.WorkflowDispositionFailed
		lane.Retryable = slices.ContainsFunc(failures, func(failure api.WorkflowFailure) bool {
			return failure.Failure.Recovery == api.OperationRecoveryRetry ||
				failure.Failure.Recovery == api.OperationRecoveryAuthenticateTrackers ||
				failure.Failure.Recovery == api.OperationRecoveryRefreshRelease
		})
	}
}

func applyTrackerFailures(
	lanes []api.TrackerLaneOutcome,
	index map[api.TrackerID]int,
	failures []api.WorkflowFailure,
) {
	for _, failure := range failures {
		if failure.TrackerID == "" {
			continue
		}
		lane := trackerLane(lanes, index, failure.TrackerID)
		if lane == nil {
			continue
		}
		applyLaneEvidence(lane, nil, []api.WorkflowFailure{failure})
	}
}

func projectionRequiresDescription(projections *api.TrackerReleaseProjectionSet, trackerID api.TrackerID) bool {
	if projections == nil {
		return false
	}
	for _, projection := range projections.Projections {
		if projection.TrackerID == trackerID {
			return projection.Artifacts.Description
		}
	}
	return false
}

func applyOperationItems(
	lanes []api.TrackerLaneOutcome,
	index map[api.TrackerID]int,
	operation *api.WorkflowOperationStatus,
) {
	if operation == nil {
		return
	}
	for _, item := range operation.Items {
		if item.Kind != "tracker" && item.Kind != "description_group" && item.Kind != "upload" {
			continue
		}
		lane := trackerLane(lanes, index, api.TrackerID(item.ID))
		if lane == nil {
			continue
		}
		switch item.Status {
		case api.StageStatusPending, api.StageStatusQueued:
			lane.Lifecycle = api.OperationLifecycleQueued
		case api.StageStatusRunning:
			lane.Lifecycle = api.OperationLifecycleRunning
		case api.StageStatusBlocked:
			lane.Lifecycle = api.OperationLifecycleWaiting
			lane.Disposition = api.WorkflowDispositionNeedsAction
		case api.StageStatusFailed, api.StageStatusInterrupted, api.StageStatusUnavailable, api.StageStatusStale:
			lane.Lifecycle = api.OperationLifecycleTerminal
			lane.Disposition = api.WorkflowDispositionFailed
			lane.Retryable = true
		case api.StageStatusPartial:
			lane.Lifecycle = api.OperationLifecycleTerminal
			lane.Disposition = api.WorkflowDispositionPartial
		case api.StageStatusCanceled:
			lane.Lifecycle = api.OperationLifecycleTerminal
			lane.Disposition = api.WorkflowDispositionCanceled
		case api.StageStatusReady, api.StageStatusSkipped, api.StageStatusCompleted, api.StageStatusExecuted:
		}
	}
}

func reduceWorkflowOutcome(
	current CommandResult,
	lanes []api.TrackerLaneOutcome,
) (api.OperationLifecycle, api.WorkflowDisposition) {
	if current.Workflow.Status == api.WorkflowStatusCanceled {
		return api.OperationLifecycleTerminal, api.WorkflowDispositionCanceled
	}
	if current.Operation != nil && !isTerminalProgressStatus(current.Operation.Status) {
		switch current.Operation.Status {
		case api.StageStatusQueued, api.StageStatusPending:
			return api.OperationLifecycleQueued, api.WorkflowDispositionNone
		case api.StageStatusRunning:
			return api.OperationLifecycleRunning, api.WorkflowDispositionNone
		case api.StageStatusReady, api.StageStatusBlocked, api.StageStatusStale, api.StageStatusFailed,
			api.StageStatusPartial, api.StageStatusSkipped, api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusInterrupted,
			api.StageStatusCanceled, api.StageStatusUnavailable:
			return api.OperationLifecycleTerminal, operationDisposition(current.Operation.Status)
		}
	}

	var succeeded, failed, waiting, runnable int
	for _, lane := range lanes {
		switch lane.Disposition {
		case api.WorkflowDispositionSucceeded:
			succeeded++
		case api.WorkflowDispositionFailed, api.WorkflowDispositionCanceled:
			failed++
		case api.WorkflowDispositionNeedsAction:
			waiting++
		case api.WorkflowDispositionNone, api.WorkflowDispositionPartial:
			runnable++
		}
	}
	if runnable > 0 {
		if failed > 0 || succeeded > 0 {
			return api.OperationLifecycleReady, api.WorkflowDispositionPartial
		}
		return api.OperationLifecycleReady, api.WorkflowDispositionNone
	}
	if waiting > 0 || len(current.Workflow.RequiredActions) > 0 {
		if succeeded > 0 || failed > 0 {
			return api.OperationLifecycleWaiting, api.WorkflowDispositionPartial
		}
		return api.OperationLifecycleWaiting, api.WorkflowDispositionNeedsAction
	}
	if succeeded > 0 && failed > 0 {
		return api.OperationLifecycleTerminal, api.WorkflowDispositionPartial
	}
	if succeeded > 0 {
		return api.OperationLifecycleTerminal, api.WorkflowDispositionSucceeded
	}
	if failed > 0 || current.Workflow.Status == api.WorkflowStatusFailed || len(current.Workflow.Failures) > 0 {
		return api.OperationLifecycleTerminal, api.WorkflowDispositionFailed
	}
	if current.Workflow.Status == api.WorkflowStatusCompleted {
		return api.OperationLifecycleTerminal, api.WorkflowDispositionSucceeded
	}
	return api.OperationLifecycleReady, api.WorkflowDispositionNone
}

func projectAvailableGoals(current CommandResult) []api.GoalAvailability {
	goals := []api.WorkflowGoal{
		api.WorkflowGoalPrepared,
		api.WorkflowGoalTrackersAssessed,
		api.WorkflowGoalDuplicatesDecided,
		api.WorkflowGoalMediaReady,
		api.WorkflowGoalDescriptionsReady,
		api.WorkflowGoalUploadReviewed,
		api.WorkflowGoalDryRun,
		api.WorkflowGoalUploaded,
	}
	available := make([]api.GoalAvailability, 0, len(goals))
	for _, goal := range goals {
		allowed, code, reason := goalAvailability(current, goal)
		available = append(available, api.GoalAvailability{
			Goal:       goal,
			Available:  allowed,
			ReasonCode: code,
			Reason:     reason,
		})
	}
	return available
}

func goalAvailability(current CommandResult, goal api.WorkflowGoal) (bool, string, string) {
	if current.Workflow.Status == api.WorkflowStatusCanceled {
		return false, goalReasonWorkflowCanceled, "Workflow is canceled."
	}
	if current.Workflow.Status == api.WorkflowStatusCompleted && goal != api.WorkflowGoalUploaded {
		return false, goalReasonWorkflowComplete, "Workflow is complete."
	}
	if current.Operation != nil && !isTerminalProgressStatus(current.Operation.Status) {
		return false, goalReasonOperationActive, "Wait for the active operation to finish."
	}
	switch goal {
	case api.WorkflowGoalPrepared:
		return true, goalReasonAvailable, ""
	case api.WorkflowGoalTrackersAssessed:
		if current.Release == nil {
			return false, goalReasonPreparationRequired, "Prepare the selected source first."
		}
	case api.WorkflowGoalDuplicatesDecided:
		if current.Release == nil {
			return false, goalReasonPreparationRequired, "Prepare the selected source first."
		}
	case api.WorkflowGoalMediaReady:
		if current.Projections == nil {
			return false, goalReasonTrackersRequired, "Select and assess trackers first."
		}
	case api.WorkflowGoalDescriptionsReady:
		if current.Projections == nil {
			return false, goalReasonTrackersRequired, "Select and assess trackers first."
		}
		if current.Media == nil {
			return false, goalReasonMediaRequired, "Capture and select the required release images first."
		}
		if !stageSucceeded(current.Media.Status) {
			for _, action := range current.Media.RequiredActions {
				if action.Status == "" || action.Status == api.RequiredActionStatusPending {
					if prompt := strings.TrimSpace(action.Prompt); prompt != "" {
						return false, goalReasonMediaRequired, prompt
					}
				}
			}
			return false, goalReasonMediaRequired, "Required release images are not ready."
		}
	case api.WorkflowGoalUploadReviewed, api.WorkflowGoalDryRun, api.WorkflowGoalUploaded:
		if current.Projections == nil {
			return false, goalReasonTrackersRequired, "Select and assess trackers first."
		}
		if workflowRequiresDescriptions(current) && !descriptionsHaveViableTracker(current.Descriptions) {
			return false, goalReasonDescriptionsRequired, "Prepare at least one eligible tracker description first."
		}
	}
	return true, goalReasonAvailable, ""
}

func workflowRequiresDescriptions(current CommandResult) bool {
	if current.Projections != nil && slices.ContainsFunc(current.Projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.Artifacts.Description && projection.Readiness != api.ReadinessStatusIneligible
	}) {
		return true
	}
	if current.Catalog == nil || current.Selection == nil {
		return false
	}
	selected := make(map[api.TrackerID]struct{}, len(current.Selection.TrackerIDs))
	for _, trackerID := range current.Selection.TrackerIDs {
		selected[trackerID] = struct{}{}
	}
	return slices.ContainsFunc(current.Catalog.Trackers, func(tracker api.TrackerCatalogDescriptor) bool {
		_, ok := selected[tracker.TrackerID]
		return ok && tracker.Capabilities.Description
	})
}

func dupesAllowContinuation(projections *api.TrackerReleaseProjectionSet, dupes *api.DupeAssessment) bool {
	if projections == nil || dupes == nil {
		return false
	}
	return len(DownstreamEligibleProjections(*projections, *dupes).Projections) > 0
}

func descriptionsHaveViableTracker(descriptions *api.DescriptionSet) bool {
	if descriptions == nil {
		return false
	}
	if len(descriptions.TrackerResults) == 0 {
		return len(descriptions.Descriptions) > 0 || descriptions.Status == api.StageStatusSkipped
	}
	return slices.ContainsFunc(descriptions.TrackerResults, func(result api.DescriptionTrackerResult) bool {
		return stageSucceeded(result.Status)
	})
}

func stageSucceeded(status api.StageStatus) bool {
	switch status {
	case api.StageStatusReady, api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusSkipped:
		return true
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusBlocked, api.StageStatusStale,
		api.StageStatusFailed, api.StageStatusRunning, api.StageStatusInterrupted, api.StageStatusCanceled,
		api.StageStatusUnavailable, api.StageStatusPartial:
		return false
	}
	return false
}

func stageFailed(status api.StageStatus) bool {
	switch status {
	case api.StageStatusBlocked, api.StageStatusStale, api.StageStatusFailed, api.StageStatusInterrupted,
		api.StageStatusCanceled, api.StageStatusUnavailable:
		return true
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusSkipped,
		api.StageStatusRunning, api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusPartial:
		return false
	}
	return false
}

func projectWorkflowEvents(current CommandResult) []api.WorkflowEvent {
	operation := current.Operation
	if operation == nil {
		return nil
	}
	if len(operation.Events) > 0 {
		return append([]api.WorkflowEvent(nil), operation.Events...)
	}
	return projectOperationEvents(*operation)
}

func projectOperationEvents(operation api.WorkflowOperationStatus) []api.WorkflowEvent {
	events := make([]api.WorkflowEvent, 0, len(operation.Items)+1)
	events = append(events, api.WorkflowEvent{
		Sequence:    operation.Sequence * 1000,
		WorkflowID:  operation.WorkflowID,
		OperationID: operation.ID,
		Command:     operation.Command,
		Phase:       operation.Phase,
		Scope:       api.WorkflowEventScopeWorkflow,
		ScopeID:     string(operation.WorkflowID),
		Lifecycle:   operationLifecycle(operation.Status),
		State:       operation.Status,
		Disposition: operationDisposition(operation.Status),
		Severity:    operationSeverity(operation.Status, api.WorkflowEventScopeWorkflow, operation.Phase, operation.Message),
		Completed:   operation.Completed,
		Total:       operation.Total,
		Message:     operation.Message,
		Timestamp:   operation.UpdatedAt,
	})
	if len(operation.Failures) > 0 {
		events[0].FailureCode = operation.Failures[0].Failure.Code
		events[0].Recovery = operation.Failures[0].Failure.Recovery
	}
	for itemIndex, item := range operation.Items {
		scope := eventScope(item.Kind)
		event := api.WorkflowEvent{
			Sequence:    operation.Sequence*1000 + uint64(itemIndex) + 1,
			WorkflowID:  operation.WorkflowID,
			OperationID: operation.ID,
			Command:     operation.Command,
			Phase:       item.Phase,
			Scope:       scope,
			ScopeID:     item.ID,
			Lifecycle:   operationLifecycle(item.Status),
			State:       item.Status,
			Disposition: operationDisposition(item.Status),
			Severity:    operationSeverity(item.Status, scope, item.Phase, item.Message),
			Completed:   item.Completed,
			Total:       item.Total,
			Message:     item.Message,
			Timestamp:   operation.UpdatedAt,
		}
		for _, failure := range operation.Failures {
			if failure.TrackerID != "" && string(failure.TrackerID) != item.ID {
				continue
			}
			event.FailureCode = failure.Failure.Code
			event.Recovery = failure.Failure.Recovery
			break
		}
		if event.FailureCode == "" && scope == api.WorkflowEventScopeHost && item.Status == api.StageStatusFailed {
			event.FailureCode = api.OperationFailureImageHostUnavailable
			event.Recovery = api.OperationRecoveryRetry
		}
		events = append(events, event)
	}
	return events
}

func projectOperationEventChanges(previous api.WorkflowOperationStatus, current api.WorkflowOperationStatus) []api.WorkflowEvent {
	previousByScope := make(map[string]api.WorkflowEvent, len(previous.Items)+1)
	for _, event := range projectOperationEvents(previous) {
		previousByScope[workflowEventScopeKey(event)] = event
	}
	currentEvents := projectOperationEvents(current)
	changes := make([]api.WorkflowEvent, 0, len(currentEvents))
	for _, event := range currentEvents {
		previousEvent, ok := previousByScope[workflowEventScopeKey(event)]
		if ok && workflowEventContentEqual(previousEvent, event) {
			continue
		}
		changes = append(changes, event)
	}
	return changes
}

func workflowEventScopeKey(event api.WorkflowEvent) string {
	return string(event.Scope) + "\x00" + event.ScopeID
}

func workflowEventContentEqual(left api.WorkflowEvent, right api.WorkflowEvent) bool {
	left.Sequence = 0
	left.Timestamp = time.Time{}
	right.Sequence = 0
	right.Timestamp = time.Time{}
	return left == right
}

func operationLifecycle(status api.StageStatus) api.OperationLifecycle {
	switch status {
	case api.StageStatusQueued, api.StageStatusPending:
		return api.OperationLifecycleQueued
	case api.StageStatusRunning:
		return api.OperationLifecycleRunning
	case api.StageStatusBlocked:
		return api.OperationLifecycleWaiting
	case api.StageStatusReady:
		return api.OperationLifecycleReady
	case api.StageStatusStale, api.StageStatusFailed, api.StageStatusPartial, api.StageStatusSkipped, api.StageStatusCompleted,
		api.StageStatusExecuted, api.StageStatusInterrupted, api.StageStatusCanceled, api.StageStatusUnavailable:
		return api.OperationLifecycleTerminal
	}
	return api.OperationLifecycleTerminal
}

func operationDisposition(status api.StageStatus) api.WorkflowDisposition {
	switch status {
	case api.StageStatusReady, api.StageStatusCompleted, api.StageStatusExecuted, api.StageStatusSkipped:
		return api.WorkflowDispositionSucceeded
	case api.StageStatusPartial:
		return api.WorkflowDispositionPartial
	case api.StageStatusBlocked:
		return api.WorkflowDispositionNeedsAction
	case api.StageStatusFailed, api.StageStatusInterrupted, api.StageStatusStale, api.StageStatusUnavailable:
		return api.WorkflowDispositionFailed
	case api.StageStatusCanceled:
		return api.WorkflowDispositionCanceled
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusRunning:
		return api.WorkflowDispositionNone
	}
	return api.WorkflowDispositionNone
}

func operationSeverity(
	status api.StageStatus,
	scope api.WorkflowEventScope,
	phase string,
	message string,
) api.WorkflowEventSeverity {
	switch status {
	case api.StageStatusFailed, api.StageStatusInterrupted, api.StageStatusStale, api.StageStatusUnavailable:
		if scope == api.WorkflowEventScopeHost {
			return api.WorkflowEventSeverityWarn
		}
		return api.WorkflowEventSeverityError
	case api.StageStatusPartial, api.StageStatusBlocked, api.StageStatusCanceled:
		return api.WorkflowEventSeverityWarn
	case api.StageStatusSkipped, api.StageStatusReady:
		return api.WorkflowEventSeverityInfo
	case api.StageStatusCompleted, api.StageStatusExecuted:
		if scope == api.WorkflowEventScopeWorkflow ||
			strings.EqualFold(strings.TrimSpace(phase), "tracker_upload") ||
			strings.Contains(strings.ToLower(message), "bypass") {
			return api.WorkflowEventSeverityInfo
		}
		return api.WorkflowEventSeverityDebug
	case api.StageStatusPending, api.StageStatusQueued, api.StageStatusRunning:
		return api.WorkflowEventSeverityDebug
	}
	return api.WorkflowEventSeverityDebug
}

func eventScope(kind string) api.WorkflowEventScope {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "tracker", "description_group", "upload":
		return api.WorkflowEventScopeTracker
	case "image_host":
		return api.WorkflowEventScopeHost
	case "media":
		return api.WorkflowEventScopeArtifact
	case "client":
		return api.WorkflowEventScopeClient
	default:
		return api.WorkflowEventScopeWorkflow
	}
}
