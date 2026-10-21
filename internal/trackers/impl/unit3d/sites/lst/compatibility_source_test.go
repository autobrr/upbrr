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

func TestLSTSameLanguageCompatibilityNeedsNoOriginReview(t *testing.T) {
	for _, codec := range []string{"DD", "AC-3", "DD+", "DDP", "E-AC3", "E-AC-3", "DD+ Atmos"} {
		for _, personal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/personal=%t", codec, personal), func(t *testing.T) {
				subject := ordinaryCompatibilitySubject()
				subject.PersonalRelease = personal
				subject.LanguageFacts.Tracks[1].Codec = codec
				for _, failure := range compatibilityFailures(subject) {
					if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput {
						t.Fatalf("matching companion requires input: %+v", failure)
					}
				}
				questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
					Type:            subject.Type,
					Source:          subject.Source,
					PersonalRelease: personal,
					Identity:        subject.Identity,
					LanguageFacts:   subject.LanguageFacts,
				}})
				if questionnaire != nil {
					for _, field := range questionnaire.Fields {
						if strings.HasPrefix(field.Key, "compatibility_") {
							t.Fatalf("matching companion has a tracker question: %+v", field)
						}
					}
				}
			})
		}
	}
}

func TestLSTObsoleteCompatibilityOriginAnswersDoNotGateMatchingTracks(t *testing.T) {
	for _, answer := range []string{"unresolved", "other_source", "matching_disc_core", "matching_web_source"} {
		t.Run(answer, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			subject.QuestionnaireAnswers = map[string]string{
				trackers.LanguageQuestionKey(subject, "compatibility_origin_companion_main"): answer,
			}
			for _, failure := range compatibilityFailures(subject) {
				if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput {
					t.Fatalf("obsolete source answer created a gate: %+v", failure)
				}
			}
		})
	}
}
