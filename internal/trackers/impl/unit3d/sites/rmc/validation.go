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

// checkRequirements requires current matching TMDB metadata, then validates
// the effective title and year against RMC's release policy. The profile
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
	title := tmdb.Title
	if subject.EffectiveMetadata.TitleProvenance.IsManual() {
		title = subject.EffectiveMetadata.Title
	}
	if strings.TrimSpace(title) == "" {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_tmdb_metadata",
			"RMC requires a resolved title from current TMDB metadata.",
			api.RuleDispositionStrict,
		)}, nil
	}
	year := tmdb.Year
	if subject.EffectiveMetadata.YearProvenance.IsManual() {
		year = subject.EffectiveMetadata.Year
	}
	if year <= 0 {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_release_year",
			"RMC requires a resolved release year from current TMDB metadata.",
			api.RuleDispositionStrict,
		)}, nil
	}
	if year > maxReleaseYear {
		return []api.RuleFailure{trackers.NewRuleFailure(
			"rmc_release_year",
			"RMC only allows TMDB releases from 2000 or earlier.",
			api.RuleDispositionStrict,
		)}, nil
	}
	return nil, nil
}
