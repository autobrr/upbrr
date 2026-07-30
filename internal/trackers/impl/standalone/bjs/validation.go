// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-bjs-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			fields := buildFields(meta, "", "", standalone.QuestionnaireAnswers(meta, "BJS"))
			failures := make([]api.RuleFailure, 0, 3)
			if strings.TrimSpace(fields["imdblink"]) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"missing IMDb or TMDb identifier",
					api.RuleDispositionStrict,
				))
			}
			if strings.TrimSpace(fields["sinopse"]) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_overview", "missing overview", api.RuleDispositionStrict))
			}
			if strings.TrimSpace(fields["diretor"]) == "" || strings.EqualFold(strings.TrimSpace(fields["diretor"]), "skipped") {
				failures = append(failures, trackers.NewRuleFailure(
					"required_credits",
					"missing director/creator credits",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}

func validateFields(fields map[string]string) string {
	if strings.TrimSpace(fields["imdblink"]) == "" {
		return "missing IMDb or TMDb identifier"
	}
	if strings.TrimSpace(fields["sinopse"]) == "" {
		return "missing overview"
	}
	if strings.TrimSpace(fields["diretor"]) == "" || strings.EqualFold(strings.TrimSpace(fields["diretor"]), "skipped") {
		return "missing director/creator credits"
	}
	return ""
}
