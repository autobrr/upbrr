// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package hdb

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	policy := trackers.LanguagePolicy{ExtraDubs: trackers.LanguageProhibited, DisallowedRoles: []api.AudioTrackRole{api.AudioRoleDescription}}
	if strings.EqualFold(subject.Type, "REMUX") {
		policy.OriginalAudio = trackers.LanguageProhibited
		policy.OriginalPrimary = true
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	if subject.LanguageFacts.HasEnglishDub() {
		animated := subject.Anime ||
			slices.ContainsFunc(subject.EffectiveMetadata.Genres, func(genre string) bool { return strings.EqualFold(genre, "Animation") })
		if !animated {
			outcome := trackers.LanguageProhibited
			if len(subject.EffectiveMetadata.Genres) == 0 {
				outcome = trackers.LanguageUnresolved
			}
			failures = append(failures, trackers.LanguageRuleFailure(subject, "english_dub", "English dubs require established animated content", outcome))
		}
	}
	if strings.EqualFold(subject.Type, "REMUX") {
		failure := trackers.LanguageRuleFailure(
			subject,
			"source_audio",
			"retain the best available original-language soundtrack; if the source has an original mono/stereo mix and surround upmix, retain both with the original mix default. Source availability cannot be established from the upload alone",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	if needsForeignDialogueReview(subject.LanguageFacts) {
		hasForced, defaultPresent, unknownDefault := forcedDefaultSubtitleEvidence(subject.LanguageFacts)
		if !defaultPresent {
			outcome := trackers.LanguageAdvisory
			reason := "if foreign dialogue requires subtitles, include a forced/default subtitle track; dialogue coverage cannot be established from track metadata alone"
			if hasForced {
				outcome = trackers.LanguageProhibited
				reason = "the inspected forced subtitles must include a default track"
				if unknownDefault {
					outcome = trackers.LanguageUnresolved
					reason = "the default flag of the inspected forced subtitles is unresolved"
				}
			}
			failure := trackers.LanguageRuleFailure(subject, "foreign_dialogue_subtitles", reason, outcome)
			if !hasForced {
				failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
			}
			failures = append(failures, failure)
		}
	}
	return failures
}

func needsForeignDialogueReview(facts api.LanguageFacts) bool {
	return !facts.AudioAbsent && (facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !slices.Equal(facts.ProgrammeLanguages, []string{"ZXX"}))
}

// forcedDefaultSubtitleEvidence checks only forced tracks supported by current
// subtitle-language facts. One known default satisfies the track requirement.
func forcedDefaultSubtitleEvidence(facts api.LanguageFacts) (hasForced, defaultPresent, unknownDefault bool) {
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackSubtitle {
			continue
		}
		eligible := slices.ContainsFunc(track.Languages, func(value string) bool {
			language, coverage := languageutil.SubtitleLanguageParts(value)
			code := languageutil.NormalizeLanguageCode(language)
			return code != "" && code != "und" && code != "mul" &&
				(track.Forced || strings.EqualFold(coverage, "Forced")) && slices.Contains(facts.SubtitleLanguages, language)
		})
		if !eligible {
			continue
		}
		hasForced = true
		if track.DefaultKnown && track.Default {
			return true, true, false
		}
		unknownDefault = unknownDefault || !track.DefaultKnown
	}
	return hasForced, false, unknownDefault
}
