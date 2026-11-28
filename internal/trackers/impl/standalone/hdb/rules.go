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
	remux := strings.EqualFold(subject.Type, "REMUX")
	policy := trackers.LanguagePolicy{ExtraDubs: trackers.LanguageProhibited, DisallowedRoles: []api.AudioTrackRole{api.AudioRoleDescription}}
	if remux {
		policy.OriginalAudio = trackers.LanguageProhibited
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	animated := subject.Anime ||
		slices.ContainsFunc(subject.EffectiveMetadata.Genres, func(genre string) bool { return strings.EqualFold(genre, "Animation") })
	facts := subject.LanguageFacts
	englishDub := facts.HasEnglishDub()
	if !englishDub && !animated && slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
		return facts.ProgrammeAudioOrigin(track) == api.TrackAudioDubbed && languageutil.ContainsLanguage(track.Languages, "English")
	}) {
		failures = append(failures, trackers.LanguageRuleFailure(
			subject,
			"english_dub_evidence",
			"Audio languages is cleared or inconsistent with an inspected English dub; correct or reset that correction before assessing the English-dub restriction",
			trackers.LanguageUnresolved,
		))
	}
	if !animated && (!remux || facts.HasOriginalAudio()) && trackers.ForeignLanguageContent(facts) &&
		languageutil.ContainsLanguage(facts.ProgrammeLanguages, "English") {
		englishSubject := subject
		englishSubject.LanguageFacts.Tracks = nil
		for _, track := range facts.Tracks {
			if languageutil.ContainsLanguage(track.Languages, "English") {
				// Resolve missing origins before discarding paired original-language evidence.
				if track.AudioOrigin == "" {
					track.AudioOrigin = facts.ProgrammeAudioOrigin(track)
				}
				englishSubject.LanguageFacts.Tracks = append(englishSubject.LanguageFacts.Tracks, track)
			}
		}
		if len(englishSubject.LanguageFacts.Tracks) > 0 {
			failures = append(failures, trackers.ProgrammeAudioOriginFailures(englishSubject)...)
		}
	}
	if englishDub && !animated {
		outcome := trackers.LanguageProhibited
		if len(subject.EffectiveMetadata.Genres) == 0 {
			outcome = trackers.LanguageUnresolved
		}
		failures = append(failures, trackers.LanguageRuleFailure(subject, "english_dub", "English dubs require established animated content", outcome))
	}
	if remux {
		failure := trackers.LanguageRuleFailure(
			subject,
			"source_audio",
			"retain the best available original-language soundtrack; if the source has an original mono/stereo mix and surround upmix, retain both with the original mix default. Source availability cannot be established from the upload alone",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	if facts.HasEnglishOriginalAudio() {
		if reason, status := forcedEnglishSubtitleDefaultIssue(facts); reason != "" {
			failure := trackers.NewEvidenceRuleFailure("language_foreign_dialogue_subtitles", reason, api.RuleDispositionWaivable, status)
			failure.DebugBypass = true
			failure.EvidenceFingerprint = api.WorkflowFingerprint(trackers.LanguageQuestionKey(subject, "foreign_dialogue_subtitles"))
			failures = append(failures, failure)
		}
	}
	return failures
}

func forcedEnglishSubtitleDefaultIssue(facts api.LanguageFacts) (string, api.MetadataEvidenceStatus) {
	if !languageutil.ContainsLanguage(facts.SubtitleLanguages, "English") {
		return "", ""
	}
	unknownDefault := false
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackSubtitle {
			continue
		}
		forcedEnglish := slices.ContainsFunc(track.Languages, func(value string) bool {
			language, coverage := languageutil.SubtitleLanguageParts(value)
			return languageutil.SameLanguage(language, "English") && (track.Forced || strings.EqualFold(coverage, "Forced"))
		})
		if !forcedEnglish {
			continue
		}
		if !track.DefaultKnown {
			unknownDefault = true
		} else if !track.Default {
			return "each forced English subtitle track must be set as default", api.MetadataEvidenceStatusComplete
		}
	}
	if unknownDefault {
		return "the default flag of a forced English subtitle track is unresolved", api.MetadataEvidenceStatusPartial
	}
	return "", ""
}
