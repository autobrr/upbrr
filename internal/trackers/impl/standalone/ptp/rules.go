// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// languageFailures assesses independent defects from finalized evidence.
// Legacy payload choices never authorize a language-rule waiver.
func languageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	if ptpNoProgrammeDialogue(facts) {
		return nil
	}
	var failures []api.RuleFailure
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	needsOriginal := slices.ContainsFunc(facts.ProgrammeLanguages, func(language string) bool { return language != "English" && language != "ZXX" })
	if (needsOriginal && !facts.OriginalLanguagesKnown) || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		add("evidence", "original/programme language and track-role evidence is unresolved", trackers.LanguageUnresolved)
	} else if len(facts.ProgrammeLanguages) > 0 && !facts.HasOriginalAudio() && !slices.Contains(facts.ProgrammeLanguages, "English") {
		add("non_english_dub", "Non-English Language Dub; a replacement must resolve this defect", trackers.LanguageTrumpable)
	}
	if ptpNeedsTrackPurposeReview(facts) {
		detail := ". Programme tracks: " + ptpProgrammeTrackDetails(facts)
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "programme_track_purpose")] {
		case "distinct_content":
		case "redundant":
			add(
				"redundant_audio",
				"Redundant Audio Track(s); a replacement must resolve the reviewed duplicate mix or superfluous dub without losing required content"+detail,
				trackers.LanguageTrumpable,
			)
		default:
			add(
				"redundant_audio",
				"programme track purpose must establish whether additional mixes or dubs are redundant or preserve distinct necessary content"+detail,
				trackers.LanguageUnresolved,
			)
		}
	}
	primary := ptpPrimaryProgrammeLanguage(facts)
	if primary == "" && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete {
		add("primary_evidence", "primary programme language is unresolved for English-subtitle applicability", trackers.LanguageUnresolved)
	}
	if primary != "" && primary != "English" && primary != "ZXX" && !slices.Contains(facts.SubtitleLanguages, "English") {
		outcome := trackers.LanguageUnresolved
		reason := "English subtitle availability must be established locally or in PTP's subtitle manager"
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "english_subtitle_manager")] {
		case "available":
			outcome = ""
		case "missing":
			if facts.SubtitleStatus == api.MetadataEvidenceStatusComplete {
				outcome = trackers.LanguageTrumpable
				reason = "Missing English Subtitles; no local subtitles or subtitle-manager alternative is available"
			}
		}
		if outcome != "" {
			add("english_subtitles", reason, outcome)
		}
	}
	if facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && !ptpHasForcedEnglish(subject) {
		outcome := trackers.LanguageUnresolved
		reason := "required forced English dialogue coverage and subtitle-manager availability need review"
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "forced_english_dialogue")] {
		case "not_required", "available_in_manager":
			outcome = ""
		case "missing":
			if facts.SubtitleStatus == api.MetadataEvidenceStatusComplete {
				outcome = trackers.LanguageTrumpable
				reason = "Missing Forced English Subtitles; required dialogue coverage is absent locally and from the subtitle manager"
			}
		}
		if outcome != "" {
			add("forced_english_subtitles", reason, outcome)
		}
	}
	return failures
}

func ptpProgrammeTrackDetails(facts api.LanguageFacts) string {
	var details []string
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) {
			details = append(details, strings.TrimSpace(track.ID+" "+track.Title)+" ["+strings.Join(track.Languages, ", ")+"]")
		}
	}
	return strings.Join(details, "; ")
}

func ptpNoProgrammeDialogue(facts api.LanguageFacts) bool {
	return (facts.AudioAbsent && len(facts.ProgrammeLanguages) == 0) ||
		(facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && slices.Equal(facts.ProgrammeLanguages, []string{"ZXX"}))
}

// ptpPrimaryProgrammeLanguage uses the inspected primary stream, never original
// metadata or an optional English/secondary track elsewhere in the release.
func ptpPrimaryProgrammeLanguage(facts api.LanguageFacts) string {
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || facts.PrimaryAudioTrackID == "" {
		return ""
	}
	for _, track := range facts.Tracks {
		if track.ID != facts.PrimaryAudioTrackID || track.Kind != api.MediaTrackAudio ||
			(track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) || len(track.Languages) != 1 {
			continue
		}
		code := languageutil.NormalizeLanguageCode(track.Languages[0])
		if code == "" || code == "und" || code == "mul" {
			return ""
		}
		return languageutil.NormalizeLanguageLabel(track.Languages[0])
	}
	return ""
}

// ptpNeedsTrackPurposeReview identifies candidates for review. Track counts and
// language differences alone never prove that a mix or dub is redundant.
func ptpNeedsTrackPurposeReview(facts api.LanguageFacts) bool {
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		return false
	}
	seen := map[string]bool{}
	programmeTracks := 0
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		programmeTracks++
		for _, language := range track.Languages {
			if seen[language] {
				return true
			}
			seen[language] = true
		}
	}
	return programmeTracks > 1 && facts.OriginalLanguagesKnown && slices.ContainsFunc(facts.ProgrammeLanguages, func(language string) bool {
		return language != "English" && language != "ZXX" && !slices.Contains(facts.OriginalLanguages, language)
	})
}

// ptpHasForcedEnglish requires current English evidence before consulting
// retained tracks, preserving an explicit aggregate subtitle-language clear.
func ptpHasForcedEnglish(subject api.TrackerValidationSubject) bool {
	if !slices.Contains(subject.LanguageFacts.SubtitleLanguages, "English") {
		return false
	}
	for _, value := range subject.SubtitleLanguages {
		language, coverage := languageutil.SubtitleLanguageParts(value)
		if language == "English" && strings.EqualFold(coverage, "Forced") {
			return true
		}
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackSubtitle {
			continue
		}
		for _, value := range track.Languages {
			language, coverage := languageutil.SubtitleLanguageParts(value)
			if language == "English" && (track.Forced || strings.EqualFold(coverage, "Forced")) {
				return true
			}
		}
	}
	return slices.ContainsFunc(subject.HardcodedSubtitleCoverage, func(coverage api.SubtitleLanguageCoverage) bool {
		return coverage.Language == "English" && coverage.Coverage == api.SubtitleCoverageForced
	})
}
