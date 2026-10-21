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

// questionnaireDuplicateCandidates carries only fresh, exact server evidence across an answer-only edit.
// Other changes must search again before exposing candidate comparison questions.
func questionnaireDuplicateCandidates(state *State, release api.ReleaseSnapshot, now time.Time) map[string][]api.DupeMatchProjection {
	pending := state.PendingDuplicateReuse
	if pending == nil || !pending.QuestionnaireOnly {
		return nil
	}
	assessment, ok := state.Dupes[pending.Assessment.ID]
	if !ok || assessment.Revision != pending.Assessment.Revision || !assessment.ExpiresAt.After(now) ||
		assessment.ReleaseRef != (api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}) {
		return nil
	}
	projections, ok := state.Projections[assessment.ProjectionSet.ID]
	if !ok || projections.Revision != assessment.ProjectionSet.Revision {
		return nil
	}
	candidates := make(map[string][]api.DupeMatchProjection)
	for _, result := range assessment.Results {
		if !result.FreshUntil.After(now) || slices.Contains(pending.InvalidatedTrackers, result.TrackerID) {
			continue
		}
		for _, projection := range projections.Projections {
			if projection.TrackerID != result.TrackerID {
				continue
			}
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err == nil && result.ProjectionFingerprint == fingerprint {
				candidates[string(result.TrackerID)] = slices.Clone(result.Matches)
				// Keep prior bound answers for resolved candidates while another candidate is reviewed.
				// They cannot authorize a later result unless the evaluator sees the same evidence again.
				for _, comparison := range projection.DuplicateTarget.Comparisons {
					if comparison.CandidateID == "" || comparison.CandidateFingerprint == "" ||
						slices.ContainsFunc(result.Matches, func(match api.DupeMatchProjection) bool { return match.ID == comparison.CandidateID }) {
						continue
					}
					candidates[string(result.TrackerID)] = append(candidates[string(result.TrackerID)], api.DupeMatchProjection{
						ID:                  comparison.CandidateID,
						EvidenceFingerprint: comparison.CandidateFingerprint,
						Relation:            api.DupeRelationCoexists,
					})
				}
			}
		}
	}
	return candidates
}

// duplicateQuestionnaireFeedbackAllowed permits optional evidence review without accepting duplicate risk.
func duplicateQuestionnaireFeedbackAllowed(current CommandResult, action api.RequiredAction, response compositeUploadFeedbackResponse) bool {
	if action.Kind != api.RequiredActionReviewDuplicates || response.Kind != api.ReleaseWorkflowUploadFeedbackQuestionnaire ||
		action.TrackerID == "" || response.TrackerID != action.TrackerID || current.Dupes == nil || current.Projections == nil ||
		current.Dupes.WorkflowID != current.Workflow.ID || current.Dupes.ProjectionSet.ID != current.Projections.ID ||
		current.Dupes.ProjectionSet.Revision != current.Projections.Revision {
		return false
	}
	return slices.ContainsFunc(current.Dupes.Results, func(result api.TrackerDupeAssessment) bool {
		return result.TrackerID == action.TrackerID && len(result.Questionnaire) > 0 && !strictDupeResult(result)
	})
}

// projectDuplicateQuestionnaires rebuilds dependent question keys for fresh and reused evidence.
func (m *Module) projectDuplicateQuestionnaires(
	ctx context.Context,
	state *State,
	projections api.TrackerReleaseProjectionSet,
	snapshot api.DupeAssessment,
) (api.DupeAssessment, error) {
	projector, ok := m.trackerProjector.(DuplicateQuestionnaireProjector)
	if !ok {
		return snapshot, nil
	}
	trackerIDs := make([]string, len(projections.Projections))
	for index, projection := range projections.Projections {
		trackerIDs[index] = string(projection.TrackerID)
	}
	subject, err := m.preparer.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:              projections.ReleaseRef,
		Trackers:             trackerIDs,
		QuestionnaireAnswers: cloneTrackerInputAnswers(state.TrackerInputAnswers),
	})
	if err != nil {
		return api.DupeAssessment{}, fmt.Errorf("release workflow resolve duplicate questionnaire subject: %w", err)
	}
	return projector.DuplicateQuestionnaires(subject, projections, snapshot), nil
}
