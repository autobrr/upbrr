// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy(regionID, distributorID func(string) string) trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "unit3d-acm-payload-v2",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			failures := make([]api.RuleFailure, 0, 2)
			if strings.TrimSpace(subject.Region) != "" && regionID(subject.Region) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_region",
					"ACM region must be a known country code or positive tracker ID",
					api.RuleDispositionStrict,
				))
			}
			if strings.TrimSpace(subject.Distributor) != "" && distributorID(subject.Distributor) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"unsupported_distributor",
					"ACM distributor must be a known publisher or positive tracker ID",
					api.RuleDispositionStrict,
				))
			}
			return failures, nil
		},
	}
}
