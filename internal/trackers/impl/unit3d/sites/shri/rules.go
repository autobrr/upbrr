// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *trackers.RuleSet { return &trackers.RuleSet{Check: checkRegion} }

func checkRegion(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD") {
		return nil, nil
	}
	if strings.TrimSpace(meta.Region) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure("region_required", "Region required; skipping SHRI.", api.RuleDispositionStrict)}, nil
	}
	return nil, nil
}
