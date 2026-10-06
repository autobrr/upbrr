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
		ID: "standalone-asc-constructibility-v3",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			answers := standalone.QuestionnaireAnswers(meta, "ASC")
			failures := make([]api.RuleFailure, 0, 5)
			if !meta.Anime && strings.TrimSpace(resolveIMDbIDText(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_provider_id", "missing IMDb ID", api.RuleDispositionStrict))
			}
			if strings.TrimSpace(resolvePoster(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_poster", "missing poster URL", api.RuleDispositionStrict))
			}
			if len(resolveGenreIDs(resolveGenres(meta, answers))) == 0 {
				failures = append(failures, trackers.NewRuleFailure("required_genre", "missing genre known to ASC", api.RuleDispositionStrict))
			}
			if strings.TrimSpace(resolveOverview(meta, answers)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_overview", "missing overview", api.RuleDispositionStrict))
			}
			if resolveQualityID(meta) == "" {
				failures = append(failures, trackers.NewRuleFailure("unsupported_type", "release type has no ASC quality", api.RuleDispositionStrict))
			}
			if resolveContainerID(meta) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_container", "missing container", api.RuleDispositionStrict))
			}
			return failures, nil
		},
	}
}

// validatePayloadFields checks prepared payload constructibility and returns
// a blocking reason, or "" when the site's required fields are satisfied.
// Movies, series and anime share the site's video profile requirements.
func validatePayloadFields(meta api.UploadSubject, payload uploadPayload, screenshotCount int, coverURL string) string {
	fields := payload.fields
	switch {
	case !meta.Anime && strings.TrimSpace(fields["imdb_id"]) == "":
		return "missing IMDb ID"
	case strings.TrimSpace(fields["year"]) == "":
		return "missing year"
	case resolveQualityID(meta) == "":
		return "release type has no ASC quality"
	case resolveContainerID(meta) == "":
		return "missing container"
	case strings.TrimSpace(resolveOverview(meta, standalone.QuestionnaireAnswers(meta, "ASC"))) == "":
		return "missing overview"
	case hasForeignImages(fields["description"]):
		return "description embeds images hosted outside ASC"
	case strings.TrimSpace(fields["mediainfo"]) == "":
		return "missing MediaInfo report"
	case len(payload.genreIDs) == 0:
		return "missing genre known to ASC"
	case strings.TrimSpace(fields["width"]) == "" || strings.TrimSpace(fields["height"]) == "":
		return "missing video resolution"
	case strings.TrimSpace(coverURL) == "":
		return "missing poster URL"
	case screenshotCount < minScreenshots:
		return fmt.Sprintf("needs at least %d local screenshots, found %d", minScreenshots, screenshotCount)
	}
	return ""
}
