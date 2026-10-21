// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestRequiredQuestionnaireFailuresRemainActionable(t *testing.T) {
	registry := MustNewRegistry()
	for _, test := range []struct {
		tracker string
		rules   []string
		answers map[string]string
	}{
		{
			tracker: "ASC",
			rules:   []string{"required_overview", "required_genre"},
			answers: map[string]string{"overview": "Example overview", "genre": "Drama"},
		},
		{
			tracker: "BT",
			rules:   []string{"required_overview", "required_tags"},
			answers: map[string]string{"overview": "Example overview", "tags": "Drama"},
		},
		{
			tracker: "BJS",
			rules:   []string{"required_overview"},
			answers: map[string]string{"overview": "Example overview"},
		},
		{
			tracker: "PTS",
			rules:   []string{"required_questionnaire_answer"},
			answers: map[string]string{"mandarin_override": "yes"},
		},
	} {
		t.Run(test.tracker, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(test.tracker)
			if !ok {
				t.Fatal("missing tracker")
			}
			for _, answered := range []bool{false, true} {
				subject := api.TrackerValidationSubject{Tracker: test.tracker}
				if answered {
					subject.QuestionnaireAnswers = test.answers
				}
				failures, err := descriptor.Validation.Check(t.Context(), subject, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				for _, rule := range test.rules {
					index := slices.IndexFunc(failures, func(failure api.RuleFailure) bool { return failure.Rule == rule })
					if answered {
						if index >= 0 {
							t.Fatalf("answer did not resolve %s: %+v", rule, failures)
						}
					} else if index < 0 || !failures[index].QuestionnaireInput || failures[index].Disposition != api.RuleDispositionStrict {
						t.Fatalf("required answer lost its strict corrective gate: %+v", failures)
					}
				}
				for _, failure := range failures {
					if !slices.Contains(test.rules, failure.Rule) && failure.QuestionnaireInput {
						t.Fatalf("independent failure became questionnaire input: %+v", failure)
					}
				}
			}
		})
	}
}
