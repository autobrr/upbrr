// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

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
		ID: "standalone-ptp-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 2)
			if _, err := meta.Identity.RequireCategory(); err != nil {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_category",
					"PTP requires a canonical movie or TV-pack category",
					api.RuleDispositionStrict,
				))
			}
			resolution, _ := resolveResolution(meta)
			for _, value := range []string{
				resolveType(meta),
				resolveCodec(meta),
				resolveContainer(meta),
				resolution,
				resolveSource(meta.Source),
			} {
				if strings.TrimSpace(value) == "" {
					failures = append(failures, trackers.NewRuleFailure(
						"unsupported_taxonomy",
						"release does not map to PTP upload taxonomy",
						api.RuleDispositionStrict,
					))
					break
				}
			}
			return failures, nil
		},
	}
}
