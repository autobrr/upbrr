// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func reusableWorkflowDupes(
	reuse *releaseworkflow.DuplicateAssessmentReuse,
	projections api.TrackerReleaseProjectionSet,
	now time.Time,
	skipRemote bool,
	inClient func(api.TrackerID) bool,
) (map[api.TrackerID]api.TrackerDupeAssessment, workflowDupePrivateEvidence, error) {
	if reuse == nil || reuse.Assessment.ReleaseRef != projections.ReleaseRef ||
		reuse.Projections.ExecutionMode != projections.ExecutionMode || !reuse.Assessment.ExpiresAt.After(now) {
		return nil, workflowDupePrivateEvidence{}, nil
	}
	evidence, ok := reuse.PrivateEvidence.(workflowDupePrivateEvidence)
	if !ok || evidence.SkipRemote == nil || *evidence.SkipRemote != skipRemote {
		return nil, workflowDupePrivateEvidence{}, nil
	}
	if _, ok := evidence.Assessment.(dupechecking.Assessment); !ok {
		return nil, workflowDupePrivateEvidence{}, nil
	}
	previous := make(map[api.TrackerID]api.TrackerReleaseProjection, len(reuse.Projections.Projections))
	for _, projection := range reuse.Projections.Projections {
		previous[projection.TrackerID] = projection
	}
	results := make(map[api.TrackerID]api.TrackerDupeAssessment, len(reuse.Assessment.Results))
	for _, result := range reuse.Assessment.Results {
		results[result.TrackerID] = result
	}
	retained := make(map[api.TrackerID]api.TrackerDupeAssessment)
	for _, projection := range projections.Projections {
		prior, projectionOK := previous[projection.TrackerID]
		result, resultOK := results[projection.TrackerID]
		ready := projection.Readiness == api.ReadinessStatusReady && projection.DupeReady
		unchangedSkippedLane := reuse.QuestionnaireOnly && result.Decision == api.DupeDecisionSkipped && !ready
		if !projectionOK || !resultOK || slices.Contains(reuse.InvalidatedTrackers, projection.TrackerID) ||
			((!ready || result.Decision == api.DupeDecisionSkipped) && !unchangedSkippedLane) ||
			!result.FreshUntil.After(now) || inClient(projection.TrackerID) ||
			slices.ContainsFunc(result.Matches, func(match api.DupeMatchProjection) bool {
				return strings.EqualFold(strings.TrimSpace(match.Reason), "in_client")
			}) {
			continue
		}
		boundFingerprint, err := api.CanonicalWorkflowFingerprint(prior)
		if err != nil {
			return nil, workflowDupePrivateEvidence{}, fmt.Errorf("workflow duplicate check: fingerprint prior projection: %w", err)
		}
		if result.ProjectionFingerprint != boundFingerprint {
			continue
		}
		previousFingerprint, err := reusableDupeProjectionFingerprint(prior, reuse.QuestionnaireOnly)
		if err != nil {
			return nil, workflowDupePrivateEvidence{}, err
		}
		currentFingerprint, err := reusableDupeProjectionFingerprint(projection, reuse.QuestionnaireOnly)
		if err != nil {
			return nil, workflowDupePrivateEvidence{}, err
		}
		if previousFingerprint == currentFingerprint {
			result.RequiredActions = slices.Clone(result.RequiredActions)
			retained[projection.TrackerID] = result
		}
	}
	return retained, evidence, nil
}

// Scoped edits change the set-wide input hash and action publication stamps.
// Questionnaire-only transitions may also ignore answer and display-schema data.
// All tracker-local policy, configuration, readiness and action content must match.
func reusableDupeProjectionFingerprint(projection api.TrackerReleaseProjection, questionnaireOnly bool) (api.WorkflowFingerprint, error) {
	projection.InputFingerprint = ""
	if questionnaireOnly {
		projection.Questionnaire = nil
		projection.QuestionnaireAnswers = nil
	}
	projection.RequiredActions = slices.Clone(projection.RequiredActions)
	for index := range projection.RequiredActions {
		action := &projection.RequiredActions[index]
		action.ID = ""
		action.WorkflowRevision = 0
		action.CreatedAt = time.Time{}
		action.ExpiresAt = nil
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
	if err != nil {
		return "", fmt.Errorf("workflow duplicate check: fingerprint reusable projection: %w", err)
	}
	return fingerprint, nil
}
