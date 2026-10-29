// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.WithLanguageAssessment(trackers.ValidationPolicyBinding{
		ID: "standalone-ant-constructibility-v6-tv-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			typeName, _ := resolveType(meta, standalone.QuestionnaireAnswers(meta, "ANT"))
			failures := make([]api.RuleFailure, 0, 2)
			if strings.TrimSpace(typeName) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_type",
					"ANT requires a movie, TV season, or TV episode",
					api.RuleDispositionStrict,
				))
			}
			if _, err := requestID(standalone.QuestionnaireAnswers(meta, "ANT")); err != nil {
				failures = append(failures, trackers.NewQuestionnaireRuleFailure("invalid_request_id", err.Error()))
			}
			if !standalone.PreparedMediaReady(subject) {
				failures = append(failures, trackers.NewRuleFailure(
					"prepared_media_missing",
					"ANT requires prepared MediaInfo or BDInfo text",
					api.RuleDispositionStrict,
				))
			}
			if subject.Identity.Category == api.CanonicalCategoryTV {
				if _, err := tvCoordinates(meta); err != nil {
					failures = append(failures, trackers.NewRuleFailure("invalid_tv_coordinates", err.Error(), api.RuleDispositionStrict))
				}
				tvFailures, err := checkTVEvidenceRules(ctx, subject, nil)
				if err != nil {
					return nil, err
				}
				failures = append(failures, tvFailures...)
			}
			return failures, nil
		},
	}, func(subject api.TrackerValidationSubject) []api.RuleFailure {
		if subject.Identity.Category != api.CanonicalCategoryTV {
			return nil
		}
		return append(trackers.EvaluateLanguagePolicy(subject, languagePolicy()), fullDiscLanguageFailures(subject)...)
	})
}
