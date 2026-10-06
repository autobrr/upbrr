// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXOriginalAudioRecommendation(t *testing.T) {
	subject := api.TrackerValidationSubject{Tracker: "ULCX", LanguageFacts: ulcxTestLanguageFacts("Japanese", []string{"English"}, nil)}
	failures := languageAssessment(subject)
	requireULCXSourceFailure(t, failures, "language_original_recommendation", api.RuleDispositionAdvisory)
	for _, failure := range failures {
		if failure.Disposition == api.RuleDispositionStrict {
			t.Fatalf("ordinary English dub prohibited: %+v", failures)
		}
	}
	subject.PersonalRelease = true
	requireULCXSourceFailure(t, languageAssessment(subject), "language_original", api.RuleDispositionStrict)
	subject.Type = "DISC"
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("full disc: %+v", got)
	}
}

func TestULCXAlternateMixSourceReview(t *testing.T) {
	subject := api.TrackerValidationSubject{
		Tracker:       "ULCX",
		Type:          "REMUX",
		LanguageFacts: ulcxTestLanguageFacts("English", []string{"English", "English"}, nil),
	}
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix
	requireULCXSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionAdvisory)
	question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{LanguageFacts: subject.LanguageFacts, Type: subject.Type}})
	if question == nil {
		t.Fatal("alternate mix source review is unreachable")
	}
	subject.QuestionnaireAnswers = map[string]string{}
	for _, field := range question.Fields {
		if strings.HasPrefix(field.Key, "alternate_mix_") {
			subject.QuestionnaireAnswers[field.Key] = "unique"
		}
	}
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_alternate_mix" {
			t.Fatalf("source-confirmed unique mix: %+v", failure)
		}
	}
}

func requireULCXSourceFailure(t *testing.T, failures []api.RuleFailure, rule string, disposition api.RuleDisposition) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule && failure.Disposition == disposition {
			return
		}
	}
	t.Fatalf("missing %s/%s: %+v", rule, disposition, failures)
}

func TestULCXAlternateMixReviewNeverPermitsExtraDub(t *testing.T) {
	subject := api.TrackerValidationSubject{Tracker: "ULCX", LanguageFacts: ulcxTestLanguageFacts("Japanese", []string{"Japanese", "English", "German"}, nil)}
	subject.LanguageFacts.Tracks[2].Role = api.AudioRoleAlternateMix
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "alternate_mix_audio-2"): "unique"}
	requireULCXSourceFailure(t, languageAssessment(subject), "language_extra_dub", api.RuleDispositionStrict)
	subject.PersonalRelease = true
	requireULCXSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionAdvisory)
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "alternate_mix_audio-2")] = "duplicate"
	requireULCXSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionAdvisory)
}
