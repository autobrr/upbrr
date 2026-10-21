// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceProcessingGuidanceAllFormats(t *testing.T) {
	for _, kind := range []string{"ENCODE", "REMUX", "WEBDL", "DISC"} {
		for _, answer := range []string{"", "reviewed_no_user_processing_or_improper_splicing", "retail_processing_preserved", "user_generated_or_enhanced_streams", "improper_splicing"} {
			subject := lstValidationSubject()
			subject.Type = kind
			subject.QuestionnaireAnswers[sourceQuestionKey(subject, "stream_processing")] = answer
			questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{Type: kind}})
			if questionnaire != nil {
				for _, field := range questionnaire.Fields {
					if strings.Contains(field.Key, "stream_processing") {
						t.Fatalf("%s: stream processing still requires a tracker answer: %+v", kind, field)
					}
				}
			}
			failures := withoutLSTStreamProcessingGuidance(t, sourceValidationFailures(subject))
			for _, failure := range failures {
				if strings.Contains(failure.Reason, "source stream processing") {
					t.Fatalf("%s %q retained a processing review gate: %+v", kind, answer, failure)
				}
			}
		}
	}
}

func withoutLSTStreamProcessingGuidance(t *testing.T, failures []api.RuleFailure) []api.RuleFailure {
	t.Helper()
	index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "lst_stream_processing" })
	if index < 0 {
		t.Fatalf("missing stream processing guidance: %+v", failures)
	}
	failure := failures[index]
	if failure.Disposition != api.RuleDispositionAdvisory || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial || failure.QuestionnaireInput {
		t.Fatalf("stream processing guidance blocks or requires review: %+v", failure)
	}
	return slices.Delete(failures, index, index+1)
}
