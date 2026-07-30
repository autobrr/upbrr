// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const minimumContentAgeReason = "content must be at least 10 years old"

// validationPolicy strictly blocks content newer than RTF's ten-year
// eligibility cutoff and adult-classified releases.
func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "standalone-rtf-constructibility-v1", Check: checkRules}
}

func checkRules(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 2)
	uploadSubject := standalone.UploadSubjectForValidation(meta)
	genres := strings.ToLower(genresText(uploadSubject) + "," + keywordsText(uploadSubject))
	for _, value := range []string{"xxx", "erotic", "porn", "adult", "orgy"} {
		if strings.Contains(genres, value) {
			failures = append(failures, trackers.NewRuleFailure(
				"block_adult",
				"adult content is not allowed",
				api.RuleDispositionStrict,
			))
			break
		}
	}
	if minimumContentAgeViolation(meta, time.Now().UTC()) {
		failures = append(failures, trackers.NewRuleFailure(
			"minimum_content_age",
			minimumContentAgeReason,
			api.RuleDispositionStrict,
		))
	}
	return failures, nil
}

func minimumContentAgeViolation(meta api.TrackerValidationSubject, now time.Time) bool {
	limit := now.UTC().AddDate(-10, 0, 3)
	if releaseDate := ruleReleaseDate(meta); !releaseDate.IsZero() {
		return releaseDate.After(limit)
	}
	return ruleReleaseYear(meta) > limit.Year()
}

func ruleReleaseDate(meta api.TrackerValidationSubject) time.Time {
	if meta.ProviderMetadata.TMDB == nil {
		return time.Time{}
	}
	for _, value := range []string{
		strings.TrimSpace(meta.ProviderMetadata.TMDB.ReleaseDate),
		strings.TrimSpace(meta.ProviderMetadata.TMDB.LastAirDate),
		strings.TrimSpace(meta.ProviderMetadata.TMDB.FirstAirDate),
	} {
		if value == "" {
			continue
		}
		if releaseDate, err := time.Parse("2006-01-02", value); err == nil {
			return releaseDate
		}
	}
	return time.Time{}
}

func ruleReleaseYear(meta api.TrackerValidationSubject) int {
	if meta.Release.Year > 0 {
		return meta.Release.Year
	}
	if meta.ProviderMetadata.TMDB == nil {
		return 0
	}
	return meta.ProviderMetadata.TMDB.Year
}
