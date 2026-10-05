// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) pendingDuplicateReuse(
	ownerID string,
	state *State,
	projections api.TrackerReleaseProjectionSet,
	command CheckDuplicatesCommand,
	now time.Time,
) (*DuplicateAssessmentReuse, error) {
	pending := state.PendingDuplicateReuse
	if pending == nil {
		return nil, nil
	}
	prior, ok := state.Dupes[pending.Assessment.ID]
	if !ok || prior.Revision != pending.Assessment.Revision || !prior.ExpiresAt.After(now) ||
		prior.ReleaseRef != projections.ReleaseRef ||
		normalizedDuplicateCheckOrdinal(prior.CheckOrdinal) != normalizedDuplicateCheckOrdinal(command.CheckOrdinal) {
		return nil, nil
	}
	previous, ok := state.Projections[prior.ProjectionSet.ID]
	if !ok || previous.Revision != prior.ProjectionSet.Revision || previous.ExecutionMode != projections.ExecutionMode {
		return nil, nil
	}
	evidence, err := m.private.Get(ownerID, state.Workflow.ID, dupePrivateResourceID(prior.ID), now)
	if errors.Is(err, ErrPrivateResourceUnavailable) || errors.Is(err, ErrPrivateResourceConsumed) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("release workflow load reusable duplicate evidence: %w", err)
	}
	return &DuplicateAssessmentReuse{
		QuestionnaireOnly:   pending.QuestionnaireOnly,
		Assessment:          prior,
		Projections:         previous,
		PrivateEvidence:     evidence,
		InvalidatedTrackers: pending.InvalidatedTrackers,
	}, nil
}

// stageQuestionnaireDuplicateReuse retains the original baseline across incomplete
// answer edits. Confirmed submissions are excluded from selection comparison;
// other selection, instruction, and execution changes discard the baseline.
func stageQuestionnaireDuplicateReuse(state *State, command ProjectTrackersCommand) (bool, error) {
	pending := state.PendingDuplicateReuse
	state.PendingDuplicateReuse = nil
	workflow := state.Workflow
	if workflow.TrackerProjections == nil || workflow.Selection == nil || workflow.ProjectionInstructions == nil {
		return false, nil
	}
	projections := state.Projections[workflow.TrackerProjections.ID]
	selection := state.Selections[workflow.Selection.ID]
	instructions := state.ProjectionInstructions[workflow.ProjectionInstructions.ID]
	desired := normalizeContinuationTrackerIDs(withoutConfirmedSubmissions(command.TrackerIDs, workflow.SubmissionExclusions))
	if projections.Revision != workflow.TrackerProjections.Revision || selection.Revision != workflow.Selection.Revision ||
		instructions.Revision != workflow.ProjectionInstructions.Revision ||
		!slices.Equal(desired, normalizeContinuationTrackerIDs(selection.TrackerIDs)) ||
		api.NormalizeWorkflowExecutionMode(command.ExecutionMode) != api.NormalizeWorkflowExecutionMode(projections.ExecutionMode) {
		return false, nil
	}
	previous := effectiveProjectionInstructions(instructions.Instructions)
	next := effectiveProjectionInstructions(command.Instructions)
	unchanged, err := sameProjectionInstructions(previous, next)
	if err != nil || unchanged {
		return false, err
	}
	stripAnswers := func(instructions map[api.TrackerID]api.TrackerProjectionInstructions) map[api.TrackerID]api.TrackerProjectionInstructions {
		result := maps.Clone(instructions)
		for trackerID, instruction := range result {
			instruction.Questionnaire = nil
			result[trackerID] = instruction
		}
		return effectiveProjectionInstructions(result)
	}
	answerOnly, err := sameProjectionInstructions(stripAnswers(previous), stripAnswers(next))
	if err != nil || !answerOnly {
		return false, err
	}
	if workflow.Dupes != nil {
		state.PendingDuplicateReuse = &PendingDuplicateReuse{Assessment: *workflow.Dupes, QuestionnaireOnly: true}
	} else if pending != nil && pending.QuestionnaireOnly {
		state.PendingDuplicateReuse = pending
	}
	return true, nil
}

func sameProjectionInstructions(left, right map[api.TrackerID]api.TrackerProjectionInstructions) (bool, error) {
	a, err := api.CanonicalWorkflowFingerprint(left)
	if err != nil {
		return false, fmt.Errorf("release workflow fingerprint prior questionnaire instructions: %w", err)
	}
	b, err := api.CanonicalWorkflowFingerprint(right)
	if err != nil {
		return false, fmt.Errorf("release workflow fingerprint current questionnaire instructions: %w", err)
	}
	return a == b, nil
}

func (m *Module) rebindQuestionnaireDupes(
	ctx context.Context, ownerID string, state *State, nextRevision api.WorkflowRevision, now time.Time,
	command PreflightTrackersCommand, projections api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment,
) (*api.DupeAssessment, error) {
	pending := state.PendingDuplicateReuse
	rebinder, ok := m.dupeBuilder.(QuestionnaireDupeAssessmentRebinder)
	if !ok || pending == nil || !pending.QuestionnaireOnly || projections.Status != api.StageStatusReady || preflight.Status != api.StageStatusReady {
		return nil, nil
	}
	prior := state.Dupes[pending.Assessment.ID]
	if normalizedDuplicateCheckOrdinal(prior.CheckOrdinal) < normalizedDuplicateCheckOrdinal(command.DuplicateCheckCount) {
		return nil, nil
	}
	reuse, err := m.pendingDuplicateReuse(ownerID, state, projections, CheckDuplicatesCommand{CheckOrdinal: prior.CheckOrdinal}, now)
	if err != nil || reuse == nil {
		return nil, err
	}
	trackerIDs := make([]string, len(projections.Projections))
	for index, projection := range projections.Projections {
		trackerIDs[index] = string(projection.TrackerID)
	}
	subject, err := m.preparer.ResolveDuplicateSubject(ctx, api.DuplicateCheckInput{
		Release:  projections.ReleaseRef,
		Trackers: trackerIDs,
		Skip:     command.SkipRemoteDuplicates,
	})
	if err != nil {
		return nil, fmt.Errorf("release workflow resolve reusable duplicate subject: %w", err)
	}
	snapshot, evidence, err := rebinder.RebindQuestionnaire(ctx, subject, projections, preflight, now, command.SkipRemoteDuplicates, *reuse)
	if err != nil {
		return nil, fmt.Errorf("release workflow rebind questionnaire evidence: %w", err)
	}
	if len(snapshot.Results) == 0 {
		return nil, nil
	}
	result, err := m.publishBuiltDupes(ownerID, state, nextRevision, now, projections, snapshot, evidence, prior.CheckOrdinal)
	return result.Dupes, err
}
