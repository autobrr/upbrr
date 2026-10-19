// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"crypto/sha256"
	"fmt"
	"html"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

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

func hybridAssetsRequired(subject api.TrackerValidationSubject) bool {
	return slices.Contains(subject.ReleaseFeatures, api.ReleaseFeatureHybrid) &&
		(slices.Contains(subject.HDRFacts.Formats, api.HDRFormatDolbyVision) || slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10Plus))
}

func assetReviewQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	var fields []api.TrackerQuestionnaireField
	if subject.AssetFacts.HostedScreenshots.Ready {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      assetQuestionKey(subject, "screenshot_preservation"),
			Label:    "Review hosted screenshot preservation",
			Kind:     "select",
			Options:  []string{"unresolved", "lossless_preserved", "lossy_or_changed"},
			Required: true,
			Help:     "Inspect the full-resolution hosted images against the prepared screenshots. Confirm their lossless format and preservation; a filename or thumbnail alone is insufficient.",
		})
	}
	if tracks := tracksMissingLanguages(subject); len(tracks) > 0 {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:      assetQuestionKey(subject, "track_notes"),
				Label:    "Clarify tracks with missing languages",
				Kind:     "text",
				Required: true,
				Help:     "Identify the language and role of each track with a missing language tag: " + strings.Join(tracks, ", "),
			},
		)
	}
	if hybridAssetsRequired(subject) && !generatedHybridEvidenceIncluded(subject, subject.DescriptionOverride) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:                assetQuestionKey(subject, "hybrid_will_add"),
			Label:              "Include hybrid HDR evidence",
			Kind:               "select",
			Options:            []string{"unresolved", "will_add_required_data"},
			Required:           subject.DescriptionGroupsFinal,
			HDRGradeComparison: true,
			Help:               "Confirm that you will add the grade comparison and HDR10+ plot for this source to the description. If upbrr has already generated and included both, no confirmation is needed.",
		})
	}
	for i := range fields {
		value := strings.TrimSpace(subject.QuestionnaireAnswers[fields[i].Key])
		fields[i].Value = value
		fields[i].NeedsReview = fields[i].Required && (value == "" || value == "unresolved" || value == "lossy_or_changed" || value == "source_mismatch")
	}
	return fields
}

func assetReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	images := subject.AssetFacts.Screenshots
	if subject.AssetFacts.HostedScreenshots.Ready && images.LosslessCount < 3 {
		status := api.MetadataEvidenceStatusPartial
		if images.Ready && images.InspectedFormatCount == images.Count {
			status = api.MetadataEvidenceStatusComplete
		}
		failures = append(
			failures,
			trackers.NewEvidenceRuleFailure(
				"lst_screenshot_format",
				"three decoded lossless screenshots are required; unknown formats require inspection",
				api.RuleDispositionStrict,
				status,
			),
		)
	}
	for _, field := range assetReviewQuestions(subject) {
		// Hybrid evidence is enforced against the final payload description, with
		// a recoverable late question when generated evidence is not included.
		if field.HDRGradeComparison {
			continue
		}
		value := strings.TrimSpace(subject.QuestionnaireAnswers[field.Key])
		valid := value != "" && value != "unresolved" && value != "lossy_or_changed" && value != "source_mismatch"
		if field.Kind == "text" {
			valid = value != ""
		} else {
			valid = valid && slices.Contains(field.Options, value)
		}
		if valid {
			continue
		}
		failure := trackers.NewQuestionnaireRuleFailure("lst_asset_review", field.Label+" requires current source-bound evidence and review")
		if value == "lossy_or_changed" || value == "source_mismatch" {
			failure.EvidenceStatus = api.MetadataEvidenceStatusComplete
		} else {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	if !slices.Contains(subject.ReleaseFeatures, api.ReleaseFeatureHybrid) &&
		slices.Contains(subject.HDRFacts.Formats, api.HDRFormatDolbyVision) &&
		(slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10) || slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10Plus)) {
		failures = append(
			failures,
			trackers.NewEvidenceRuleFailure(
				"lst_hybrid_hdr_guidance",
				"DV+HDR does not establish hybrid provenance. If this release is a hybrid, select Hybrid in Release features and include the required source-linked HDR evidence in its description.",
				api.RuleDispositionAdvisory,
				api.MetadataEvidenceStatusPartial,
			),
		)
	}
	for i := range failures {
		failures[i].DebugBypass = true
	}
	return failures
}

func escapedDescriptionEvidence(value string) string {
	return strings.NewReplacer("[", "&#91;", "]", "&#93;").Replace(html.EscapeString(value))
}

func requiredAssetDescription(subject api.TrackerValidationSubject) string {
	return escapedDescriptionEvidence(strings.TrimSpace(subject.QuestionnaireAnswers[assetQuestionKey(subject, "track_notes")]))
}

func tracksMissingLanguages(subject api.TrackerValidationSubject) []string {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var tracks []string
	for _, track := range subject.LanguageFacts.Tracks {
		if (track.Kind == api.MediaTrackAudio || track.Kind == api.MediaTrackSubtitle) && len(track.Languages) == 0 {
			tracks = append(tracks, track.ResourceID+":"+track.ID)
		}
	}
	return tracks
}
