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

func TestLSTDocumentationConfirmsDescriptionInclusion(t *testing.T) {
	for _, releaseType := range []string{"REMUX", "ENCODE"} {
		for _, personal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/personal=%t", releaseType, personal), func(t *testing.T) {
				meta := api.UploadSubject{Type: releaseType, PersonalRelease: personal}
				questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
				var references api.TrackerQuestionnaireField
				for _, field := range questionnaire.Fields {
					if strings.Contains(field.Key, "documentation_review") || strings.Contains(field.Key, "retail_subtitle_") ||
						!personal && strings.Contains(field.Key, "track_presentation") {
						t.Fatalf("unexpected review checkbox: %+v", field)
					}
					if strings.Contains(field.Key, "presentation_source_documentation_") {
						references = field
					}
				}
				if references.Key == "" || references.Kind != "select" || references.Required != personal {
					t.Fatalf("wrong reference input: %+v", references)
				}
				meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"LST": {references.Key: "will_add_required_data"}}
				subject := api.NewTrackerValidationSubject(meta, "LST")
				for _, field := range presentationReviewQuestions(subject) {
					if field.Key != references.Key {
						subject.QuestionnaireAnswers[field.Key] = "reviewed_correct"
					}
				}
				for _, failure := range presentationReviewFailures(subject) {
					if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput {
						t.Fatalf("description inclusion requires another confirmation: %+v", failure)
					}
				}
				if description := requiredAssetDescription(subject); description != "" {
					t.Fatalf("confirmation generated description content: %s", description)
				}
				subject.AssetFacts.ScreenshotFingerprint = "later-hosted-screenshots"
				for _, field := range presentationReviewQuestions(subject) {
					if strings.Contains(field.Key, "source_documentation") && (field.Key != references.Key || field.Value != "will_add_required_data") {
						t.Fatalf("screenshot stage lost source confirmation: %+v", field)
					}
				}
			})
		}
	}
}

func TestLSTMissingAudioTitlesDoNotGenerateDescriptions(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts.Tracks[0].Title = ""
	if description := requiredAssetDescription(subject); description != "" {
		t.Fatalf("missing title generated description text: %s", description)
	}
}
