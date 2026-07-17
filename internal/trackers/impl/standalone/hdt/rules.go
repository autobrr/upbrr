// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func rules() *trackers.RuleSet { return &trackers.RuleSet{Check: checkRules} }

func checkRules(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if trackers.ResolveRuleResolution(meta) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"resolution_required",
			"missing resolution",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
