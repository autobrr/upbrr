// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func btnSourceSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:       "BTN",
		Type:          "REMUX",
		Source:        "BluRay",
		LanguageFacts: btnLanguageFacts("English", "English"),
	}
}

func btnSourceAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = make(map[string]string)
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func requireBTNSourceFailure(t *testing.T, subject api.TrackerValidationSubject, rule string, disposition api.RuleDisposition, status api.MetadataEvidenceStatus) {
	t.Helper()
	failures := languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == rule && f.Disposition == disposition && f.EvidenceStatus == status
	}) {
		t.Fatalf("missing %s / %s / %s: %#v", rule, disposition, status, failures)
	}
}

func requireBTNSourceWarnings(t *testing.T, subject api.TrackerValidationSubject, rules ...string) {
	t.Helper()
	failures := languageAssessment(subject)
	if len(failures) != len(rules) {
		t.Fatalf("warnings = %#v, want %v", failures, rules)
	}
	for _, rule := range rules {
		requireBTNSourceFailure(t, subject, rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	}
	for _, failure := range failures {
		if trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false) {
			t.Fatalf("source warning blocked execution: %#v", failure)
		}
	}
}

func TestBTNSourceRetentionWarningIgnoresSavedAnswers(t *testing.T) {
	for _, answers := range [][3]string{
		{"", "", ""},
		{"incomplete", "incomplete", "missing"},
		{"best_original_retained", "retained_or_unavailable", "same_or_both_retained"},
		{"unresolved", "unresolved", "unresolved"},
	} {
		subject := btnSourceSubject()
		btnSourceAnswer(&subject, "source_audio", answers[0])
		btnSourceAnswer(&subject, "source_extras", answers[1])
		btnSourceAnswer(&subject, "broadcast_soundtrack", answers[2])
		requireBTNSourceWarnings(t, subject, "language_source_audio", "language_source_extras", "language_broadcast_soundtrack")
		subject.Identity.Generation++
		requireBTNSourceWarnings(t, subject, "language_source_audio", "language_source_extras", "language_broadcast_soundtrack")
		subject.Type = "DISC"
		requireBTNSourceWarnings(t, subject)
	}
}

func TestBTNSourceWarningsHaveBoundedApplicability(t *testing.T) {
	for _, test := range []struct {
		typ, source string
		rules       []string
	}{
		{"WEBDL", "WEB", nil},
		{"HDTV", "HDTV", nil},
		{"ENCODE", "UHDTV", nil},
		{"ENCODE", "BluRay", []string{"language_source_extras", "language_broadcast_soundtrack"}},
		{"ENCODE", "DVD", []string{"language_source_extras"}},
		{"REMUX", "BluRay", []string{"language_source_audio", "language_source_extras", "language_broadcast_soundtrack"}},
		{"DISC", "BluRay", nil},
	} {
		t.Run(test.typ+"/"+test.source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = test.typ, test.source
			requireBTNSourceWarnings(t, subject, test.rules...)
			if question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
				Type:          test.typ,
				Source:        test.source,
				LanguageFacts: subject.LanguageFacts,
			}}); question != nil {
				t.Fatalf("source comparison still requests answers: %#v", question)
			}
		})
	}
}

func TestBTNAnimationDubAndRetailSubtitleGuidance(t *testing.T) {
	for _, answers := range [][2]string{
		{"", ""},
		{"available_missing", "available_missing"},
		{"unavailable", "unavailable"},
		{"unavailable", "retail_included"},
	} {
		subject := btnSourceSubject()
		subject.LanguageFacts = btnLanguageFacts("Japanese", "Japanese")
		subject.EffectiveMetadata.Genres = []string{"Animation"}
		btnSourceAnswer(&subject, "primary_audio_country", "Japan")
		btnSourceAnswer(&subject, "retail_english_dub", answers[0])
		btnSourceAnswer(&subject, "retail_english_subtitles", answers[1])
		requireBTNSourceWarnings(t, subject, "language_source_audio", "language_source_extras", "language_broadcast_soundtrack", "language_retail_english_dub", "language_retail_english_subtitles")
		meta := api.UploadSubject{
			Type:                        subject.Type,
			Source:                      subject.Source,
			LanguageFacts:               subject.LanguageFacts,
			EffectiveMetadata:           subject.EffectiveMetadata,
			TrackerQuestionnaireAnswers: map[string]map[string]string{"BTN": subject.QuestionnaireAnswers},
		}
		question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
		if question == nil || len(question.Fields) != 1 || !strings.HasPrefix(question.Fields[0].Key, "primary_audio_country_") || !question.Fields[0].Required || question.Fields[0].Value != "Japan" {
			t.Fatalf("primary-country question changed or source questions remain: %#v", question)
		}
		// English subtitle presence does not establish its retail provenance.
		subject.LanguageFacts.SubtitleLanguages = []string{"English"}
		btnSourceAnswer(&subject, "primary_audio_country", "Japan")
		btnSourceAnswer(&subject, "retail_english_subtitles", "retail_included")
		requireBTNSourceFailure(t, subject, "language_retail_english_subtitles", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		subject.Type, subject.Source = "WEBDL", "WEB"
		subject.EffectiveMetadata.Genres = []string{"Drama"}
		btnSourceAnswer(&subject, "primary_audio_country", "Japan")
		requireBTNSourceWarnings(t, subject)
	}
}

func TestBTNSourceReviewDoesNotWaiveMeasuredRules(t *testing.T) {
	subject := btnSourceSubject()
	subject.LanguageFacts = btnLanguageFacts("English", "German")
	btnSourceAnswer(&subject, "primary_audio_country", "Germany")
	btnSourceAnswer(&subject, "source_audio", "best_original_retained")
	btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
	btnSourceAnswer(&subject, "broadcast_soundtrack", "missing")
	for _, key := range []string{"language_staff_dub", "language_original", "language_original_primary"} {
		requireBTNSourceFailure(t, subject, key, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	requireBTNSourceFailure(t, subject, "language_broadcast_soundtrack", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	subject.Source = "WEB"
	btnSourceAnswer(&subject, "primary_audio_country", "Germany")
	for _, key := range []string{"language_staff_dub", "language_original", "language_original_primary"} {
		requireBTNSourceFailure(t, subject, key, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	subject.LanguageFacts = btnLanguageFacts("Japanese", "Japanese")
	subject.Anime = true
	btnSourceAnswer(&subject, "primary_audio_country", "Japan")
	btnSourceAnswer(&subject, "retail_english_subtitles", "retail_included")
	requireBTNSourceFailure(t, subject, "language_anime_audio", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestBTNSourceAliasesKeepApplicableWarnings(t *testing.T) {
	for _, source := range []string{"BLU RAY", "BLU-RAY 3D", "BD", "BDMV", "PAL DVD", "NTSC DVD", "HD DVD"} {
		t.Run(source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = "ENCODE", source
			rules := []string{"language_source_extras"}
			if slices.Contains([]string{"BLU RAY", "BLU-RAY 3D", "BD", "BDMV"}, source) {
				rules = append(rules, "language_broadcast_soundtrack")
			}
			requireBTNSourceWarnings(t, subject, rules...)
		})
	}
}

func TestBTNUnknownSourceApplicabilityStaysAdvisory(t *testing.T) {
	for _, source := range []string{"", "Mixed", "Unknown", "unrecognized source", "WEB-archive"} {
		t.Run(source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = "ENCODE", source
			btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
			btnSourceAnswer(&subject, "broadcast_soundtrack", "same_or_both_retained")
			requireBTNSourceWarnings(t, subject, "language_source_kind")
			subject.Source = "WEB"
			requireBTNSourceWarnings(t, subject)
			subject.Source, subject.Type = source, "DISC"
			requireBTNSourceWarnings(t, subject)
		})
	}
}

func TestBTNRemuxSourceKindBoundsSoundtrackWarning(t *testing.T) {
	for _, discType := range []string{"", "Unknown", "BDMV", "Blu-Ray", "BLU RAY", "DVD", "HDDVD"} {
		t.Run(discType, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Source, subject.DiscType = "", discType
			rules := []string{"language_source_audio", "language_source_extras"}
			switch discType {
			case "", "Unknown":
				rules = append(rules, "language_source_kind")
			case "BDMV", "Blu-Ray", "BLU RAY":
				rules = append(rules, "language_broadcast_soundtrack")
			}
			requireBTNSourceWarnings(t, subject, rules...)
		})
	}
}

func TestBTNSupportedNonDiscSourcesNeedNoDiscReview(t *testing.T) {
	for _, source := range []string{"PDTV", "DSR", "TVRip", "VHSRip", "WEB-DL", "WEBDL", "WEBRip"} {
		t.Run(source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = "ENCODE", source
			requireBTNSourceWarnings(t, subject)
			if question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
				Type:          subject.Type,
				Source:        source,
				LanguageFacts: subject.LanguageFacts,
			}}); question != nil {
				t.Fatalf("non-disc source acquired a country question: %#v", question)
			}
		})
	}
}
