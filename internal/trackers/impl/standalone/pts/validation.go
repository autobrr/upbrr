// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-pts-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			if reason := validateUpload(standalone.UploadSubjectForValidation(subject)); reason != "" {
				return []api.RuleFailure{trackers.NewRuleFailure(
					"required_questionnaire_answer",
					reason,
					api.RuleDispositionStrict,
				)}, nil
			}
			return nil, nil
		},
	}
}
