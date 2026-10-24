// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceBoundAssetGuidance(t *testing.T) {
	s := lstValidationSubject()
	s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	s.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision}, Status: api.HDREvidenceComplete}
	s.QuestionnaireAnswers = map[string]string{assetQuestionKey(s, "hybrid_will_add"): "will_add_required_data"}
	for range 2 {
		failures := assetReviewFailures(s)
		requireLSTValidationFailure(t, failures, "guidance_lst_hybrid_hdr_evidence", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		for _, f := range failures {
			if f.QuestionnaireInput {
				t.Fatalf("guidance needs answer: %+v", f)
			}
		}
		s.Identity.Generation++
	}
}

func TestScreenshotEvidenceDoesNotTrustCountOrExtension(t *testing.T) {
	s := lstValidationSubject()
	s.AssetFacts.Screenshots = api.AssetEvidence{
		Ready:  true,
		Count:  3,
		Status: api.MetadataEvidenceStatusComplete,
	}
	s.QuestionnaireAnswers = map[string]string{assetQuestionKey(s, "screenshot_preservation"): "lossless_preserved"}
	requireLSTValidationFailure(t, assetReviewFailures(s), "guidance_lst_screenshot_format", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	s.AssetFacts.Screenshots.InspectedFormatCount = 3
	s.AssetFacts.Screenshots.LosslessCount = 2
	requireLSTValidationFailure(t, assetReviewFailures(s), "lst_screenshot_format", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
}

func TestMissingTrackLanguagesUseCanonicalCorrectionGuidance(t *testing.T) {
	for _, kind := range []api.MediaTrackKind{api.MediaTrackAudio, api.MediaTrackSubtitle} {
		s := lstValidationSubject()
		for i := range s.LanguageFacts.Tracks {
			if s.LanguageFacts.Tracks[i].Kind == kind {
				s.LanguageFacts.Tracks[i].Languages = nil
			}
		}
		found := false
		for _, f := range presentationReviewFailures(s) {
			if f.Rule == "language_presentation_tags" {
				found = true
				if f.QuestionnaireInput || !strings.Contains(f.Reason, "shared track-language correction") {
					t.Fatalf("redundant language input: %+v", f)
				}
			}
		}
		if !found {
			t.Fatal("missing canonical correction guidance")
		}
	}
}
