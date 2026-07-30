// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

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
		ID: "standalone-asc-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			answers := standalone.QuestionnaireAnswers(meta, "ASC")
			failures := make([]api.RuleFailure, 0, 4)
			if !meta.Anime && strings.TrimSpace(resolveIMDbIDText(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"missing IMDb ID",
					api.RuleDispositionStrict,
				))
			}
			if strings.TrimSpace(resolvePoster(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_poster", "missing poster URL", api.RuleDispositionStrict))
			}
			if strings.TrimSpace(resolveGenres(meta, answers)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_genre", "missing genre", api.RuleDispositionStrict))
			}
			if strings.TrimSpace(resolveOverview(meta, answers)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_overview", "missing overview", api.RuleDispositionStrict))
			}
			return failures, nil
		},
	}
}

func validatePayloadFields(meta api.UploadSubject, fields map[string]string) string {
	if !meta.Anime && strings.TrimSpace(fields["imdb"]) == "" {
		return "missing IMDb ID"
	}
	if strings.TrimSpace(fields["capa"]) == "" {
		return "missing poster URL"
	}
	if strings.TrimSpace(fields["genre"]) == "" {
		return "missing genre"
	}
	if strings.TrimSpace(resolveOverview(meta, standalone.QuestionnaireAnswers(meta, "ASC"))) == "" {
		return "missing overview"
	}
	return ""
}
