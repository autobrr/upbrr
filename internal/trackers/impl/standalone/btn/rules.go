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
	return append(failures, sourceLanguageFailures(subject)...)
}

// sourceLanguageFailures keeps source comparisons separate from measured
// language facts. These findings do not establish remote replacement eligibility.
func sourceLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	// Unknown recommendation evidence does not turn should-guidance into a
	// mandatory source requirement.
	addUnresolvedGuidance := func(key, reason string) {
		failure := trackers.LanguageRuleFailure(subject, key, reason, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	if !sourceKindKnown(subject) {
		add(
			"source_kind",
			"correct Source in Input to establish source-disc extras and Blu-ray/broadcast soundtrack applicability",
			trackers.LanguageUnresolved,
		)
	}
	remux := strings.EqualFold(subject.Type, "REMUX")
	if remux {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "source_audio")] {
		case "best_original_retained":
		case "incomplete":
			add("source_audio", "remuxes must retain the best available original-language primary audio", trackers.LanguageProhibited)
		default:
			add("source_audio", "source comparison must establish the best available original-language primary audio is retained", trackers.LanguageUnresolved)
		}
	}
	if discSourceVideo(subject) {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "source_extras")] {
		case "retained_or_unavailable":
		case "incomplete":
			add("source_extras", "available source-disc commentary, isolated scores and chapters must be included", trackers.LanguageProhibited)
		default:
			add("source_extras", "source-disc commentary, isolated-score and chapter availability/inclusion needs review", trackers.LanguageUnresolved)
		}
	}
	if bluRaySourceVideo(subject) {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "broadcast_soundtrack")] {
		case "same_or_both_retained":
		case "missing":
			add(
				"broadcast_soundtrack",
				"a different Blu-ray and original broadcast soundtrack should both be retained; one is missing",
				trackers.LanguageTrumpable,
			)
		default:
			add(
				"broadcast_soundtrack",
				"compare the Blu-ray and original broadcast soundtracks and establish whether both are retained when different",
				trackers.LanguageUnresolved,
			)
		}
	}
	if needsRetailEnglishDubReview(subject) {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "retail_english_dub")] {
		case "unavailable":
		case "available_missing":
			add("retail_english_dub", "foreign animation should include an English dub when available from retail sources", trackers.LanguageAdvisory)
		default:
			addUnresolvedGuidance("retail_english_dub", "retail English-dub availability is unresolved; foreign animation should include it when available")
		}
	}
	if remux && foreignOriginal(subject.LanguageFacts) {
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "retail_english_subtitles")] {
		case "unavailable":
		case "retail_included":
			if slices.Contains(subject.LanguageFacts.SubtitleLanguages, "English") {
				break
			}
			add("retail_english_subtitles", "retail English subtitle inclusion is not established in the finalized subtitle facts", trackers.LanguageAdvisory)
		case "available_missing":
			add(
				"retail_english_subtitles",
				"retail English subtitles can improve a foreign remux; replacement eligibility still requires a source/slot comparison",
				trackers.LanguageAdvisory,
			)
		default:
			addUnresolvedGuidance(
				"retail_english_subtitles",
				"retail English subtitle availability and inclusion are unresolved; they can improve a foreign remux",
			)
		}
	}
	return failures
}

func bluRaySourceVideo(subject api.TrackerValidationSubject) bool {
	source := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(subject.Source), "-", ""))
	switch source {
	case "BLURAY", "BLU RAY", "BLURAY 3D", "BD", "BDMV", "BDRIP", "BRRIP":
		return true
	default:
		discType := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToUpper(strings.TrimSpace(subject.DiscType)))
		return discType == "BDMV" || discType == "BLURAY"
	}
}

func discSourceVideo(subject api.TrackerValidationSubject) bool {
	return discSourceKindKnown(subject) || strings.EqualFold(subject.Type, "REMUX") || strings.EqualFold(subject.Type, "DVDRIP")
}

func discSourceKindKnown(subject api.TrackerValidationSubject) bool {
	if bluRaySourceVideo(subject) || trackers.IsDiscType(subject.DiscType) {
		return true
	}
	switch strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(subject.Source), "-", "")) {
	case "DVD", "PAL DVD", "NTSC DVD", "HDDVD", "HD DVD", "DVD5", "DVD9":
		return true
	default:
		return false
	}
}

// sourceKindKnown distinguishes unknown provenance from known non-disc video.
// A remux without its disc kind still needs review for Blu-ray soundtrack rules.
func sourceKindKnown(subject api.TrackerValidationSubject) bool {
	if discSourceKindKnown(subject) {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(subject.Source)) {
	case "WEB", "WEB-DL", "WEBDL", "WEBRIP", "HDTV", "UHDTV", "PDTV", "DSR", "TVRIP", "VHSRIP":
		return true
	case "":
		return strings.EqualFold(subject.Type, "WEBDL") || strings.EqualFold(subject.Type, "WEBRIP") ||
			strings.EqualFold(subject.Type, "HDTV") || strings.EqualFold(subject.Type, "DVDRIP")
	default:
		return false
	}
}

func foreignOriginal(facts api.LanguageFacts) bool {
	return facts.OriginalLanguagesKnown && !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX")
}

func needsRetailEnglishDubReview(subject api.TrackerValidationSubject) bool {
	animated := subject.Anime || slices.ContainsFunc(subject.EffectiveMetadata.Genres, func(genre string) bool { return strings.EqualFold(genre, "Animation") })
	return animated && foreignOriginal(subject.LanguageFacts) && subject.LanguageFacts.ProgrammeStatus == api.MetadataEvidenceStatusComplete &&
		!slices.Contains(subject.LanguageFacts.ProgrammeLanguages, "English")
}
