// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRetiredReassuranceAnswersCannotChangeFindings(t *testing.T) {
	for _, personal := range []bool{false, true} {
		s := lstValidationSubject()
		s.PersonalRelease = personal
		s.Type = "ENCODE"
		s.LanguageFacts.Tracks = []api.MediaTrackFacts{{
			ID:            "audio-0",
			ResourceID:    "primary",
			Kind:          api.MediaTrackAudio,
			Role:          api.AudioRoleAlternateMix,
			Codec:         "FLAC",
			BitDepth:      16,
			ChannelLayout: "2.0",
		}}
		before := append(presentationReviewFailures(s), alternateMixFailures(s)...)
		audioBefore := audioTierFailures(s)
		for _, value := range []string{"native_16_bit", "reduced_without_dither", "reduced_dither_unknown", "additional_original_or_unique_remix", "not_unique", "reviewed_correct", "corrections_needed", "will_add_required_data"} {
			s.QuestionnaireAnswers = map[string]string{
				trackers.LanguageQuestionKey(s, "audio_processing_audio-0"):      value,
				trackers.LanguageQuestionKey(s, "alternate_mix_primary_audio-0"): value,
			}
			s.QuestionnaireAnswers[trackers.LanguageQuestionKey(s, "presentation_source_documentation_will_add_legacy")] = value
			after := append(presentationReviewFailures(s), alternateMixFailures(s)...)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(audioBefore, audioTierFailures(s)) {
				t.Fatalf("retired answer %q changed findings for personal=%t", value, personal)
			}
		}
	}
}

func TestInspectedScreenshotProblemIsWaivableNotQuestionnaire(t *testing.T) {
	s := lstValidationSubject()
	s.AssetFacts.Screenshots = api.AssetEvidence{
		Ready:                true,
		Count:                3,
		InspectedFormatCount: 3,
		LosslessCount:        2,
		Status:               api.MetadataEvidenceStatusComplete,
	}
	failures := assetReviewFailures(s)
	requireLSTValidationFailure(t, failures, "lst_screenshot_format", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	for _, f := range failures {
		if f.QuestionnaireInput {
			t.Fatalf("warning generated questionnaire: %+v", f)
		}
	}
	s.AssetFacts.Screenshots.InspectedFormatCount = 0
	failures = assetReviewFailures(s)
	requireLSTValidationFailure(t, failures, "guidance_lst_screenshot_format", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
}

// assetQuestionKey reproduces retired saved keys only to prove they convey no authority.
func assetQuestionKey(subject api.TrackerValidationSubject, key string) string {
	if key == "screenshot_preservation" {
		return trackers.LanguageQuestionKey(subject, key+"_"+string(subject.AssetFacts.ScreenshotFingerprint))
	}
	if key == "hybrid_will_add" {
		// A source-specific promise must survive later screenshot/hosting stages.
		return trackers.LanguageQuestionKey(
			subject,
			fmt.Sprintf("%s_%x", key, sha256.Sum256([]byte(fmt.Sprintf("%#v|%#v", subject.HDRFacts, subject.ReleaseFeatures)))),
		)
	}
	return trackers.LanguageQuestionKey(
		subject,
		fmt.Sprintf("%s_%x", key, sha256.Sum256([]byte(fmt.Sprintf("%s|%#v|%#v", subject.PreparedResourceFingerprint, subject.HDRFacts, subject.AssetFacts)))),
	)
}

func TestAssetWarningsBindActualEvidenceAndDescription(t *testing.T) {
	s := lstValidationSubject()
	s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	s.HDRFacts = api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}}
	s.DescriptionGroupsFinal = true
	s.AssetFacts.HDRArtifacts = []api.HDRArtifactEvidence{
		{
			GenerationComplete: true,
			Kind:               api.HDRArtifactGradePlot,
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
	failures := assetReviewFailures(s)
	requireLSTValidationFailure(t, failures, "lst_hybrid_hdr_evidence", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("LST", failures)
	if err != nil {
		t.Fatal(err)
	}
	s.DescriptionOverride = "Changed final description"
	changed, err := trackers.WaivableRuleFailureFingerprint("LST", assetReviewFailures(s))
	if err != nil || changed == fingerprint {
		t.Fatalf("changed description retained warning authority: %v", err)
	}
	s.Identity.Generation++
	next, err := trackers.WaivableRuleFailureFingerprint("LST", assetReviewFailures(s))
	if err != nil || next == changed {
		t.Fatalf("changed generation retained warning authority: %v", err)
	}
	s.AssetFacts.HDRArtifacts[0].GenerationComplete = false
	requireLSTValidationFailure(t, assetReviewFailures(s), "guidance_lst_hybrid_hdr_evidence", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	s.AssetFacts.HDRArtifacts[0].GenerationComplete = true
	s.DescriptionOverride = "[img]https://images.example/grade.png[/img][img]https://images.example/hdr.png[/img]"
	for _, failure := range assetReviewFailures(s) {
		if failure.Rule == "lst_hybrid_hdr_evidence" {
			t.Fatalf("included evidence still blocked: %+v", failure)
		}
	}
}

func TestDescriptionImageCountDoesNotReintroduceLosslessPolicyGate(t *testing.T) {
	meta := api.UploadSubject{}
	description := lstDescriptionScreenshots(&meta)
	meta.ExactMedia.Screenshots[0].Format = "jpeg"
	if err := validateFinalDescription(meta, description, "MediaInfo", ""); err != nil {
		t.Fatalf("format warning became strict description gate: %v", err)
	}
	if got := countPreparedDescriptionImages(meta, description); got != 3 {
		t.Fatalf("actual prepared images=%d", got)
	}
}
