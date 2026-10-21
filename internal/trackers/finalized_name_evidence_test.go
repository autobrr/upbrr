// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparationRulesReceiveFinalNameWithoutCanonicalMutation(t *testing.T) {
	subject := api.TrackerValidationSubject{ReleaseName: "canonical"}
	input := PreparationInput{Projection: &api.TrackerReleaseProjection{UploadReleaseName: "reviewed-name"}}
	checked := false
	policy := ValidationPolicyBinding{Check: func(_ context.Context, got api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
		checked = true
		if got.FinalizedUploadName != "reviewed-name" || got.ReleaseName != "canonical" {
			t.Fatalf("name authority mixed: %+v", got)
		}
		return nil, nil
	}}
	if _, err := EvaluatePreparationRules(t.Context(), input, subject, policy); err != nil {
		t.Fatal(err)
	}
	if !checked || subject.FinalizedUploadName != "" {
		t.Fatal("caller subject mutated or validation skipped")
	}
}
