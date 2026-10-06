// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-fld-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 3)

			if meta.Identity.IMDBID <= 0 && meta.Identity.TMDBID <= 0 {
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"missing IMDb or TMDb ID",
					api.RuleDispositionStrict,
				))
			}

			if !standalone.PreparedMediaReady(subject) {
				failures = append(failures, trackers.NewRuleFailure(
					"prepared_media_missing",
					"FLD requires prepared MediaInfo or BDInfo text",
					api.RuleDispositionStrict,
				))
			}

			if err := validateFLDRequirements(meta); err != nil {
				failures = append(failures, trackers.NewRuleFailure(
					"disallowed_content",
					err.Error(),
					api.RuleDispositionStrict,
				))
			}

			return failures, nil
		},
	}
}

func validateFLDRequirements(meta api.UploadSubject) error {
	if isDiscType(meta.DiscType) {
		return nil
	}

	container := strings.ToLower(strings.TrimSpace(meta.Container))
	if container != "" {
		allowed := []string{"mkv", "mp4"}
		if strings.EqualFold(strings.TrimSpace(meta.Type), "HDTV") {
			allowed = append(allowed, "ts")
		}

		if !slices.Contains(allowed, container) {
			return fmt.Errorf(
				"container %q is not allowed for %s: only %s are permitted",
				meta.Container,
				meta.Type,
				strings.ToUpper(strings.Join(allowed, ", ")),
			)
		}
	}

	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if resolution != "" {
		heightStr := strings.TrimSuffix(strings.TrimSuffix(resolution, "p"), "i")
		if height, err := strconv.Atoi(heightStr); err == nil && height > 0 && height < 1080 {
			return fmt.Errorf("resolution %s is not allowed: only 1080p and above are permitted", resolution)
		}
	}

	return nil
}
