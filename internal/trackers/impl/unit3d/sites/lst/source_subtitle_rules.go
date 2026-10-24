// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func remuxSubtitleFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !strings.EqualFold(subject.Type, "REMUX") {
		return nil
	}
	failure := trackers.LanguageRuleFailure(
		subject,
		"retail_subtitles",
		"Retain applicable retail English subtitles and retail forced-English subtitles for non-English dialogue or on-screen text in English-language content. Compare every prepared resource with its retail source; preserved retail hardcoded forced subtitles can satisfy forced coverage. Missing retail tracks may make the release trumpable.",
		trackers.LanguageAdvisory,
	)
	failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
	return []api.RuleFailure{failure}
}

func alternateMixFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleAlternateMix {
			continue
		}
		failure := trackers.LanguageRuleFailure(
			subject,
			"alternate_mix",
			"resource "+track.ResourceID+", track "+track.ID+": retain an additional original mix or unique remix; the final track's labels do not establish source-mix uniqueness",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	return failures
}
