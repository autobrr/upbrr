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

func Rules() *trackers.RuleSet { return &trackers.RuleSet{ExtraCheck: checkRegion} }

func checkRegion(ctx context.Context, meta api.RuleSubject, _ api.Logger) trackers.RuleResult {
	if err := ctx.Err(); err != nil {
		return trackers.RuleFail(fmt.Errorf("context canceled: %w", err).Error())
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD") {
		return trackers.RulePass()
	}
	if strings.TrimSpace(meta.Region) == "" {
		return trackers.RuleFail("Region required; skipping SHRI.")
	}
	return trackers.RulePass()
}
