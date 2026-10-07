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
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "source_audio")] {
		case "best_original_retained":
		case "source_mix_pair_retained":
			failures = append(failures, sourceMixPairFailures(subject)...)
		case "incomplete":
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"source_audio",
					"the best original soundtrack or a required original/source upmix is missing",
					trackers.LanguageProhibited,
				),
			)
		default:
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"source_audio",
					"source evidence must establish the best original soundtrack and any required original mono/stereo plus upmix",
					trackers.LanguageUnresolved,
				),
			)
		}
	}
	if needsForeignDialogueReview(subject.LanguageFacts) {
		outcome := trackers.LanguageUnresolved
		reason := "foreign-dialogue subtitle need and inclusion require review"
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "foreign_dialogue_subtitles")] {
		case "not_required":
			outcome = ""
		case "missing":
			outcome = trackers.LanguageProhibited
			reason = "required foreign-dialogue subtitles are missing"
		case "included":
			included, unknownDefault := forcedDefaultSubtitleEvidence(subject.LanguageFacts)
			if included {
				outcome = ""
			} else if subject.LanguageFacts.SubtitleStatus == api.MetadataEvidenceStatusComplete && !unknownDefault {
				outcome = trackers.LanguageProhibited
				reason = "required foreign-dialogue subtitles must be included, forced and default"
			}
		}
		if outcome != "" {
			failures = append(failures, trackers.LanguageRuleFailure(subject, "foreign_dialogue_subtitles", reason, outcome))
		}
	}
	return failures
}

// sourceMixPairFailures checks necessary track/default facts only after the
// uploader establishes that the source contains the original/upmix pair.
func sourceMixPairFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	facts := subject.LanguageFacts
	if !facts.OriginalLanguagesKnown || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		return nil // The shared original/programme evidence findings already block.
	}
	monoStereo, surround, defaultOriginal, unknownChannels, unknownDefault := false, false, false, false, false
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) ||
			!slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(facts.OriginalLanguages, language) }) {
			continue
		}
		switch {
		case track.Channels <= 0:
			unknownChannels = true
		case track.Channels <= 2:
			monoStereo = true
			defaultOriginal = defaultOriginal || track.DefaultKnown && track.Default
			unknownDefault = unknownDefault || !track.DefaultKnown
		default:
			surround = true
		}
	}
	outcome := trackers.LanguageProhibited
	if unknownChannels {
		outcome = trackers.LanguageUnresolved
	}
	if !monoStereo || !surround {
		return []api.RuleFailure{
			trackers.LanguageRuleFailure(
				subject,
				"source_mix_pair",
				"the reviewed source original mono/stereo and surround upmix are not both established in the upload",
				outcome,
			),
		}
	}
	if !defaultOriginal {
		if unknownDefault {
			outcome = trackers.LanguageUnresolved
		}
		return []api.RuleFailure{trackers.LanguageRuleFailure(subject, "original_mix_default", "the source original mono/stereo mix must be default", outcome)}
	}
	return nil
}

func needsForeignDialogueReview(facts api.LanguageFacts) bool {
	return !facts.AudioAbsent && (facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !slices.Equal(facts.ProgrammeLanguages, []string{"ZXX"}))
}

// forcedDefaultSubtitleEvidence distinguishes a known qualifying default from
// an otherwise eligible subtitle whose inspected default flag is unknown.
func forcedDefaultSubtitleEvidence(facts api.LanguageFacts) (bool, bool) {
	unknownDefault := false
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
		if track.DefaultKnown && track.Default {
			return true, false
		}
		unknownDefault = unknownDefault || !track.DefaultKnown
	}
	return false, unknownDefault
}
