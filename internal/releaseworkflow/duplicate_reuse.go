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

// RequireStaffTokenReview clears retained attestation and blocks a staff-required
// lane until the owner confirms possession for the current upload assessment.
// It replaces the lane's decision and actions while retaining its search evidence.
func RequireStaffTokenReview(result *api.TrackerDupeAssessment) {
	result.StaffTokenConfirmed = false
	result.Decision = api.DupeDecisionPending
	result.Status = api.StageStatusBlocked
	result.RequiredActions = []api.RequiredAction{{
		Kind:      api.RequiredActionReviewDuplicates,
		Status:    api.RequiredActionStatusPending,
		TrackerID: result.TrackerID,
		Prompt:    "Confirm you have the appropriate staff-issued token for this upload. Without confirmation, the upload is blocked.",
		Options: []api.RequiredActionOption{
			{Value: string(api.DupeDecisionAccepted), Label: "No staff token; block upload"},
			{Value: string(api.DupeDecisionIgnored), Label: "I have the appropriate staff-issued token"},
		},
	}}
}

// stageMetadataDuplicateReuse retains only a historical candidate while active
// authority is withdrawn. Preparation must certify the exact generation pair
// before any duplicate decision can be rebound.
func stageMetadataDuplicateReuse(state *State, now time.Time) {
	if state.Workflow.Release != nil && state.Workflow.Dupes != nil {
		ref := state.Workflow.Dupes
		prior, ok := state.Dupes[ref.ID]
		release, err := currentReleaseSnapshot(state)
		if ok && err == nil && prior.Revision == ref.Revision && prior.ExpiresAt.After(now) &&
			prior.ReleaseRef == (api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}) {
			state.PendingDuplicateReuse = &PendingDuplicateReuse{Assessment: *ref, MetadataRefresh: true}
			return
		}
	}
	if pending := state.PendingDuplicateReuse; pending != nil && pending.MetadataRefresh {
		return
	}
	state.PendingDuplicateReuse = nil
}

// certifyMetadataDuplicateReuse accepts only the preparation owner's exact
// predecessor certificate. Missing proof drops the candidate without granting
// any new review authority or extending its evidence lifetime.
func certifyMetadataDuplicateReuse(state *State, prepared api.PrepareResult) {
	pending := state.PendingDuplicateReuse
	if pending == nil || !pending.MetadataRefresh {
		return
	}
	prior, ok := state.Dupes[pending.Assessment.ID]
	if !ok || prior.Revision != pending.Assessment.Revision || prepared.EquivalentPreviousRelease == nil ||
		prior.ReleaseRef != *prepared.EquivalentPreviousRelease {
		state.PendingDuplicateReuse = nil
		return
	}
	next := api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation}
	if next.SourcePath != prior.ReleaseRef.SourcePath || next.Generation <= prior.ReleaseRef.Generation {
		state.PendingDuplicateReuse = nil
		return
	}
	pending.EquivalentMetadataTo = &next
}

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
		(prior.ReleaseRef != projections.ReleaseRef && (pending.EquivalentMetadataTo == nil || *pending.EquivalentMetadataTo != projections.ReleaseRef ||
			prior.ReleaseRef.SourcePath != projections.ReleaseRef.SourcePath)) ||
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
		EquivalentMetadataTo: pending.EquivalentMetadataTo,
		QuestionnaireOnly:    pending.QuestionnaireOnly,
		Assessment:           prior,
		Projections:          previous,
		PrivateEvidence:      evidence,
		InvalidatedTrackers:  pending.InvalidatedTrackers,
	}, nil
}

// stageQuestionnaireDuplicateReuse retains the original baseline across incomplete
// answer edits. Confirmed submissions are excluded from selection comparison;
// other selection, instruction, and execution changes discard the baseline.
func stageQuestionnaireDuplicateReuse(state *State, command ProjectTrackersCommand) (bool, error) {
	pending := state.PendingDuplicateReuse
	if pending != nil && pending.MetadataRefresh && pending.EquivalentMetadataTo != nil {
		release, err := currentReleaseSnapshot(state)
		if err == nil &&
			*pending.EquivalentMetadataTo == (api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}) {
			return false, nil
		}
	}
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
	if !ok || pending == nil || (!pending.QuestionnaireOnly && pending.EquivalentMetadataTo == nil) || projections.Status != api.StageStatusReady ||
		preflight.Status != api.StageStatusReady {
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
