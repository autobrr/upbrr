// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// maxReleaseYear is RMC's latest accepted release year.
const maxReleaseYear = 2000

// checkRequirements rejects releases newer than RMC's accepted era.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if subject.Release.Year > maxReleaseYear {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_release_year",
			"RMC only allows movies released in 2000 or earlier.",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
