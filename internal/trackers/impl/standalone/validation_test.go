// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidatePreparationExecutionPolicy(t *testing.T) {
	t.Parallel()

	policy := func(disposition api.RuleDisposition) trackers.ValidationPolicyBinding {
		return trackers.ValidationPolicyBinding{
			ID: "test-policy-v1",
			Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
				return []api.RuleFailure{trackers.NewRuleFailure("test_rule", "test reason", disposition)}, nil
			},
		}
	}
	input := trackers.PreparationInput{
		Intent:  trackers.PreparationIntentUpload,
		Tracker: "EXAMPLE",
		Logger:  api.NopLogger{},
	}

	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionStrict)); err == nil ||
		!strings.Contains(err.Error(), "test_rule") {
		t.Fatalf("strict failure must block direct preparation: %v", err)
	}
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionWaivable)); err == nil {
		t.Fatal("waivable failure must block normal direct preparation")
	}
	input.ExecutionMode = api.WorkflowExecutionModeDebug
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionWaivable)); err != nil {
		t.Fatalf("debug must bypass waivable direct-preparation policy: %v", err)
	}
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionStrict)); err == nil {
		t.Fatal("debug must not bypass strict direct-preparation failure")
	}
}

func TestValidatePreparationRejectsInvalidDirectDryRun(t *testing.T) {
	t.Parallel()

	policy := trackers.ValidationPolicyBinding{
		ID: "test-policy-v1",
		Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"required_questionnaire_answer",
				"answer required",
				api.RuleDispositionStrict,
			)}, nil
		},
	}
	err := ValidatePreparation(context.Background(), trackers.PreparationInput{
		Intent:  trackers.PreparationIntentDryRun,
		Tracker: "EXAMPLE",
		Logger:  api.NopLogger{},
	}, policy)
	if err == nil || !strings.Contains(err.Error(), "required_questionnaire_answer") {
		t.Fatalf("direct dry run must reject invalid facts: %v", err)
	}
}
