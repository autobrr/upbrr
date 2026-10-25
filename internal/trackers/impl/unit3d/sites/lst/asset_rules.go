// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func hybridAssetsRequired(subject api.TrackerValidationSubject) bool {
	return slices.Contains(subject.ReleaseFeatures, api.ReleaseFeatureHybrid) &&
		(slices.Contains(subject.HDRFacts.Formats, api.HDRFormatDolbyVision) || slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10Plus))
}

func assetReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	subject = withImportedReferenceFacts(subject)
	var failures []api.RuleFailure
	images := subject.AssetFacts.Screenshots
	hosted := subject.AssetFacts.HostedScreenshots
	inspected := images.Ready && images.Status == api.MetadataEvidenceStatusComplete && images.Count > 0 && images.InspectedFormatCount == images.Count
	if (subject.AssetFacts.Status == api.MetadataEvidenceStatusComplete || subject.ImportedScreenshotEvidence != nil) &&
		(!images.Ready || images.Status != api.MetadataEvidenceStatusComplete || images.LosslessCount < 3 ||
			!hosted.Ready || hosted.Status != api.MetadataEvidenceStatusComplete || hosted.Count < 3) {
		status := api.MetadataEvidenceStatusPartial
		disposition := api.RuleDispositionAdvisory
		if inspected && hosted.Status == api.MetadataEvidenceStatusComplete {
			status = api.MetadataEvidenceStatusComplete
			disposition = api.RuleDispositionWaivable
		}
		rule := "guidance_lst_screenshot_format"
		if disposition == api.RuleDispositionWaivable {
			rule = "lst_screenshot_format"
		}
		reason := "Screenshot format evidence is incomplete."
		if images.Status == api.MetadataEvidenceStatusComplete {
			reason = fmt.Sprintf("%d of %d prepared screenshots have verified lossless image data (%d confirmed JPEG, %d format unknown).",
				images.LosslessCount, images.Count, images.InspectedFormatCount-images.LosslessCount, images.Count-images.InspectedFormatCount)
		}
		if hosted.Status == api.MetadataEvidenceStatusComplete {
			reason += fmt.Sprintf(" %d distinct screenshots have usable hosted images.", hosted.Count)
		} else {
			reason += " Hosted screenshot evidence is incomplete."
		}
		reason += " LST asks for at least three lossless images; review the available evidence before upload."
		failures = append(failures, trackers.NewEvidenceRuleFailure(rule, reason, disposition, status))
		failures[len(failures)-1].EvidenceFingerprint, _ = api.CanonicalWorkflowFingerprint(struct {
			Identity    api.ExternalIdentity
			Screenshots api.WorkflowFingerprint
			Evidence    api.AssetEvidence
			Hosted      api.AssetEvidence
		}{subject.Identity, subject.AssetFacts.ScreenshotFingerprint, images, hosted})
	}
	if subject.DescriptionGroupsFinal {
		count := countDescriptionScreenshotReferences(subject.AssetFacts.ScreenshotReferences, subject.DescriptionOverride)
		if count < 3 {
			rule := "guidance_lst_screenshot_description"
			disposition := api.RuleDispositionAdvisory
			status := api.MetadataEvidenceStatusPartial
			reason := "The available screenshot references do not establish three distinct prepared images in the final description."
			if inspected && hosted.Status == api.MetadataEvidenceStatusComplete && screenshotFullImageReferencesKnown(subject.AssetFacts.ScreenshotReferences) {
				rule = "lst_screenshot_description"
				disposition = api.RuleDispositionWaivable
				status = api.MetadataEvidenceStatusComplete
				reason = fmt.Sprintf(
					"The final description includes %d distinct prepared screenshots; LST asks for at least three full images or linked thumbnails.",
					count,
				)
			}
			failure := trackers.NewEvidenceRuleFailure(rule, reason, disposition, status)
			failure.EvidenceFingerprint, _ = api.CanonicalWorkflowFingerprint(struct {
				Identity    api.ExternalIdentity
				Screenshots api.WorkflowFingerprint
				References  []api.ScreenshotReference
				Description string
			}{subject.Identity, subject.AssetFacts.ScreenshotFingerprint, subject.AssetFacts.ScreenshotReferences, subject.DescriptionOverride})
			failures = append(failures, failure)
		}
	}
	if hybridAssetsRequired(subject) && !generatedHybridEvidenceIncluded(subject, subject.DescriptionOverride) {
		disposition := api.RuleDispositionAdvisory
		status := api.MetadataEvidenceStatusPartial
		reason := "Include source-linked hybrid grade comparisons and HDR10+ plots in the description; the available evidence does not establish their inclusion or comparative quality."
		if subject.DescriptionGroupsFinal && subject.HDRFacts.Status == api.HDREvidenceComplete && len(subject.AssetFacts.HDRArtifacts) > 0 &&
			!slices.ContainsFunc(subject.AssetFacts.HDRArtifacts, func(a api.HDRArtifactEvidence) bool { return !a.GenerationComplete }) {
			disposition = api.RuleDispositionWaivable
			status = api.MetadataEvidenceStatusComplete
			reason = "The final description does not include the complete selected source-linked hybrid grade comparison and HDR10+ plot set; include the generated artifacts before upload."
		}
		rule := "guidance_lst_hybrid_hdr_evidence"
		if disposition == api.RuleDispositionWaivable {
			rule = "lst_hybrid_hdr_evidence"
		}
		failure := trackers.NewEvidenceRuleFailure(rule, reason, disposition, status)
		failure.EvidenceFingerprint, _ = api.CanonicalWorkflowFingerprint(struct {
			Identity    api.ExternalIdentity
			HDR         api.HDRFacts
			Assets      api.AssetFacts
			Description string
		}{subject.Identity, subject.HDRFacts, subject.AssetFacts, subject.DescriptionOverride})
		failures = append(failures, failure)
	}
	if !slices.Contains(subject.ReleaseFeatures, api.ReleaseFeatureHybrid) &&
		slices.Contains(subject.HDRFacts.Formats, api.HDRFormatDolbyVision) &&
		(slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10) || slices.Contains(subject.HDRFacts.Formats, api.HDRFormatHDR10Plus)) {
		failures = append(
			failures,
			trackers.NewEvidenceRuleFailure(
				"guidance_lst_hybrid_hdr",
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

// screenshotFullImageReferencesKnown requires a full URL for each hosted image
// identity. An additional thumbnail-only variant does not remove known evidence.
func screenshotFullImageReferencesKnown(references []api.ScreenshotReference) bool {
	known := make(map[api.WorkflowFingerprint]bool)
	for _, image := range references {
		if strings.TrimSpace(string(image.ID)) == "" {
			return false
		}
		known[image.ID] = known[image.ID] || validScreenshotFullURL(image.RawURL)
	}
	for _, hasFullURL := range known {
		if !hasFullURL {
			return false
		}
	}
	return len(known) > 0
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
