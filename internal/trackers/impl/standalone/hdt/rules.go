// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// validationPolicy strictly blocks uploads whose resolution is unknown.
func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "standalone-hdt-constructibility-v1", Check: checkRules}
}

func checkRules(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if strings.TrimSpace(meta.Release.Resolution) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"resolution_required",
			"missing resolution",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
