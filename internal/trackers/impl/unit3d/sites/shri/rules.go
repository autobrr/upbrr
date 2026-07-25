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

// ValidationPolicy strictly requires a region for DVD and HDDVD uploads.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-shri-region-v1", Check: checkRegion}
}

func checkRegion(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD") {
		if strings.TrimSpace(meta.Region) != "" && numericValue(meta.Region) == "" {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"unsupported_region",
				"SHRI region must be a numeric tracker ID.",
				api.RuleDispositionStrict,
			)}, nil
		}
		if strings.TrimSpace(meta.Distributor) != "" && numericValue(meta.Distributor) == "" {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"unsupported_distributor",
				"SHRI distributor must be a numeric tracker ID.",
				api.RuleDispositionStrict,
			)}, nil
		}
		return nil, nil
	}
	if strings.TrimSpace(meta.Region) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure("region_required", "Region required; skipping SHRI.", api.RuleDispositionStrict)}, nil
	}
	if numericValue(meta.Region) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"unsupported_region",
			"SHRI region must be a numeric tracker ID.",
			api.RuleDispositionStrict,
		)}, nil
	}
	if strings.TrimSpace(meta.Distributor) != "" && numericValue(meta.Distributor) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"unsupported_distributor",
			"SHRI distributor must be a numeric tracker ID.",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
