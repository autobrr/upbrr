// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules strictly requires a movie category; its adult-content failure
// remains waivable.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:       true,
		AdultMessage:     "Porn is not allowed",
		RequireMovieOnly: true,
	}
}

func languageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	var failures []api.RuleFailure
	if !facts.OriginalLanguagesKnown {
		return []api.RuleFailure{
			trackers.LanguageRuleFailure(
				subject,
				"subtitle_evidence",
				"original-language evidence is required for the subtitle/intertitle assessment",
				trackers.LanguageUnresolved,
			),
		}
	}
	if facts.SubtitleStatus != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{
			trackers.LanguageRuleFailure(subject, "subtitle_evidence", "complete subtitle presentation evidence is required", trackers.LanguageUnresolved),
		}
	}
	if len(facts.SubtitleLanguages) == 0 &&
		slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool { return track.Kind == api.MediaTrackSubtitle }) {
		return []api.RuleFailure{
			trackers.LanguageRuleFailure(subject, "subtitle_evidence", "embedded subtitle languages were cleared and need review", trackers.LanguageUnresolved),
		}
	}
	englishOriginal := slices.Contains(facts.OriginalLanguages, "English")
	if !englishOriginal && !slices.Contains(facts.SubtitleLanguages, "English") {
		failure := trackers.LanguageRuleFailure(
			subject,
			"retail_subtitles",
			"English subtitles are absent; if normally supplied or expected from the retail source, their absence makes the release trumpable",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	predominantlyEnglish := englishOriginal && len(facts.OriginalLanguages) == 1
	if rfNeedsPredominanceEvidence(subject) {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "predominantly_english")] {
		case "yes":
			predominantlyEnglish = true
		case "no":
		default:
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"predominance_evidence",
					"confirm whether the film is predominantly English before applying the forced-English subtitle exception",
					trackers.LanguageUnresolved,
				),
			)
		}
	}
	presentationDefect := false
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackSubtitle &&
			(!predominantlyEnglish || !slices.Contains(facts.SubtitleLanguages, "English") || !rfForcedEnglishTrack(track)) {
			presentationDefect = true
		}
	}
	if subject.HardcodedSubs {
		forcedEnglish := predominantlyEnglish && len(subject.HardcodedSubtitleCoverage) == 1 && subject.HardcodedSubtitleCoverage[0].Language == "English" &&
			subject.HardcodedSubtitleCoverage[0].Coverage == api.SubtitleCoverageForced
		if !forcedEnglish {
			presentationDefect = true
		}
	}
	if presentationDefect {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"subtitle_presentation",
				"hardcoded or embedded subtitles are trumpable except forced English for foreign dialogue in a predominantly English film",
				trackers.LanguageTrumpable,
			),
		)
	}
	return failures
}

func rfForcedEnglishTrack(track api.MediaTrackFacts) bool {
	if len(track.Languages) == 0 {
		return false
	}
	for _, value := range track.Languages {
		language, coverage := languageutil.SubtitleLanguageParts(value)
		if language != "English" || (!track.Forced && !strings.EqualFold(coverage, "Forced")) {
			return false
		}
	}
	return true
}

func rfNeedsPredominanceEvidence(subject api.TrackerValidationSubject) bool {
	facts := subject.LanguageFacts
	if !facts.OriginalLanguagesKnown || len(facts.OriginalLanguages) < 2 || !slices.Contains(facts.OriginalLanguages, "English") {
		return false
	}
	if slices.Contains(facts.SubtitleLanguages, "English") && slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackSubtitle && rfForcedEnglishTrack(track)
	}) {
		return true
	}
	return subject.HardcodedSubs && len(subject.HardcodedSubtitleCoverage) == 1 && subject.HardcodedSubtitleCoverage[0].Language == "English" &&
		subject.HardcodedSubtitleCoverage[0].Coverage == api.SubtitleCoverageForced
}
