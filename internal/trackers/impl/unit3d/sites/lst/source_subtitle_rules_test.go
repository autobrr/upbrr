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

func TestLSTRetailSubtitleCoverageIsGuidance(t *testing.T) {
	for _, personal := range []bool{false, true} {
		for _, answer := range []string{"", "required_track_missing", "retail_track_retained"} {
			t.Run(fmt.Sprintf("personal=%t/answer=%s", personal, answer), func(t *testing.T) {
				subject := ordinaryCompatibilitySubject()
				subject.PersonalRelease = personal
				subject.QuestionnaireAnswers = map[string]string{
					sourceQuestionKey(subject, "retail_subtitle_english_media"):        answer,
					sourceQuestionKey(subject, "retail_subtitle_forced_english_media"): answer,
				}
				questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
					Type:            subject.Type,
					PersonalRelease: personal,
					LanguageFacts:   subject.LanguageFacts,
				}})
				if questionnaire != nil {
					for _, field := range questionnaire.Fields {
						if strings.Contains(field.Key, "retail_subtitle_") {
							t.Fatalf("retail coverage still asks for confirmation: %+v", field)
						}
					}
				}
				failures := remuxSubtitleFailures(subject)
				if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionAdvisory || failures[0].QuestionnaireInput ||
					failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
					t.Fatalf("retail coverage is not passive guidance: %+v", failures)
				}
				subject.Type = "ENCODE"
				if failures := remuxSubtitleFailures(subject); len(failures) != 0 {
					t.Fatalf("remux guidance leaked to encodes: %+v", failures)
				}
			})
		}
	}
}

func TestLSTAlternateMixPriority(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[0].Role = api.AudioRoleAlternateMix
	subject.QuestionnaireAnswers = map[string]string{}
	fields := alternateMixQuestions(subject)
	if len(fields) != 1 {
		t.Fatalf("fields: %#v", fields)
	}
	for _, personal := range []bool{false, true} {
		subject.PersonalRelease = personal
		fields = alternateMixQuestions(subject)
		if fields[0].Required != personal {
			t.Fatal("wrong priority")
		}
		failures := alternateMixFailures(subject)
		if len(failures) != 1 || (failures[0].Disposition == api.RuleDispositionStrict) != personal {
			t.Fatalf("priority: %#v", failures)
		}
		subject.QuestionnaireAnswers[fields[0].Key] = "additional_original_or_unique_remix"
		if len(alternateMixFailures(subject)) != 0 {
			t.Fatal("current review failed")
		}
		subject.QuestionnaireAnswers = map[string]string{}
	}
}
