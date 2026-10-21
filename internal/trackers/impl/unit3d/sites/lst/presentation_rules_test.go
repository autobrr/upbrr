// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPresentationReviewPriorityAndGeneration(t *testing.T) {
	for _, personal := range []bool{false, true} {
		subject := lstValidationSubject()
		subject.PersonalRelease = personal
		subject.Type = "REMUX"
		subject.QuestionnaireAnswers = map[string]string{}
		for _, failure := range presentationReviewFailures(subject) {
			if (failure.Disposition == api.RuleDispositionStrict) != personal || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
				t.Fatalf("priority or missing evidence: %+v", failure)
			}
		}
		for _, field := range presentationReviewQuestions(subject) {
			if strings.Contains(field.Key, "documentation_review") || !personal && strings.Contains(field.Key, "track_presentation") {
				t.Fatalf("redundant review question: %+v", field)
			}
			if field.Required != personal {
				t.Fatal("wrong required state")
			}
			subject.QuestionnaireAnswers[field.Key] = field.Options[1]
		}
		failures := presentationReviewFailures(subject)
		if !personal {
			failures = withoutLSTTrackPresentationGuidance(t, failures)
		}
		if len(failures) != 0 {
			t.Fatalf("reviewed: %+v", failures)
		}
		if description := requiredAssetDescription(subject); description != "" {
			t.Fatalf("confirmation generated description content: %s", description)
		}
		subject.Identity.Generation++
		if personal && len(presentationReviewFailures(subject)) == 0 {
			t.Fatal("stale review reused")
		}
	}
}

func TestPersonalPresentationCannotOverrideMissingLanguages(t *testing.T) {
	for _, kind := range []api.MediaTrackKind{api.MediaTrackAudio, api.MediaTrackSubtitle} {
		t.Run(string(kind), func(t *testing.T) {
			subject := lstValidationSubject()
			subject.PersonalRelease = true
			for i := range subject.LanguageFacts.Tracks {
				if subject.LanguageFacts.Tracks[i].Kind == kind {
					subject.LanguageFacts.Tracks[i].Languages = nil
				}
			}
			for _, field := range presentationReviewQuestions(subject) {
				subject.QuestionnaireAnswers[field.Key] = "reviewed_correct"
			}
			for _, field := range assetReviewQuestions(subject) {
				if field.Kind == "text" {
					subject.QuestionnaireAnswers[field.Key] = "Reviewed English track"
				}
			}
			failures := presentationReviewFailures(subject)
			requireLSTValidationFailure(t, failures, "language_presentation_tags", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
		})
	}
}

func TestLSTAudioTitlesAreGuidance(t *testing.T) {
	for _, personal := range []bool{false, true} {
		for _, title := range []string{"", "   ", "Programme audio"} {
			t.Run(fmt.Sprintf("personal=%t/title=%q", personal, title), func(t *testing.T) {
				subject := lstValidationSubject()
				subject.PersonalRelease = personal
				subject.LanguageFacts.Tracks[0].Title = title
				for _, field := range assetReviewQuestions(subject) {
					if strings.Contains(field.Key, "track_notes") {
						t.Fatalf("audio title created a clarification requirement: %+v", field)
					}
					subject.QuestionnaireAnswers[field.Key] = "lossless_preserved"
				}
				for _, field := range presentationReviewQuestions(subject) {
					subject.QuestionnaireAnswers[field.Key] = "reviewed_correct"
				}
				for _, field := range sourceReviewQuestions(subject) {
					subject.QuestionnaireAnswers[field.Key] = field.Options[1]
				}
				failures, err := validationPolicy().Check(t.Context(), subject, nil)
				if err != nil {
					t.Fatal(err)
				}
				failures = withoutLSTStreamProcessingGuidance(t, failures)
				if !personal {
					failures = withoutLSTTrackPresentationGuidance(t, failures)
				}
				if strings.TrimSpace(title) != "" {
					if len(failures) != 0 {
						t.Fatalf("complete tags rejected: %+v", failures)
					}
					return
				}
				if len(failures) != 1 || failures[0].QuestionnaireInput || !strings.Contains(failures[0].Reason, "audio-0") {
					t.Fatalf("expected only track-specific guidance: %+v", failures)
				}
				requireLSTValidationFailure(t, failures, "language_audio_titles", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
				subject.Type = "DISC"
				if failures := presentationReviewFailures(subject); len(failures) != 0 {
					t.Fatalf("full-disc track titles reviewed: %+v", failures)
				}
			})
		}
	}
}

func TestLSTWEBSourceNamingUsesResolvedService(t *testing.T) {
	for _, test := range []struct {
		name, releaseType, service, longName string
		wantQuestion, wantGuidance           bool
	}{
		{
			name:         "service code",
			releaseType:  "WEBDL",
			service:      "ATVP",
			wantGuidance: true,
		},
		{
			name:         "service name",
			releaseType:  "WEB-DL",
			longName:     "Apple TV+",
			wantGuidance: true,
		},
		{
			name:         "missing service",
			releaseType:  "WEBDL",
			wantQuestion: true,
		},
		{
			name:         "blank service",
			releaseType:  "WEB-DL",
			service:      " ",
			longName:     " ",
			wantQuestion: true,
		},
		{
			name:        "non-WEB release",
			releaseType: "ENCODE",
			service:     "ATVP",
		},
	} {
		for _, personal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/personal=%t", test.name, personal), func(t *testing.T) {
				meta := api.UploadSubject{
					Type:            test.releaseType,
					Service:         test.service,
					ServiceLongName: test.longName,
					PersonalRelease: personal,
				}
				questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
				found := false
				var fields []api.TrackerQuestionnaireField
				if questionnaire != nil {
					fields = questionnaire.Fields
				}
				for _, field := range fields {
					if strings.Contains(field.Key, "web_source_naming") {
						found = true
						if field.Required != personal {
							t.Fatalf("missing-service question priority changed: %+v", field)
						}
					}
				}
				if found != test.wantQuestion {
					t.Fatalf("WEB source question present=%t, want %t", found, test.wantQuestion)
				}
				subject := api.NewTrackerValidationSubject(meta, "LST")
				found = false
				for _, failure := range presentationReviewFailures(subject) {
					if failure.Rule == "language_web_source_naming" {
						found = true
						if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput {
							t.Fatalf("known-service guidance blocks or requires review: %+v", failure)
						}
					}
				}
				if found != test.wantGuidance {
					t.Fatalf("WEB source guidance present=%t, want %t", found, test.wantGuidance)
				}
			})
		}
	}
}

func withoutLSTTrackPresentationGuidance(t *testing.T, failures []api.RuleFailure) []api.RuleFailure {
	t.Helper()
	for i, failure := range failures {
		if failure.Rule != "language_track_presentation" {
			continue
		}
		if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("track presentation guidance requires an answer: %+v", failure)
		}
		return append(failures[:i], failures[i+1:]...)
	}
	t.Fatalf("missing ordinary-release track presentation guidance: %+v", failures)
	return nil
}
