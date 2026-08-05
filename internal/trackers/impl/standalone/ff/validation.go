// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

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
		ID: "standalone-ff-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 4)
			if !meta.Anime {
				if strings.TrimSpace(categoryOf(meta)) == "" {
					failures = append(failures, trackers.NewRuleFailure(
						"unsupported_category",
						"release does not have a supported FF category",
						api.RuleDispositionStrict,
					))
				}
				if meta.Identity.IMDBID <= 0 {
					failures = append(failures, trackers.NewRuleFailure(
						"required_provider_id",
						"FF requires an IMDb ID for movie and TV uploads",
						api.RuleDispositionStrict,
					))
				}
				if !ffSourceSupported(meta.Source) {
					failures = append(failures, trackers.NewRuleFailure(
						"unsupported_source",
						"release source does not map to an FF source",
						api.RuleDispositionStrict,
					))
				}
			}
			if !strings.EqualFold(strings.TrimSpace(meta.Source), "DVD") &&
				strings.TrimSpace(meta.VideoCodec) == "" &&
				strings.TrimSpace(meta.VideoEncode) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_codec",
					"FF requires a canonical video codec",
					api.RuleDispositionStrict,
				))
			}
			if meta.Anime && strings.TrimSpace(meta.Release.Resolution) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_resolution",
					"FF anime uploads require a canonical resolution",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}

func ffSourceSupported(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dvd", "blu-ray", "bluray", "hdtv", "webrip", "webdl", "web":
		return true
	default:
		return false
	}
}
