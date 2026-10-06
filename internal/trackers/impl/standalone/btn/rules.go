// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package btn

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.WithLanguageAssessment(validationPolicy(), languageAssessment)
}
func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	remux := strings.EqualFold(subject.Type, "REMUX")
	policy := trackers.LanguagePolicy{}
	if remux {
		policy.OriginalAudio = trackers.LanguageProhibited
		policy.OriginalPrimary = true
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		if !remux {
			add("evidence", "programme language, track role or inspected coverage needs review", trackers.LanguageUnresolved)
		}
	} else if language := btnPrimaryProgrammeLanguage(facts); language == "" {
		add("primary_evidence", "one identified primary programme language is required for the Foreign flag", trackers.LanguageUnresolved)
	} else if !isBTNEnglishLanguage(language) && btnPrimaryCountryID(subject) == "" {
		add("primary_country", "select the BTN country matching the primary "+language+" programme audio", trackers.LanguageUnresolved)
	}
	english := slices.Contains(facts.ProgrammeLanguages, "English")
	if !facts.OriginalLanguagesKnown {
		if !remux {
			add("original_evidence", "original language is unresolved for the programme dub assessment", trackers.LanguageUnresolved)
		}
	} else {
		for _, language := range facts.ProgrammeLanguages {
			if slices.Contains(facts.OriginalLanguages, language) {
				continue
			}
			if slices.Contains(facts.OriginalLanguages, "English") {
				add("staff_dub", "dubs of English-original shows require staff approval", trackers.LanguageStaffException)
			} else if language != "English" {
				add("optional_dub", "other non-English dubs should be omitted", trackers.LanguageAdvisory)
			}
		}
	}
	if subject.Anime && !english && (!slices.Contains(facts.ProgrammeLanguages, "Japanese") || !slices.Contains(facts.SubtitleLanguages, "English")) {
		outcome := trackers.LanguageProhibited
		if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || facts.SubtitleStatus != api.MetadataEvidenceStatusComplete {
			outcome = trackers.LanguageUnresolved
		}
		add("anime_audio", "anime requires Japanese audio with English subtitles or English programme audio", outcome)
	}
	if !subject.Anime && !remux && facts.OriginalLanguagesKnown &&
		facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && !slices.Contains(facts.OriginalLanguages, "English") && !facts.HasOriginalAudio() {
		add("original_pack", "a foreign non-anime release lacking original audio can be replaced by an original-audio pack", trackers.LanguageTrumpable)
	}
	return failures
}
