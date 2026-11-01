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
		ID:                 "standalone-ptp-constructibility-v8",
		LanguageAssessment: languageFailures,
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			failures := make([]api.RuleFailure, 0, 3)
			isDVD := strings.EqualFold(strings.TrimSpace(subject.DiscType), "DVD")
			if strings.TrimSpace(subject.DiscType) == "" {
				switch strings.ToUpper(strings.TrimSpace(meta.Source)) {
				case "DVD", "PAL", "NTSC", "PAL DVD", "NTSC DVD":
					isDVD = true
				}
			}
			if isDVD && trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_disc",
					"Full DVD disc uploads to PTP are unsupported because PTP requires a MediaInfo version that upbrr does not support.",
					api.RuleDispositionStrict,
				))
			}
			if reason := validateNoEnglishSubtitles(meta); reason != "" {
				failures = append(failures, trackers.NewQuestionnaireRuleFailure("tracker_input.no_english_subtitles", reason))
			}
			category, categoryErr := meta.Identity.RequireCategory()
			if categoryErr != nil || category != api.CanonicalCategoryMovie {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_category",
					"PTP requires a canonical movie category",
					api.RuleDispositionStrict,
				))
			}
			resolution, _, _ := resolveResolution(meta)
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
			if resolveContainer(meta) == "MP4" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_container",
					"PTP requires MP4 releases to be remuxed to MKV",
					api.RuleDispositionStrict,
				))
			}
			failures = append(failures, languageFailures(subject)...)
			return failures, nil
		},
	}
}
