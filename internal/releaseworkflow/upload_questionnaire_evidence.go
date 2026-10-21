// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// currentUploadQuestionnaires admits answers only to the exact pending dry run.
// Re-preparation still recomputes every resource-bound question key.
func currentUploadQuestionnaires(state *State, now time.Time) []api.TrackerQuestionnaire {
	w := state.Workflow
	if w.DryRun == nil || w.Release == nil || w.TrackerProjections == nil || w.Dupes == nil || w.Media == nil || w.Descriptions == nil {
		return nil
	}
	dryRun, ok := state.DryRuns[w.DryRun.ID]
	if !ok || dryRun.WorkflowID != w.ID || dryRun.Revision != w.DryRun.Revision || dryRun.ProjectionSet != *w.TrackerProjections ||
		dryRun.Dupes != *w.Dupes || dryRun.Media != *w.Media || dryRun.Descriptions != *w.Descriptions ||
		(dryRun.TrackerApproval == nil) != (w.TrackerApproval == nil) ||
		(dryRun.TrackerApproval != nil && *dryRun.TrackerApproval != *w.TrackerApproval) {
		return nil
	}
	release, err := currentReleaseSnapshot(state)
	if err != nil {
		return nil
	}
	releaseRef := api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	projection, projectionOK := state.Projections[w.TrackerProjections.ID]
	media, mediaOK := state.Media[w.Media.ID]
	description, descriptionOK := state.Descriptions[w.Descriptions.ID]
	if !projectionOK || projection.Revision != w.TrackerProjections.Revision || projection.Release != *w.Release || projection.ReleaseRef != releaseRef ||
		!mediaOK || media.Revision != w.Media.Revision || media.Release != *w.Release || media.ReleaseRef != releaseRef || media.ProjectionSet != *w.TrackerProjections ||
		!descriptionOK || description.Revision != w.Descriptions.Revision || description.Release != *w.Release || description.ReleaseRef != releaseRef ||
		description.ProjectionSet != *w.TrackerProjections || description.Media == nil || *description.Media != *w.Media {
		return nil
	}
	var schemas []api.TrackerQuestionnaire
	for _, report := range dryRun.Reports {
		if !slices.Contains(dryRun.TrackerIDs, report.TrackerID) || !slices.ContainsFunc(w.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionAnswerQuestionnaire && action.TrackerID == report.TrackerID &&
				action.WorkflowRevision == w.Revision && action.Status == api.RequiredActionStatusPending &&
				action.ExpiresAt != nil && now.Before(*action.ExpiresAt)
		}) {
			continue
		}
		schema := api.TrackerQuestionnaire{Tracker: string(report.TrackerID)}
		for _, field := range report.Questionnaire {
			schema.Fields = append(schema.Fields, api.TrackerQuestionnaireField{
				Key:                field.Key,
				Label:              field.Label,
				Kind:               field.Kind,
				Required:           field.Required,
				Options:            slices.Clone(field.Options),
				Help:               field.Help,
				HDRGradeComparison: field.HDRGradeComparison,
				NeedsReview:        field.NeedsReview,
			})
		}
		schemas = append(schemas, schema)
	}
	return schemas
}
