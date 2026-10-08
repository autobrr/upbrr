// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package ulcx

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules retains ULCX's independent media and release-type requirements.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{RequireValidMISetting: true, BlockDVDRip: true}
}
func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	policy := trackers.LanguagePolicy{
		ExtraDubs:        trackers.LanguageProhibited,
		EnglishSubtitles: "foreign_without_dub",
		MissingSubtitles: trackers.LanguageProhibited,
		SubtitleDefault:  trackers.LanguageAdvisory,
	}
	if subject.PersonalRelease {
		policy.OriginalAudio = trackers.LanguageProhibited
		policy.SubtitleDefault = trackers.LanguageProhibited
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	if !subject.PersonalRelease && subject.LanguageFacts.OriginalLanguagesKnown &&
		subject.LanguageFacts.ProgrammeStatus == api.MetadataEvidenceStatusComplete &&
		subject.LanguageFacts.HasEnglishDub() && !subject.LanguageFacts.HasOriginalAudio() {
		failures = append(failures, trackers.LanguageRuleFailure(subject, "original_recommendation",
			"original audio should accompany the English dub", trackers.LanguageAdvisory))
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		var key, reason string
		switch track.Role {
		case api.AudioRoleAlternateMix:
			key, reason = "alternate_mix", "alternate mix "+track.ID+" should be meaningfully unique; source uniqueness has not been verified"
		case api.AudioRoleCommentary, api.AudioRoleIsolatedScore:
			key, reason = "secondary_source", "secondary track "+track.ID+" must come from a retail source; source provenance has not been verified"
		case api.AudioRoleProgramme,
			api.AudioRoleCompatibility,
			api.AudioRoleHistorical,
			api.AudioRoleInterview,
			api.AudioRoleNovelty,
			api.AudioRoleDescription,
			api.AudioRoleVoiceOver:
			continue
		default:
			continue
		}
		failure := trackers.LanguageRuleFailure(subject, key, reason, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}

	facts := subject.LanguageFacts
	if facts.OriginalLanguagesKnown {
		foreign := !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX")
		for _, track := range facts.Tracks {
			if track.Kind != api.MediaTrackSubtitle {
				continue
			}
			if !foreign && (track.Default || !track.DefaultKnown) {
				outcome := policy.SubtitleDefault
				reason := "subtitles should not be default on non-foreign content: " + track.ID
				if !track.DefaultKnown {
					reason = "default status of subtitle track " + track.ID + " is unresolved; subtitles should not be default on non-foreign content"
					if subject.PersonalRelease {
						outcome = trackers.LanguageUnresolved
					}
				}
				failure := trackers.LanguageRuleFailure(subject, "subtitle_default", reason, outcome)
				if !track.DefaultKnown {
					failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
				}
				failures = append(failures, failure)
			}
			for _, value := range track.Languages {
				language, coverage := languageutil.SubtitleLanguageParts(value)
				if !track.Forced && !strings.EqualFold(coverage, "Forced") {
					continue
				}
				if !slices.Contains(facts.OriginalLanguages, language) && (language != "English" || !facts.HasEnglishDub()) {
					failures = append(
						failures,
						trackers.LanguageRuleFailure(
							subject,
							"forced_subtitle_language",
							"forced subtitle track "+track.ID+" uses a language outside the original and applicable English dub",
							trackers.LanguageProhibited,
						),
					)
				}
			}
		}
	}

	return append(failures, compatibilityFailures(subject)...)
}
