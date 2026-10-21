// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestDiscReviewUnknownAndGeneration(t *testing.T) {
	subject := lstValidationSubject()
	subject.DiscType = "BDMV"
	subject.Type = "DISC"
	if failures := discReviewFailures(subject); len(failures) != 4 {
		t.Fatalf("missing review: %+v", failures)
	} else {
		for _, f := range failures {
			if f.Disposition != api.RuleDispositionStrict || f.EvidenceStatus != api.MetadataEvidenceStatusPartial {
				t.Fatalf("unknown treated as violation: %+v", f)
			}
		}
	}
	for _, field := range discReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = field.Options[1]
	}
	if failures := discReviewFailures(subject); len(failures) != 0 {
		t.Fatalf("reviewed: %+v", failures)
	}
	subject.Identity.Generation++
	if len(discReviewFailures(subject)) == 0 {
		t.Fatal("stale disc review reused")
	}
}
func TestCustomDiscAvailabilityCannotBeAcknowledged(t *testing.T) {
	subject := lstValidationSubject()
	subject.DiscType = "BDMV"
	subject.Type = "DISC"
	for _, field := range discReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = field.Options[1]
	}
	subject.QuestionnaireAnswers[discQuestionKey(subject, "form")] = "custom"
	if len(discReviewFailures(subject)) != 1 {
		t.Fatal("custom availability must be reviewed")
	}
	subject.QuestionnaireAnswers[discQuestionKey(subject, "custom_availability")] = "unmodified_same_disc_exists"
	failures := discReviewFailures(subject)
	if len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete || failures[0].Disposition != api.RuleDispositionStrict {
		t.Fatalf("known unavailable: %+v", failures)
	}
	subject.QuestionnaireAnswers[discQuestionKey(subject, "custom_availability")] = "no_unmodified_same_disc"
	if len(discReviewFailures(subject)) != 0 {
		t.Fatal("reviewed custom disc rejected")
	}
}
