// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn/xxx is not allowed at BHD.",
	}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{
		OriginalPrimary:         true,
		OriginalAudio:           trackers.LanguageProhibited,
		ExtraDubs:               trackers.LanguageProhibited,
		CompatibilityOnlyTrueHD: true,
		CompatibilityCodecs:     []string{"DD", "AC-3"},
	}
}

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures := trackers.EvaluateLanguagePolicy(subject, languagePolicy())
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return failures
	}
	facts := subject.LanguageFacts
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	// Unknown evidence for a recommendation remains visible without promoting
	// the recommendation to a mandatory upload requirement.
	addUnresolvedGuidance := func(key, reason string) {
		failure := trackers.LanguageRuleFailure(subject, key, reason, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "existing_release")] {
	case "unchanged_or_new":
	case "tracks_or_chapters_added":
		add("existing_release", "adding audio, subtitles or chapters to an existing release requires staff approval", trackers.LanguageStaffException)
	default:
		add(
			"existing_release",
			"source history must establish whether audio, subtitles or chapters were added to an existing release",
			trackers.LanguageUnresolved,
		)
	}
	if needsProgrammeMixReview(facts) {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "programme_mixes")] {
		case "distinct_mixes":
		case "duplicate_main_mix":
			add(
				"redundant_original",
				"duplicate versions of a main mix in another format are prohibited except TrueHD Dolby Digital compatibility audio",
				trackers.LanguageProhibited,
			)
		default:
			add(
				"redundant_original",
				"same-language programme tracks need source/mix comparison; an alternate-mix label or different codec does not establish distinct content",
				trackers.LanguageUnresolved,
			)
		}
	}
	animated := subject.Anime || slices.ContainsFunc(subject.EffectiveMetadata.Genres, func(genre string) bool { return strings.EqualFold(genre, "Animation") })
	if animated && facts.OriginalLanguagesKnown && !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX") &&
		facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && facts.HasOriginalAudio() && !facts.HasEnglishDub() {
		add("animated_dual_audio", "original plus English dual audio is preferred over original-only audio for animated content", trackers.LanguageAdvisory)
	}
	if strings.EqualFold(subject.Type, "REMUX") {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "source_extras")] {
		case "retained_or_unavailable":
		case "incomplete":
			add("source_extras", "available source subtitles, commentary and chapters should accompany the remux", trackers.LanguageAdvisory)
		default:
			addUnresolvedGuidance(
				"source_extras",
				"source comparison is unresolved: check whether available subtitles, commentary and chapters accompany the remux",
			)
		}
		if needsForeignDialogueReview(facts) {
			answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "foreign_dialogue_subtitles")]
			switch {
			case answer == "not_required", answer == "included" && hasSeparateForcedEnglish(facts):
			case answer == "missing", answer == "included" && facts.SubtitleStatus == api.MetadataEvidenceStatusComplete:
				add("foreign_dialogue_subtitles", "English subtitles for foreign dialogue should be separate and forced", trackers.LanguageAdvisory)
			default:
				addUnresolvedGuidance(
					"foreign_dialogue_subtitles",
					"foreign-dialogue coverage is unresolved: check whether separate forced English subtitles are needed and included",
				)
			}
		}
	}
	return failures
}

// needsProgrammeMixReview compares tracks within one resource. Role labels and
// codec differences do not establish whether two tracks preserve distinct mixes.
func needsProgrammeMixReview(facts api.LanguageFacts) bool {
	seen := make(map[string][]string)
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		for _, language := range track.Languages {
			if slices.Contains(seen[track.ResourceID], language) {
				return true
			}
		}
		seen[track.ResourceID] = append(seen[track.ResourceID], track.Languages...)
	}
	return false
}

func needsForeignDialogueReview(facts api.LanguageFacts) bool {
	return !facts.AudioAbsent && (facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !slices.Equal(facts.ProgrammeLanguages, []string{"ZXX"}))
}

func hasSeparateForcedEnglish(facts api.LanguageFacts) bool {
	if !slices.Contains(facts.SubtitleLanguages, "English") {
		return false
	}
	return slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackSubtitle && slices.ContainsFunc(track.Languages, func(value string) bool {
			language, coverage := languageutil.SubtitleLanguageParts(value)
			return language == "English" && (track.Forced || strings.EqualFold(coverage, "Forced"))
		})
	})
}
