// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type downstreamStage string

const (
	downstreamStageMedia        downstreamStage = "media"
	downstreamStageDescriptions downstreamStage = "descriptions"
	downstreamStageUpload       downstreamStage = "upload"
)

type downstreamTrackerSet struct {
	orderedIDs      []api.TrackerID
	projections     api.TrackerReleaseProjectionSet
	trackerApproval *api.TrackerApprovalSnapshotRef
	authority       api.WorkflowFingerprint
}

func (s downstreamTrackerSet) TrackerIDs() []api.TrackerID {
	return append([]api.TrackerID(nil), s.orderedIDs...)
}

func (s downstreamTrackerSet) Projections() api.TrackerReleaseProjectionSet {
	return s.projections
}

func (s downstreamTrackerSet) TrackerApproval() *api.TrackerApprovalSnapshotRef {
	if s.trackerApproval == nil {
		return nil
	}
	ref := *s.trackerApproval
	return &ref
}

// ProjectionEligibleForDownstream reports whether retained projection and
// duplicate state permit media, description, and upload-plan work.
func ProjectionEligibleForDownstream(
	projection api.TrackerReleaseProjection,
	dupe api.TrackerDupeAssessment,
	hasDupe bool,
) bool {
	if !projection.UploadReady || projection.Readiness != api.ReadinessStatusReady || !hasDupe {
		return false
	}
	if dupe.Status == api.StageStatusFailed || dupe.Decision == api.DupeDecisionPending || dupe.Decision == api.DupeDecisionAccepted {
		return false
	}
	return true
}

// DownstreamEligibleProjections applies the shared retained eligibility
// predicate while preserving projection order and snapshot identity.
func DownstreamEligibleProjections(
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
) api.TrackerReleaseProjectionSet {
	dupeByTracker := make(map[api.TrackerID]api.TrackerDupeAssessment, len(dupes.Results))
	for _, result := range dupes.Results {
		dupeByTracker[result.TrackerID] = result
	}
	eligible := projections
	eligible.Projections = make([]api.TrackerReleaseProjection, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		dupe, exists := dupeByTracker[projection.TrackerID]
		if ProjectionEligibleForDownstream(projection, dupe, exists) {
			eligible.Projections = append(eligible.Projections, projection)
		}
	}
	return eligible
}

func currentDownstreamEvidence(state *State, now time.Time) (api.TrackerReleaseProjectionSet, api.DupeAssessment, error) {
	workflow := state.Workflow
	if workflow.Release == nil || workflow.Selection == nil || workflow.TrackerProjections == nil ||
		workflow.TrackerPreflight == nil || workflow.Dupes == nil {
		return api.TrackerReleaseProjectionSet{}, api.DupeAssessment{}, fmt.Errorf(
			"%w: downstream tracker evidence is incomplete",
			ErrInvalidTransition,
		)
	}
	projections, projectionsOK := state.Projections[workflow.TrackerProjections.ID]
	preflight, preflightOK := state.Preflights[workflow.TrackerPreflight.ID]
	dupes, dupesOK := state.Dupes[workflow.Dupes.ID]
	if !projectionsOK || !preflightOK || !dupesOK ||
		projections.Revision != workflow.TrackerProjections.Revision ||
		preflight.Revision != workflow.TrackerPreflight.Revision ||
		dupes.Revision != workflow.Dupes.Revision ||
		projections.Preflight == nil || *projections.Preflight != *workflow.TrackerPreflight ||
		dupes.ProjectionSet != *workflow.TrackerProjections ||
		dupes.Selection != *workflow.Selection ||
		!preflight.ExpiresAt.After(now) || !dupes.ExpiresAt.After(now) ||
		preflight.Status != api.StageStatusReady || !dupesAllowContinuation(&projections, &dupes) {
		return api.TrackerReleaseProjectionSet{}, api.DupeAssessment{}, fmt.Errorf(
			"%w: downstream tracker evidence is stale or unresolved",
			ErrInvalidTransition,
		)
	}
	return projections, dupes, nil
}

func trackerApprovalCandidates(
	state *State,
	now time.Time,
) (api.TrackerReleaseProjectionSet, api.DupeAssessment, []api.TrackerID, error) {
	projections, dupes, err := currentDownstreamEvidence(state, now)
	if err != nil {
		return api.TrackerReleaseProjectionSet{}, api.DupeAssessment{}, nil, err
	}
	dupeByTracker := make(map[api.TrackerID]api.TrackerDupeAssessment, len(dupes.Results))
	for _, result := range dupes.Results {
		dupeByTracker[result.TrackerID] = result
	}
	for _, projection := range projections.Projections {
		if !projection.UploadReady || projection.Readiness != api.ReadinessStatusReady {
			continue
		}
		dupe, exists := dupeByTracker[projection.TrackerID]
		if !exists || dupe.Decision == api.DupeDecisionPending {
			return api.TrackerReleaseProjectionSet{}, dupes, nil, nil
		}
	}
	eligible := DownstreamEligibleProjections(projections, dupes)
	ids := make([]api.TrackerID, len(eligible.Projections))
	for index, projection := range eligible.Projections {
		ids[index] = projection.TrackerID
	}
	return eligible, dupes, ids, nil
}

func trackerApprovalActionInput(
	state *State,
	dupes api.DupeAssessment,
	candidateIDs []api.TrackerID,
) (api.WorkflowFingerprint, error) {
	workflow := state.Workflow
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Mode                TrackerDecisionMode
		WorkflowID          api.WorkflowID
		Release             api.ReleaseSnapshotRef
		Selection           api.TrackerSelectionRef
		ProjectionSet       api.TrackerReleaseProjectionSetRef
		Preflight           api.TrackerPreflightAssessmentRef
		Dupes               api.DupeAssessmentRef
		DupeInput           api.WorkflowFingerprint
		CandidateTrackerIDs []api.TrackerID
	}{
		Mode:                normalizeTrackerDecisionMode(state.TrackerDecisionMode),
		WorkflowID:          workflow.ID,
		Release:             *workflow.Release,
		Selection:           *workflow.Selection,
		ProjectionSet:       *workflow.TrackerProjections,
		Preflight:           *workflow.TrackerPreflight,
		Dupes:               *workflow.Dupes,
		DupeInput:           dupes.InputFingerprint,
		CandidateTrackerIDs: append([]api.TrackerID(nil), candidateIDs...),
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint tracker approval action: %w", err)
	}
	return fingerprint, nil
}

func trackerApprovalFingerprint(
	actionInput api.WorkflowFingerprint,
	candidateIDs []api.TrackerID,
	approvedIDs []api.TrackerID,
) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		ActionInput         api.WorkflowFingerprint
		CandidateTrackerIDs []api.TrackerID
		ApprovedTrackerIDs  []api.TrackerID
	}{
		ActionInput:         actionInput,
		CandidateTrackerIDs: candidateIDs,
		ApprovedTrackerIDs:  approvedIDs,
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint tracker approval: %w", err)
	}
	return fingerprint, nil
}

func projectedTrackerApprovalAction(state *State, now time.Time) (*api.RequiredAction, api.WorkflowFingerprint, error) {
	if normalizeTrackerDecisionMode(state.TrackerDecisionMode) != TrackerDecisionModePostDupeGate ||
		state.Workflow.TrackerApproval != nil {
		return nil, "", nil
	}
	eligible, dupes, candidateIDs, err := trackerApprovalCandidates(state, now)
	if err != nil || len(candidateIDs) == 0 {
		return nil, "", nil
	}
	fingerprint, err := trackerApprovalActionInput(state, dupes, candidateIDs)
	if err != nil {
		return nil, "", err
	}
	options := make([]api.RequiredActionOption, len(eligible.Projections))
	for index, projection := range eligible.Projections {
		label := strings.TrimSpace(projection.DisplayName)
		if label == "" {
			label = string(projection.TrackerID)
		}
		options[index] = api.RequiredActionOption{Value: string(projection.TrackerID), Label: label}
	}
	expiresAt := dupes.ExpiresAt
	return &api.RequiredAction{
		ID:               api.RequiredActionID("approve-trackers-" + string(fingerprint[:16])),
		Kind:             api.RequiredActionApproveTrackers,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: state.Workflow.Revision,
		Prompt:           "Select trackers to continue through preparation and upload.",
		Options:          options,
		CreatedAt:        dupes.CreatedAt,
		ExpiresAt:        &expiresAt,
	}, fingerprint, nil
}

func currentTrackerApproval(
	state *State,
	now time.Time,
	candidates []api.TrackerID,
	dupes api.DupeAssessment,
) (api.TrackerApprovalSnapshot, error) {
	ref := state.Workflow.TrackerApproval
	if ref == nil {
		return api.TrackerApprovalSnapshot{}, fmt.Errorf("%w: tracker approval is required", ErrInvalidTransition)
	}
	approval, ok := state.TrackerApprovals[ref.ID]
	workflow := state.Workflow
	if !ok || approval.Revision != ref.Revision ||
		approval.WorkflowID != workflow.ID ||
		workflow.Release == nil || approval.Release != *workflow.Release ||
		workflow.Selection == nil || approval.Selection != *workflow.Selection ||
		workflow.TrackerProjections == nil || approval.ProjectionSet != *workflow.TrackerProjections ||
		workflow.TrackerPreflight == nil || approval.Preflight != *workflow.TrackerPreflight ||
		workflow.Dupes == nil || approval.Dupes != *workflow.Dupes ||
		!dupes.ExpiresAt.After(now) || !slices.Equal(approval.CandidateTrackerIDs, candidates) {
		return api.TrackerApprovalSnapshot{}, fmt.Errorf("%w: tracker approval is stale", ErrInvalidTransition)
	}
	actionInput, err := trackerApprovalActionInput(state, dupes, candidates)
	if err != nil {
		return api.TrackerApprovalSnapshot{}, err
	}
	expectedFingerprint, err := trackerApprovalFingerprint(
		actionInput,
		approval.CandidateTrackerIDs,
		approval.ApprovedTrackerIDs,
	)
	if err != nil {
		return api.TrackerApprovalSnapshot{}, err
	}
	if approval.InputFingerprint != expectedFingerprint {
		return api.TrackerApprovalSnapshot{}, fmt.Errorf("%w: tracker approval fingerprint is stale", ErrInvalidTransition)
	}
	return approval, nil
}

func resolveDownstreamTrackerSet(
	state *State,
	requested []api.TrackerID,
	stage downstreamStage,
	now time.Time,
) (downstreamTrackerSet, error) {
	projections, dupes, err := currentDownstreamEvidence(state, now)
	if err != nil {
		return downstreamTrackerSet{}, err
	}
	eligible := DownstreamEligibleProjections(projections, dupes)
	candidates := make([]api.TrackerID, len(eligible.Projections))
	for index, projection := range eligible.Projections {
		candidates[index] = projection.TrackerID
	}
	baseIDs := candidates
	var approvalRef *api.TrackerApprovalSnapshotRef
	authority, err := api.CanonicalWorkflowFingerprint(struct {
		Mode       TrackerDecisionMode
		Candidates []api.TrackerID
	}{
		Mode:       normalizeTrackerDecisionMode(state.TrackerDecisionMode),
		Candidates: candidates,
	})
	if err != nil {
		return downstreamTrackerSet{}, fmt.Errorf("fingerprint downstream tracker authority: %w", err)
	}
	switch normalizeTrackerDecisionMode(state.TrackerDecisionMode) {
	case TrackerDecisionModePostDupeGate:
		approval, approvalErr := currentTrackerApproval(state, now, candidates, dupes)
		if approvalErr != nil {
			return downstreamTrackerSet{}, approvalErr
		}
		baseIDs = approval.ApprovedTrackerIDs
		ref := api.TrackerApprovalSnapshotRef{ID: approval.ID, Revision: approval.Revision}
		approvalRef = &ref
		authority = approval.InputFingerprint
	case TrackerDecisionModeWebUIControls:
	}
	base := make(map[api.TrackerID]struct{}, len(baseIDs))
	for _, trackerID := range baseIDs {
		base[normalizeDownstreamTrackerID(trackerID)] = struct{}{}
	}
	if (stage == downstreamStageDescriptions || stage == downstreamStageUpload) && state.Workflow.Media != nil {
		media, ok := state.Media[state.Workflow.Media.ID]
		if !ok || media.Revision != state.Workflow.Media.Revision {
			return downstreamTrackerSet{}, fmt.Errorf("%w: retained media is stale", ErrInvalidTransition)
		}
		blocked := make([]string, 0, len(base))
		for trackerID := range base {
			if _, failed := TrackerImageHostFailure(media, trackerID); failed {
				delete(base, trackerID)
				blocked = append(blocked, string(trackerID))
			}
		}
		// An empty set here would otherwise travel silently into the upload
		// plan and only surface as a contract validation error about missing
		// target tracker IDs, which names neither the stage nor the cause.
		if len(base) == 0 && len(blocked) > 0 {
			slices.Sort(blocked)
			return downstreamTrackerSet{}, fmt.Errorf(
				"%w: image hosting failed for every downstream tracker (%s)",
				ErrInvalidTransition,
				strings.Join(blocked, ", "),
			)
		}
	}
	selected := base
	if len(requested) > 0 {
		selected = make(map[api.TrackerID]struct{}, len(requested))
		for _, trackerID := range requested {
			trackerID = normalizeDownstreamTrackerID(trackerID)
			if trackerID == "" {
				return downstreamTrackerSet{}, fmt.Errorf("%w: downstream tracker ID is required", ErrInvalidTransition)
			}
			if _, allowed := base[trackerID]; !allowed {
				return downstreamTrackerSet{}, fmt.Errorf(
					"%w: tracker %s is outside current downstream authority",
					ErrInvalidTransition,
					trackerID,
				)
			}
			if _, duplicate := selected[trackerID]; duplicate {
				return downstreamTrackerSet{}, fmt.Errorf(
					"%w: tracker %s appears more than once in the downstream selection",
					ErrInvalidTransition,
					trackerID,
				)
			}
			selected[trackerID] = struct{}{}
		}
	}
	filtered := projections
	filtered.Projections = make([]api.TrackerReleaseProjection, 0, len(selected))
	orderedIDs := make([]api.TrackerID, 0, len(selected))
	for _, projection := range eligible.Projections {
		if _, included := selected[projection.TrackerID]; !included {
			continue
		}
		filtered.Projections = append(filtered.Projections, projection)
		orderedIDs = append(orderedIDs, projection.TrackerID)
	}
	if len(requested) > 0 && len(orderedIDs) != len(requested) {
		return downstreamTrackerSet{}, errors.New("downstream tracker selection could not be resolved exactly")
	}
	return downstreamTrackerSet{
		orderedIDs:      orderedIDs,
		projections:     filtered,
		trackerApproval: approvalRef,
		authority:       authority,
	}, nil
}

func normalizeDownstreamTrackerID(value api.TrackerID) api.TrackerID {
	return api.TrackerID(strings.ToUpper(strings.TrimSpace(string(value))))
}

// TrackerImageHostFailure returns the terminal image-host failure that blocks
// one tracker in the retained media snapshot.
func TrackerImageHostFailure(media api.MediaArtifactSet, trackerID api.TrackerID) (api.WorkflowFailure, bool) {
	trackerID = normalizeDownstreamTrackerID(trackerID)
	if trackerID == "" {
		return api.WorkflowFailure{}, false
	}
	for _, failure := range media.Failures {
		if failure.Failure.Operation != api.OperationKindImageHosting {
			continue
		}
		if normalizeDownstreamTrackerID(failure.TrackerID) == trackerID {
			return failure, true
		}
	}
	return api.WorkflowFailure{}, false
}

// DownstreamEligibleProjectionsAfterMedia removes trackers with terminal
// tracker-scoped image-host failures from the regular post-dupe eligible set.
func DownstreamEligibleProjectionsAfterMedia(
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	media api.MediaArtifactSet,
) api.TrackerReleaseProjectionSet {
	eligible := DownstreamEligibleProjections(projections, dupes)
	eligible.Projections = slices.DeleteFunc(eligible.Projections, func(projection api.TrackerReleaseProjection) bool {
		_, failed := TrackerImageHostFailure(media, projection.TrackerID)
		return failed
	})
	return eligible
}
