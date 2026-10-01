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

// validationPolicy strictly requires a region for DVD and HDDVD uploads.
func validationPolicy(regionID, distributorID func(string) string) trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "unit3d-shri-region-v2",
		Check: func(ctx context.Context, meta api.TrackerValidationSubject, logger api.Logger) ([]api.RuleFailure, error) {
			return checkRegion(ctx, meta, logger, regionID, distributorID)
		},
	}
}

func checkRegion(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger, regionID, distributorID func(string) string) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD") {
		if strings.TrimSpace(meta.Region) != "" && regionID(meta.Region) == "" {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"unsupported_region",
				"SHRI region must be a known country code or positive tracker ID.",
				api.RuleDispositionStrict,
			)}, nil
		}
		if strings.TrimSpace(meta.Distributor) != "" && distributorID(meta.Distributor) == "" {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"unsupported_distributor",
				"SHRI distributor must be a known publisher or positive tracker ID.",
				api.RuleDispositionStrict,
			)}, nil
		}
		return nil, nil
	}
	if strings.TrimSpace(meta.Region) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure("region_required", "Region required; skipping SHRI.", api.RuleDispositionStrict)}, nil
	}
	if regionID(meta.Region) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"unsupported_region",
			"SHRI region must be a known country code or positive tracker ID.",
			api.RuleDispositionStrict,
		)}, nil
	}
	if strings.TrimSpace(meta.Distributor) != "" && distributorID(meta.Distributor) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"unsupported_distributor",
			"SHRI distributor must be a known publisher or positive tracker ID.",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
