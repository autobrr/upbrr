// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// maxReleaseYear is RMC's latest accepted release year.
const maxReleaseYear = 2000

// checkRequirements returns strict failures when a selected TMDB ID lacks
// current title/year metadata or identifies a release after 2000. The profile
// metadata policy handles missing TMDB IDs.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if subject.Identity.TMDBID <= 0 {
		return nil, nil
	}
	tmdb := currentRMCTMDB(subject.SourcePath, subject.Identity, subject.ProviderMetadata)
	if tmdb == nil || strings.TrimSpace(tmdb.Title) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_tmdb_metadata",
			"RMC requires current TMDB metadata for the selected TMDB ID.",
			api.RuleDispositionStrict,
		)}, nil
	}
	if tmdb.Year <= 0 {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_release_year",
			"RMC requires a release year from current TMDB metadata.",
			api.RuleDispositionStrict,
		)}, nil
	}
	if tmdb.Year > maxReleaseYear {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_release_year",
			"RMC only allows TMDB releases from 2000 or earlier.",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
