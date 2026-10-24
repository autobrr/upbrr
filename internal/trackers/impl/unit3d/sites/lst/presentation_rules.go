// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"cmp"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const trackPresentationHelp = "Review each prepared resource: correct audio language tags, subtitle language and forced flags, other default flags, and reported bitrates. Descriptive audio titles are recommended; subtitle ordering is optional. A description note does not correct an incorrect file tag."

func presentationReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var failures []api.RuleFailure
	{
		failure := trackers.LanguageRuleFailure(subject, "track_presentation", trackPresentationHelp, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	var untitledAudio []string
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind == api.MediaTrackAudio && strings.TrimSpace(track.Title) == "" {
			untitledAudio = append(untitledAudio, track.ResourceID+":"+track.ID)
		}
	}
	if len(untitledAudio) > 0 {
		failures = append(failures, trackers.LanguageRuleFailure(
			subject,
			"audio_titles",
			"audio tracks should have descriptive titles for context; missing title tags for tracks: "+strings.Join(untitledAudio, ", "),
			trackers.LanguageAdvisory,
		))
	}
	switch strings.ToUpper(strings.TrimSpace(subject.Type)) {
	case "REMUX", "ENCODE":
		failure := trackers.LanguageRuleFailure(
			subject,
			"source_documentation",
			"Include exact source references, relevant comparisons, processing logs and release notes in the description; explain genuinely inapplicable items there.",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	case "WEBDL", "WEB-DL":
		reason := "WEB release names should accurately identify the source service using your source records."
		if service := cmp.Or(strings.TrimSpace(subject.Service), strings.TrimSpace(subject.ServiceLongName)); service != "" {
			reason += " Resolved service: " + service
		}
		failure := trackers.LanguageRuleFailure(subject, "web_source_naming", reason, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	if tracks := tracksMissingLanguages(subject); len(tracks) > 0 {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"presentation_tags",
				"resolve the language using the shared track-language correction, then inspect the file tags for tracks: "+strings.Join(tracks, ", "),
				trackers.LanguageAdvisory,
			),
		)
		failures[len(failures)-1].EvidenceStatus = api.MetadataEvidenceStatusPartial
	}
	return failures
}
