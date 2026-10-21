// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// currentDescriptionHDRArtifacts retains only exact selected analysis artifacts
// associated with the current source, media and rendered description revision.
func currentDescriptionHDRArtifacts(state *State) map[api.TrackerID][]api.HDRArtifactEvidence {
	w := state.Workflow
	if !w.HDRAnalysisEnabled || w.Descriptions == nil || w.Release == nil || w.Media == nil || w.TrackerProjections == nil || w.HDRAnalysis == nil {
		return nil
	}
	description, ok := state.Descriptions[w.Descriptions.ID]
	if !ok || description.Revision != w.Descriptions.Revision || description.Release != *w.Release || description.ProjectionSet != *w.TrackerProjections ||
		description.Media == nil ||
		*description.Media != *w.Media ||
		description.Status != api.StageStatusCompleted && description.Status != api.StageStatusPartial && description.Status != api.StageStatusBlocked {
		return nil
	}
	analysis, ok := state.HDRAnalyses[w.HDRAnalysis.ID]
	if !ok || analysis.Revision != w.HDRAnalysis.Revision || analysis.Release != description.ReleaseRef || !analysis.CanIncludeImages() {
		return nil
	}

	selected := make(map[api.PublicResourceID]struct {
		target   string
		artifact api.HDRTypedArtifact
		complete bool
	})
	for _, target := range analysis.Targets {
		images := target.DescriptionImages()
		if w.HDRImages != nil {
			images = slices.DeleteFunc(
				slices.Clone(target.SelectableImages()),
				func(a api.HDRTypedArtifact) bool { return !slices.Contains(w.HDRImages.ArtifactIDs, a.ID) },
			)
		}
		for _, artifact := range images {
			selected[artifact.ID] = struct {
				target   string
				artifact api.HDRTypedArtifact
				complete bool
			}{target.TargetID, artifact, target.ArtifactGenerationComplete(artifact)}
		}
	}
	result := make(map[api.TrackerID][]api.HDRArtifactEvidence)
	for tracker, artifacts := range description.HDRArtifacts {
		if (description.Status != api.StageStatusCompleted || len(description.TrackerResults) > 0) &&
			!slices.ContainsFunc(description.TrackerResults, func(result api.DescriptionTrackerResult) bool {
				return result.TrackerID == tracker && result.Status == api.StageStatusCompleted
			}) {
			continue
		}
		if len(artifacts) != len(selected) {
			continue
		}
		valid := true
		seen := make(map[api.PublicResourceID]bool)
		var retained []api.HDRArtifactEvidence
		for _, evidence := range artifacts {
			entry, exists := selected[evidence.ArtifactID]
			if !exists || seen[evidence.ArtifactID] || evidence.Analysis != *w.HDRAnalysis || entry.target != evidence.TargetID ||
				entry.complete != evidence.GenerationComplete || entry.artifact.Kind != evidence.Kind || entry.artifact.MeasurementFingerprint != evidence.MeasurementFingerprint || entry.artifact.RenderFingerprint != evidence.RenderFingerprint {
				valid = false
				break
			}
			seen[evidence.ArtifactID] = true
			evidence.ImageURLs = slices.Clone(evidence.ImageURLs)
			retained = append(retained, evidence)
		}
		if valid {
			result[tracker] = retained
		}
	}
	return result
}

func applyDescriptionHDRQuestionnaireEvidence(subject *api.UploadSubject, state *State) {
	evidence := currentDescriptionHDRArtifacts(state)
	if len(evidence) == 0 {
		return
	}
	subject.TrackerHDRArtifacts = make(map[string][]api.HDRArtifactEvidence, len(evidence))
	for tracker, artifacts := range evidence {
		subject.TrackerHDRArtifacts[string(tracker)] = artifacts
	}
	description := state.Descriptions[state.Workflow.Descriptions.ID]
	for _, group := range description.Descriptions {
		var trackers []string
		for _, tracker := range group.TrackerIDs {
			if len(evidence[tracker]) > 0 {
				trackers = append(trackers, string(tracker))
			}
		}
		if len(trackers) > 0 {
			subject.DescriptionGroups = append(
				subject.DescriptionGroups,
				api.DescriptionBuilderGroup{
					GroupKey:       group.GroupKey,
					Trackers:       trackers,
					RawDescription: group.Source,
					Final:          true,
				},
			)
		}
	}
	subject.DescriptionGroupsFinal = true
}
