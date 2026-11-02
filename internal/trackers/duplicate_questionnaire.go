// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"maps"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// DuplicateQuestionnaires adds tracker-owned questions after search, bound to the evaluated candidates.
// It does not change duplicate decisions or infer comparison answers.
func (p *WorkflowProjector) DuplicateQuestionnaires(
	subject api.UploadSubject,
	projections api.TrackerReleaseProjectionSet,
	assessment api.DupeAssessment,
) api.DupeAssessment {
	assessment.Results = slices.Clone(assessment.Results)
	for index := range assessment.Results {
		result := &assessment.Results[index]
		descriptor, ok := p.registry.LookupDescriptor(string(result.TrackerID))
		if !ok || descriptor.DupePolicy == nil || descriptor.DupePolicy.Questionnaire == nil {
			continue
		}
		projectionIndex := slices.IndexFunc(
			projections.Projections,
			func(projection api.TrackerReleaseProjection) bool { return projection.TrackerID == result.TrackerID },
		)
		if projectionIndex < 0 {
			continue
		}
		projection := projections.Projections[projectionIndex]
		meta, _ := ApplyGroupPolicy(trackerConfigFor(p.config, string(result.TrackerID)), subject)
		meta.TrackerQuestionnaireAnswers = maps.Clone(subject.TrackerQuestionnaireAnswers)
		if meta.TrackerQuestionnaireAnswers == nil {
			meta.TrackerQuestionnaireAnswers = make(map[string]map[string]string)
		}
		meta.TrackerQuestionnaireAnswers[string(result.TrackerID)] = maps.Clone(projection.QuestionnaireAnswers)
		meta.TrackerDuplicateReviewCandidates = map[string][]api.DupeMatchProjection{string(result.TrackerID): slices.Clone(result.Matches)}
		fields := descriptor.DupePolicy.Questionnaire(
			PreparationInput{
				TrackerConfig: trackerConfigFor(p.config, string(result.TrackerID)),
				Meta:          meta,
				Tracker:       string(result.TrackerID),
				Projection:    &projection,
				ExecutionMode: projections.ExecutionMode,
			},
		)
		result.Questionnaire = ProjectQuestionnaire(&api.TrackerQuestionnaire{Tracker: string(result.TrackerID), Fields: fields})
	}
	return assessment
}
