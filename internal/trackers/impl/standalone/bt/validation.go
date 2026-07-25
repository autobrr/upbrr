// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-bt-constructibility-v1",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			answers := standalone.QuestionnaireAnswers(meta, "BT")
			ptBR := api.ExtractTrackerLocalizedPTBR(meta)
			failures := make([]api.RuleFailure, 0, 3)
			if strings.TrimSpace(resolvePoster(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_poster", "missing poster URL", api.RuleDispositionStrict))
			}
			if metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["overview"]), resolveOverview(meta, ptBR)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_overview", "missing overview", api.RuleDispositionStrict))
			}
			if metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["tags"]), resolveTags(meta, ptBR)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_tags", "missing tags", api.RuleDispositionStrict))
			}
			return failures, nil
		},
	}
}
