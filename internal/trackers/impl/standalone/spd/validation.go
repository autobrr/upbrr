// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

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
		ID: "standalone-spd-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 2)
			if strings.TrimSpace(resolveCategory(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_category",
					"release does not map to an SPD category",
					api.RuleDispositionStrict,
				))
			}
			if !standalone.PreparedMediaReady(subject) {
				failures = append(failures, trackers.NewRuleFailure(
					"prepared_media_missing",
					"SPD requires prepared MediaInfo or BDInfo text",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}
