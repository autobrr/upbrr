// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

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
		ID: "standalone-tl-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 2)
			category, categoryErr := meta.Identity.RequireCategory()
			if categoryErr != nil || strings.TrimSpace(resolveCategory(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_category",
					"release does not map to a TL category",
					api.RuleDispositionStrict,
				))
			}
			switch {
			case meta.Anime && meta.Identity.MALID <= 0:
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"TL anime uploads require a MAL ID",
					api.RuleDispositionStrict,
				))
			case !meta.Anime && category == api.CanonicalCategoryMovie && meta.Identity.IMDBID <= 0:
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"TL movie uploads require an IMDb ID",
					api.RuleDispositionStrict,
				))
			case !meta.Anime && category == api.CanonicalCategoryTV && meta.Identity.TVmazeID <= 0:
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"TL TV uploads require a TVmaze ID",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}
