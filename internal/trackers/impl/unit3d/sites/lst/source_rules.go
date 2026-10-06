// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type audioDefect struct{ key, reason string }

// audioDefects identifies drawbacks without asserting that the conditional
// trumpability requirements or any independent prohibition have been satisfied.
func audioDefects(subject api.TrackerValidationSubject) []audioDefect {
	facts := subject.LanguageFacts
	var defects []audioDefect
	if facts.OriginalLanguagesKnown {
		for _, language := range facts.ProgrammeLanguages {
			if language == "English" || slices.Contains(facts.OriginalLanguages, language) {
				continue
			}
			key, label := "extra_dub", "Redundant Audio Tracks — unjustified extra programme dub: "
			if !facts.HasOriginalAudio() && !slices.Contains(facts.ProgrammeLanguages, "English") {
				key, label = "non_original_non_english_dub", "Non-Original Non-English Dub — programme audio contains only non-original, non-English dubs: "
			}
			defects = append(defects, audioDefect{key: key, reason: label + language})
		}
	}
	counts := map[string]map[string]int{}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		switch track.Role {
		case api.AudioRoleCommentary, api.AudioRoleCompatibility, api.AudioRoleIsolatedScore, api.AudioRoleInterview, api.AudioRoleNovelty:
			continue
		case api.AudioRoleProgramme:
			if counts[track.ResourceID] == nil {
				counts[track.ResourceID] = map[string]int{}
			}
			for _, language := range track.Languages {
				counts[track.ResourceID][language]++
				if counts[track.ResourceID][language] > 1 {
					defects = append(
						defects,
						audioDefect{
							key:    "redundant_programme",
							reason: "Redundant Audio Tracks — duplicate programme mix: " + track.ID + " (" + language + ")",
						},
					)
				}
			}
		case api.AudioRoleAlternateMix:
			if subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ID)] == "duplicate" {
				defects = append(defects, audioDefect{key: "redundant_mix", reason: "Redundant Audio Tracks — non-unique alternate mix: " + track.ID})
			}
		case api.AudioRoleDescription, api.AudioRoleVoiceOver, api.AudioRoleHistorical:
			defects = append(
				defects,
				audioDefect{
					key: "track_justification",
					reason: "Redundant Audio Tracks — unjustified secondary track " + track.ID + " (" + string(
						track.Role,
					) + "); conditional eligibility must establish an avoidable drawback, not a novelty label or new permission",
				},
			)
		}
	}
	return defects
}

func sourceLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleAlternateMix {
			continue
		}
		answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ID)]
		if answer != "unique" && answer != "duplicate" {
			failures = append(failures, trackers.LanguageRuleFailure(
				subject,
				"alternate_mix",
				"source review must establish that alternate mix "+track.ID+" is an additional original mix or unique remix; its title alone does not establish uniqueness",
				trackers.LanguageUnresolved,
			))
		}
	}
	if needsSubtitlePresentation(subject) {
		outcome, key := trackers.LanguageUnresolved, "subtitle_presentation"
		reason := "English subtitle presentation needs source review; aggregate language evidence does not establish embedded coverage"
		if hardcodedEnglishSubtitles(subject) {
			reason = "known burned-in English subtitles must retain their hardcoded presentation; their coverage of each programme resource is unresolved"
		}
		if subject.QuestionnaireAnswers[subtitlePresentationQuestionKey(subject)] == "external" &&
			completeSubtitlePresentationEvidence(subject.LanguageFacts) && !hardcodedEnglishSubtitles(subject) {
			outcome, key = trackers.LanguageTrumpable, "external_subtitles"
			reason = "external English subtitles only partially satisfy the requirement; embedded English subtitles can provide a trumping improvement"
		}
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	return append(failures, compatibilityFailures(subject)...)
}

// Embedded coverage must accompany every represented programme resource. Known
// burned-in English is separate presentation evidence, scoped to the single
// resource currently inspected by production; it cannot prove multi-file coverage.
func needsSubtitlePresentation(subject api.TrackerValidationSubject) bool {
	facts := subject.LanguageFacts
	if !facts.OriginalLanguagesKnown || slices.Contains(facts.OriginalLanguages, "English") || slices.Contains(facts.OriginalLanguages, "ZXX") ||
		!slices.Contains(facts.SubtitleLanguages, "English") {
		return false
	}
	resources := map[string]bool{}
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) {
			resources[track.ResourceID] = resources[track.ResourceID] || slices.Contains(track.Languages, "English")
		}
	}
	needsEnglishSubtitles := len(resources) == 0
	for _, englishAudio := range resources {
		if !englishAudio {
			needsEnglishSubtitles = true
		}
	}
	// A proved English programme alternative makes subtitle presentation
	// irrelevant for that resource, even when unrelated subtitles are unresolved.
	if facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && !needsEnglishSubtitles {
		return false
	}
	if !completeSubtitlePresentationEvidence(facts) {
		return true
	}
	if len(resources) <= 1 && slices.Contains(facts.ProgrammeLanguages, "English") {
		return false
	}
	if len(resources) == 0 {
		return true
	}
	if len(resources) == 1 && hardcodedEnglishSubtitles(subject) {
		return false
	}
	for resource, englishAudio := range resources {
		if englishAudio {
			continue
		}
		if !slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
			return track.Kind == api.MediaTrackSubtitle && track.ResourceID == resource && slices.ContainsFunc(track.Languages, func(value string) bool {
				language, _ := languageutil.SubtitleLanguageParts(value)
				return language == "English"
			})
		}) {
			return true
		}
	}
	return false
}

func completeSubtitlePresentationEvidence(facts api.LanguageFacts) bool {
	return facts.SubtitleStatus == api.MetadataEvidenceStatusComplete && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete &&
		facts.TrackCoverageComplete
}

func hardcodedEnglishSubtitles(subject api.TrackerValidationSubject) bool {
	return slices.ContainsFunc(subject.HardcodedSubtitleLanguages, func(value string) bool {
		language, _ := languageutil.SubtitleLanguageParts(value)
		return language == "English"
	}) || slices.ContainsFunc(subject.HardcodedSubtitleCoverage, func(coverage api.SubtitleLanguageCoverage) bool {
		return languageutil.NormalizeLanguageDisplay(coverage.Language) == "English"
	})
}

func compatibilityMixes(facts api.LanguageFacts, compatibility api.MediaTrackFacts) []api.MediaTrackFacts {
	var candidates []api.MediaTrackFacts
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role != api.AudioRoleCompatibility && track.ID != "" &&
			strings.Contains(strings.ToLower(track.Codec), "truehd") && track.ResourceID == compatibility.ResourceID &&
			slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(compatibility.Languages, language) }) {
			candidates = append(candidates, track)
		}
	}
	return candidates
}

// WEB substitute provenance and relative quality are source facts. Confirming
// an inferior substitute creates a local trumpable finding, never a remote tag.
func compatibilityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	confirmed, pending := map[string]bool{}, map[string]bool{}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Codec == "" {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "compatibility_evidence", "audio codec requires review for track "+track.ID, trackers.LanguageUnresolved),
			)
		}
		if track.Role != api.AudioRoleCompatibility {
			continue
		}
		identified := track.ID != "" && len(track.Languages) > 0 && !slices.ContainsFunc(track.Languages, func(language string) bool {
			code := languageutil.NormalizeLanguageCode(language)
			return code == "" || code == "und" || code == "mul"
		})
		if !identified {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_evidence",
					"compatibility track identity and language require review",
					trackers.LanguageUnresolved,
				),
			)
		}
		validCodec := slices.ContainsFunc(
			[]string{"DD", "AC-3", "DD+", "E-AC-3", "DD+ Atmos"},
			func(codec string) bool { return strings.EqualFold(codec, track.Codec) },
		)
		if !validCodec {
			outcome := trackers.LanguageProhibited
			if track.Codec == "" {
				outcome = trackers.LanguageUnresolved
			}
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "compatibility_format", "unsupported compatibility audio format on track "+track.ID, outcome),
			)
		}
		source := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_source_"+track.ID)]
		validSource := source == "dd_core" && (strings.EqualFold(track.Codec, "DD") || strings.EqualFold(track.Codec, "AC-3")) ||
			source == "web_not_inferior" ||
			source == "web_inferior"
		if !validSource {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_source",
					"track "+track.ID+" must be the matching DD core or a source-reviewed WEB DD/DD+/DD+ Atmos substitute with assessed relative quality",
					trackers.LanguageUnresolved,
				),
			)
		} else if source == "web_inferior" {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_quality",
					"WEB compatibility substitute "+track.ID+" is demonstrably inferior to the disc-sourced AC-3",
					trackers.LanguageTrumpable,
				),
			)
		}
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]
		if answer != "" && slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == answer }) {
			if validCodec && validSource && identified {
				confirmed[answer] = true
			} else {
				pending[answer] = true
			}
		} else {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_mix",
					"source association with a TrueHD mix is unresolved for compatibility track "+track.ID,
					trackers.LanguageUnresolved,
				),
			)
			for _, candidate := range candidates {
				pending[candidate.ID] = true
			}
		}
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility || !strings.Contains(strings.ToLower(track.Codec), "truehd") ||
			track.EmbeddedCompatibility || confirmed[track.ID] {
			continue
		}
		outcome := trackers.LanguageProhibited
		if track.ID == "" || pending[track.ID] || subject.LanguageFacts.AudioStatus != api.MetadataEvidenceStatusComplete {
			outcome = trackers.LanguageUnresolved
		}
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"compatibility_missing",
				"TrueHD mix "+track.ID+" requires its DD core or a permitted WEB compatibility substitute",
				outcome,
			),
		)
	}
	return failures
}
