// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func hdrQuestionnaireState() *State {
	release := api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	media := api.MediaArtifactSetRef{ID: "media", Revision: 2}
	projection := api.TrackerReleaseProjectionSetRef{ID: "projection", Revision: 3}
	source := api.ReleaseRef{SourcePath: "synthetic.mkv", Generation: 1}
	analysis := api.HDRAnalysisRef{ID: "analysis", Revision: 4}
	artifact := api.HDRTypedArtifact{
		ID:                     "plot",
		Kind:                   api.HDRArtifactHDR10PlusPlot,
		Format:                 api.HDRNativeHDR10Plus,
		MIME:                   "image/png",
		RenderFingerprint:      "render",
		MeasurementFingerprint: "measure",
	}
	target := api.HDRAnalysisTargetResult{
		TargetID:  "target",
		Artifacts: []api.HDRTypedArtifact{artifact},
		Formats:   []api.HDRFormatResult{{Format: api.HDRNativeHDR10Plus, State: api.HDRExtractionComplete}},
	}
	return &State{
		Workflow: api.ReleaseWorkflow{
			Release:            &release,
			Media:              &media,
			TrackerProjections: &projection,
			Descriptions:       &api.DescriptionSetRef{ID: "description", Revision: 5},
			HDRAnalysisEnabled: true,
			HDRAnalysis:        &analysis,
		},
		HDRAnalyses: map[api.HDRAnalysisResultID]api.HDRAnalysisResult{"analysis": {
			ID:             analysis.ID,
			Revision:       analysis.Revision,
			Release:        source,
			ProfileVersion: api.HDRAnalysisProfileV3,
			Status:         api.StageStatusCompleted,
			Targets:        []api.HDRAnalysisTargetResult{target},
		}},
		Descriptions: map[api.DescriptionSetID]api.DescriptionSet{"description": {
			ID:            "description",
			Revision:      5,
			Release:       release,
			ReleaseRef:    source,
			ProjectionSet: projection,
			Media:         &media,
			Status:        api.StageStatusCompleted,
			Descriptions: []api.RenderedDescription{{
				GroupKey:   "group",
				TrackerIDs: []api.TrackerID{"LST"},
				Source:     "[img]https://images.example/plot.png[/img]",
			}},
			HDRArtifacts: map[api.TrackerID][]api.HDRArtifactEvidence{"LST": {{
				Analysis:               analysis,
				GenerationComplete:     true,
				ArtifactID:             artifact.ID,
				Kind:                   artifact.Kind,
				TargetID:               target.TargetID,
				RenderFingerprint:      artifact.RenderFingerprint,
				MeasurementFingerprint: artifact.MeasurementFingerprint,
				ImageURLs:              []string{"https://images.example/plot.png"},
			}}},
		}},
	}
}

func TestHDRQuestionnaireEvidenceRequiresCurrentCompleteLineage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*State)
		valid  bool
	}{
		{"current", func(*State) {}, true},
		{"completed lane with failed sibling", func(s *State) {
			d := s.Descriptions["description"]
			d.Status = api.StageStatusPartial
			d.TrackerResults = []api.DescriptionTrackerResult{{TrackerID: "LST", Status: api.StageStatusCompleted}, {TrackerID: "OTHER", Status: api.StageStatusFailed}}
			s.Descriptions["description"] = d
		}, true},
		{"failed lane", func(s *State) {
			d := s.Descriptions["description"]
			d.Status = api.StageStatusPartial
			d.TrackerResults = []api.DescriptionTrackerResult{{TrackerID: "LST", Status: api.StageStatusFailed}}
			s.Descriptions["description"] = d
		}, false},
		{"disabled", func(s *State) { s.Workflow.HDRAnalysisEnabled = false }, false},
		{"description revision", func(s *State) { s.Workflow.Descriptions.Revision++ }, false},
		{"release", func(s *State) { s.Workflow.Release.Revision++ }, false},
		{"media", func(s *State) { ref := *s.Workflow.Media; ref.Revision++; s.Workflow.Media = &ref }, false},
		{"projection", func(s *State) { s.Workflow.TrackerProjections.Revision++ }, false},
		{"analysis revision", func(s *State) { s.Workflow.HDRAnalysis.Revision++ }, false},
		{"selection removed", func(s *State) { s.Workflow.HDRImages = &api.HDRImageSelection{ArtifactIDs: []api.PublicResourceID{}} }, false},
		{"changed render", func(s *State) {
			a := s.HDRAnalyses["analysis"]
			a.Targets[0].Artifacts[0].RenderFingerprint = "changed"
			s.HDRAnalyses["analysis"] = a
		}, false},
		{"generation provenance changed", func(s *State) {
			d := s.Descriptions["description"]
			d.HDRArtifacts["LST"][0].GenerationComplete = false
			s.Descriptions["description"] = d
		}, false},
		{"extra selected artifact", func(s *State) {
			a := s.HDRAnalyses["analysis"]
			extra := a.Targets[0].Artifacts[0]
			extra.ID = "extra"
			a.Targets[0].Artifacts = append(a.Targets[0].Artifacts, extra)
			s.HDRAnalyses["analysis"] = a
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := hdrQuestionnaireState()
			tc.change(state)
			got := currentDescriptionHDRArtifacts(state)
			if (len(got["LST"]) > 0) != tc.valid {
				t.Fatalf("retained=%+v", got)
			}
		})
	}
}

func TestHDRQuestionnaireEvidenceCarriesTextWithoutMutatingSnapshot(t *testing.T) {
	state := hdrQuestionnaireState()
	var subject api.UploadSubject
	applyDescriptionHDRQuestionnaireEvidence(&subject, state)
	if len(subject.TrackerHDRArtifacts["LST"]) != 1 || !subject.DescriptionGroupsFinal || len(subject.DescriptionGroups) != 1 {
		t.Fatalf("missing retained context: %+v", subject)
	}
	subject.TrackerHDRArtifacts["LST"][0].ImageURLs[0] = "changed"
	if state.Descriptions["description"].HDRArtifacts["LST"][0].ImageURLs[0] == "changed" {
		t.Fatal("snapshot alias")
	}
}
