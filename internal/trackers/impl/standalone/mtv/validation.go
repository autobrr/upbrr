// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-mtv-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 3)
			if resolveCategoryID(meta) == "0" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_category",
					"release does not map to an MTV category",
					api.RuleDispositionStrict,
				))
			}
			if resolveSourceID(meta) == "0" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_source",
					"release does not map to an MTV source",
					api.RuleDispositionStrict,
				))
			}
			if resolveResolutionID(meta) == "10" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_resolution",
					"release does not map to an MTV resolution",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}
