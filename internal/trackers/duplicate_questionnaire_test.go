// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateQuestionnairesUseEvaluatedCandidatesAndReviewedAnswers(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:        "EXAMPLE",
		DisplayName: "Example",
		Family:      FamilyStandalone,
		Definition:  projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
		DupePolicy: &DupePolicy{ID: "example/v1", Questionnaire: func(input PreparationInput) []api.TrackerQuestionnaireField {
			candidates := input.Meta.TrackerDuplicateReviewCandidates["EXAMPLE"]
			if len(candidates) != 1 || candidates[0].ID != "actual" || candidates[0].EvidenceFingerprint != "current" {
				t.Fatalf("unbound candidates: %+v", candidates)
			}
			if input.Meta.TrackerQuestionnaireAnswers["EXAMPLE"]["review"] != "reviewed" {
				t.Fatal("mutable caller answers replaced projection authority")
			}
			return []api.TrackerQuestionnaireField{{
				Key:     "comparison",
				Label:   "Compare candidate",
				Kind:    "select",
				Options: []string{"unknown", "reviewed"},
			}}
		}},
	}); err != nil {
		t.Fatal(err)
	}
	projector, err := NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	subject := api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{"EXAMPLE": {"review": "mutable"}}, TrackerDuplicateReviewCandidates: map[string][]api.DupeMatchProjection{"EXAMPLE": {{ID: "invented"}}}}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "EXAMPLE", QuestionnaireAnswers: map[string]string{"review": "reviewed"}}}}
	assessment := api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
		TrackerID: "EXAMPLE",
		Decision:  api.DupeDecisionPending,
		Matches:   []api.DupeMatchProjection{{ID: "actual", EvidenceFingerprint: "current"}},
	}}}
	got := projector.DuplicateQuestionnaires(subject, projections, assessment)
	if len(got.Results[0].Questionnaire) != 1 || got.Results[0].Decision != api.DupeDecisionPending {
		t.Fatalf("assessment = %+v", got)
	}
	if len(assessment.Results[0].Questionnaire) != 0 || subject.TrackerQuestionnaireAnswers["EXAMPLE"]["review"] != "mutable" {
		t.Fatal("mutated caller snapshots")
	}
}

func TestDuplicateQuestionnairesUseEffectivePersonalRelease(t *testing.T) {
	no := false
	for _, test := range []struct {
		name     string
		groups   config.CSVList
		override *bool
		want     bool
	}{
		{
			name:   "configured personal",
			groups: config.CSVList{"GRP"},
			want:   true,
		},
		{name: "default false"},
		{
			name:     "explicit false",
			groups:   config.CSVList{"GRP"},
			override: &no,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.RegisterDescriptor(Descriptor{
				Name:        "EXAMPLE",
				DisplayName: "Example",
				Family:      FamilyStandalone,
				Definition:  projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
				DupePolicy: &DupePolicy{ID: "example/v1", Questionnaire: func(input PreparationInput) []api.TrackerQuestionnaireField {
					return []api.TrackerQuestionnaireField{{
						Key:     LanguageQuestionKey(api.NewTrackerValidationSubject(input.Meta, input.Tracker), "candidate_reviewed"),
						Label:   "Review candidate",
						Kind:    "select",
						Options: []string{"unknown", "yes"},
					}}
				}},
			}); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"EXAMPLE": {PersonalReleaseGroups: test.groups},
			}}}
			projector, err := NewWorkflowProjector(registry, cfg, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			subject := api.UploadSubject{Tag: "-GRP", PersonalReleaseOverride: test.override}
			projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "EXAMPLE"}}}
			assessment := api.DupeAssessment{Results: []api.TrackerDupeAssessment{{TrackerID: "EXAMPLE"}}}
			got := projector.DuplicateQuestionnaires(subject, projections, assessment)
			effective := subject
			effective.PersonalRelease = test.want
			want := LanguageQuestionKey(api.NewTrackerValidationSubject(effective, "EXAMPLE"), "candidate_reviewed")
			if len(got.Results[0].Questionnaire) != 1 || got.Results[0].Questionnaire[0].Key != want {
				t.Fatalf("questionnaire does not match effective personal-release context: %+v", got.Results[0].Questionnaire)
			}
			if subject.PersonalRelease {
				t.Fatal("mutated caller subject")
			}
		})
	}
}
