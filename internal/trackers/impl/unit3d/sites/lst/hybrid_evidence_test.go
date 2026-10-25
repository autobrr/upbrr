// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestHybridEvidenceUsesIncludedExactArtifactKinds(t *testing.T) {
	s := lstValidationSubject()
	s.LanguageFacts.Tracks = nil
	s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	s.HDRFacts = api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}}
	s.AssetFacts.HDRArtifacts = []api.HDRArtifactEvidence{
		{
			GenerationComplete: true,
			Kind:               api.HDRArtifactAutomaticGrade,
			TargetID:           "primary",
			ImageURLs:          []string{"https://images.example/grade.png"},
		},
		{
			GenerationComplete: true,
			Kind:               api.HDRArtifactHDR10PlusPlot,
			TargetID:           "primary",
			ImageURLs:          []string{"https://images.example/hdr.png"},
		},
	}
	complete := "[spoiler=HDR plots][img]https://images.example/grade.png[/img][img]https://images.example/hdr.png[/img][/spoiler]"
	for _, tc := range []struct {
		name, description string
		ready             bool
	}{
		{"included", complete, true}, {"unselected", "", false},
		{"literal", "[code]" + complete + "[/code]", false},
		{"URL only", "https://images.example/grade.png https://images.example/hdr.png", false},
		{"unrelated", strings.ReplaceAll(complete, "images.example", "other.example"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.DescriptionOverride = tc.description
			if got := generatedHybridEvidenceIncluded(s, tc.description); got != tc.ready {
				t.Fatalf("ready=%t", got)
			}
		})
	}
	s.AssetFacts.HDRArtifacts[0].GenerationComplete = false
	if generatedHybridEvidenceIncluded(s, complete) {
		t.Fatal("inconclusive generation accepted as complete evidence")
	}
	s.AssetFacts.HDRArtifacts[0].GenerationComplete = true
	s.AssetFacts.HDRArtifacts[0].Kind = api.HDRArtifactPicturePlot
	if generatedHybridEvidenceIncluded(s, complete) {
		t.Fatal("generic picture plot accepted as grade comparison")
	}
	s.AssetFacts.HDRArtifacts[0].Kind = api.HDRArtifactGradePlot
	s.AssetFacts.HDRArtifacts[1].TargetID = "other-source"
	if generatedHybridEvidenceIncluded(s, complete) {
		t.Fatal("different targets combined")
	}
}

func TestUnknownHybridDVHDRIsGuidanceOnly(t *testing.T) {
	s := lstValidationSubject()
	s.LanguageFacts.Tracks = nil
	s.HDRFacts = api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}}
	found := false
	for _, failure := range assetReviewFailures(s) {
		if failure.Rule == "guidance_lst_hybrid_hdr" {
			found = true
			if failure.Disposition != api.RuleDispositionAdvisory {
				t.Fatal("guidance blocked release")
			}
		}
	}
	if !found {
		t.Fatal("missing conditional guidance")
	}
	s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	s.HDRFacts.Formats = []api.HDRFormat{api.HDRFormatHDR10}
	if hybridAssetsRequired(s) {
		t.Fatal("plain HDR10 gained hybrid evidence rule")
	}
	s.HDRFacts = api.HDRFacts{}
	if hybridAssetsRequired(s) {
		t.Fatal("unknown HDR facts assumed applicable rule")
	}
}

func TestHybridPromiseCannotCreateLateQuestionOrEvidence(t *testing.T) {
	meta := api.UploadSubject{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid}, HDRFacts: api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}}}
	description := lstDescriptionScreenshots(&meta)
	subject := api.NewTrackerValidationSubject(meta, "LST")
	key := assetQuestionKey(subject, "hybrid_will_add")
	for _, answer := range []string{"", "will_add_required_data"} {
		meta.TrackerQuestionnaireAnswers["LST"][key] = answer
		if err := validateFinalDescription(meta, description, "MediaInfo", ""); err != nil {
			t.Fatalf("late reassurance gate returned: %v", err)
		}
		subject = api.NewTrackerValidationSubject(meta, "LST")
		if generatedHybridEvidenceIncluded(subject, description) {
			t.Fatal("promise manufactured artifact evidence")
		}
		requireLSTValidationFailure(t, assetReviewFailures(subject), "guidance_lst_hybrid_hdr_evidence", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	}
	meta.HDRFacts.Status = api.HDREvidencePartial
	if err := validateFinalDescription(meta, description, "MediaInfo", ""); err != nil {
		t.Fatalf("unknown source evidence reinstated promise gate: %v", err)
	}
}
