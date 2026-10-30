// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// regression: #803
func TestIntendedTrumpRequiresCurrentExplicitAnswer(t *testing.T) {
	input := trackers.PreparationInput{
		TrackerConfig: config.TrackerConfig{GuruTrump: true},
		Meta:          api.UploadSubject{SourcePath: "/synthetic/movie", Identity: api.ExternalIdentity{Generation: 1}},
	}
	choice, reason := trumpQuestionKeys(input.Meta.SourcePath, input.Meta.Identity)
	if got, _ := intendedTrump(input); got {
		t.Fatal("configuration alone must not authorize a trump")
	}
	fields := trumpQuestionnaireFields(input)
	if len(fields) != 2 || fields[0].Value != "no" {
		t.Fatalf("unexpected defaults: %#v", fields)
	}
	if review := duplicateTrumpQuestionnaireFields(input); len(review) != 2 || !review[0].NeedsReview || !review[1].NeedsReview || fields[0].NeedsReview {
		t.Fatal("trump intent must be requested during duplicate review without blocking the initial projection")
	}
	input.Meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"ANT": {choice: "yes", reason: "  verified improvement  "}}
	if got, explanation := intendedTrump(input); !got || explanation != "verified improvement" {
		t.Fatalf("current explicit answer not consumed: %t %q", got, explanation)
	}
	input.Projection = &api.TrackerReleaseProjection{}
	if got, _ := intendedTrump(input); got {
		t.Fatal("unreviewed subject answer overrode the reviewed projection")
	}
	input.Projection.QuestionnaireAnswers = input.Meta.TrackerQuestionnaireAnswers["ANT"]
	if got, _ := intendedTrump(input); !got {
		t.Fatal("reviewed answer was ignored")
	}
	if len(duplicateTrumpQuestionnaireFields(input)) != 0 {
		t.Fatal("saved trump choice must not repeat the duplicate questionnaire")
	}
	input.Meta.Identity.Generation++
	if got, _ := intendedTrump(input); got {
		t.Fatal("stale generation answer was accepted")
	}
	if len(duplicateTrumpQuestionnaireFields(input)) != 2 {
		t.Fatal("a new source generation must require a fresh trump choice")
	}
	input.Meta.Identity.Generation--
	input.Meta.SourcePath = "/synthetic/other"
	if got, _ := intendedTrump(input); got {
		t.Fatal("another source's answer was accepted")
	}
	input.Meta.SourcePath = "/synthetic/movie"
	input.TrackerConfig.GuruTrump = false
	if got, _ := intendedTrump(input); got || len(trumpQuestionnaireFields(input)) != 0 {
		t.Fatal("disabled Guru mode retained trump authorization or controls")
	}
}

// regression: #803
func TestGuruIntentPreservesExactDuplicateStaffAttestation(t *testing.T) {
	identity := api.ExternalIdentity{Generation: 1}
	choice, _ := trumpQuestionKeys("/synthetic/movie", identity)
	subject := api.DuplicateSubject{
		SourcePath: "/synthetic/movie",
		Identity:   identity,
		Projection: &api.TrackerReleaseProjection{QuestionnaireAnswers: map[string]string{choice: "yes"}},
	}
	cfg := config.TrackerConfig{GuruTrump: true}
	for _, test := range []struct {
		relation api.DupeRelation
		staff    bool
	}{
		{api.DupeRelationSameSlot, false},
		{api.DupeRelationManualReview, false},
		{api.DupeRelationExactDuplicate, true},
	} {
		evaluations := []api.DupeCandidateEvaluation{{Relation: test.relation}}
		if got := requiresStaffToken(cfg, subject, evaluations); got != test.staff {
			t.Errorf("%s: staff = %t, want %t", test.relation, got, test.staff)
		}
		if evaluations[0].Relation != test.relation {
			t.Fatal("intention must not reclassify duplicate evidence")
		}
	}
	subject.Projection.QuestionnaireAnswers[choice] = "no"
	if !requiresStaffToken(cfg, subject, nil) {
		t.Fatal("ordinary upload lost staff attestation")
	}
	subject.Projection = nil
	if !requiresStaffToken(cfg, subject, nil) {
		t.Fatal("missing reviewed projection must fail closed")
	}
}
