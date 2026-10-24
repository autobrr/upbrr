// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestDiscReviewUnknownAndGeneration(t *testing.T) {
	subject := lstValidationSubject()
	subject.DiscType, subject.Type = "BDMV", "DISC"
	for range 2 {
		failures := discReviewFailures(subject)
		if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionAdvisory || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial || failures[0].QuestionnaireInput {
			t.Fatalf("unknown provenance became a gate: %+v", failures)
		}
		subject.Identity.Generation++
	}
}

func TestCustomDiscAvailabilityIsNotEstablishedBySavedAnswer(t *testing.T) {
	subject := lstValidationSubject()
	subject.DiscType, subject.Type = "BDMV", "DISC"
	for _, answer := range []string{"unmodified_same_disc_exists", "no_unmodified_same_disc"} {
		subject.QuestionnaireAnswers[discQuestionKey(subject, "form")] = "custom"
		subject.QuestionnaireAnswers[discQuestionKey(subject, "custom_availability")] = answer
		failures := discReviewFailures(subject)
		if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionAdvisory || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("saved answer created authority: %+v", failures)
		}
	}
}

// Reconstruct legacy saved keys solely to verify that retired answers are ignored.
func discQuestionKey(subject api.TrackerValidationSubject, key string) string {
	fingerprint, _ := api.CanonicalWorkflowFingerprint(struct {
		Disc         api.DiscFacts
		Files        []string
		Presentation string
	}{subject.Disc, subject.FileList, subject.Is3D})
	return trackers.LanguageQuestionKey(subject, "disc_"+key+"_"+string(fingerprint))
}
