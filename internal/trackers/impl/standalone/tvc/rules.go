// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func rules() *trackers.RuleSet { return &trackers.RuleSet{Check: checkRules} }

func checkRules(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 3)
	if strings.EqualFold(trackers.ResolveRuleResolution(meta), "2160p") {
		failures = append(failures, trackers.NewRuleFailure(
			"uhd_forbidden",
			"TVC disallows UHD uploads",
			api.RuleDispositionStrict,
		))
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		failures = append(failures, trackers.NewRuleFailure(
			"disc_forbidden",
			"TVC disallows disc uploads",
			api.RuleDispositionStrict,
		))
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		failures = append(failures, trackers.NewRuleFailure(
			"remux_forbidden",
			"TVC disallows remux uploads",
			api.RuleDispositionStrict,
		))
	}
	return failures, nil
}
