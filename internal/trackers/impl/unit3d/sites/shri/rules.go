// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *ruletypes.RuleSet { return &ruletypes.RuleSet{ExtraCheck: checkRegion} }

func checkRegion(ctx context.Context, meta api.RuleSubject, _ api.Logger) ruletypes.Result {
	if err := ctx.Err(); err != nil {
		return ruletypes.Fail(fmt.Errorf("context canceled: %w", err).Error())
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD") {
		return ruletypes.Pass()
	}
	if strings.TrimSpace(meta.Region) == "" {
		return ruletypes.Fail("Region required; skipping SHRI.")
	}
	return ruletypes.Pass()
}
