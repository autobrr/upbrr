// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMetadataPolicyDefaultsToStrictTMDB(t *testing.T) {
	t.Parallel()

	definition := NewWithProfile(Profile{Name: "EXAMPLE"})
	policy := definition.MetadataPolicy()
	if len(policy.Requirements) != 1 {
		t.Fatalf("requirements = %#v", policy.Requirements)
	}
	requirement := policy.Requirements[0]
	if requirement.Disposition != api.RuleDispositionStrict ||
		len(requirement.AnyOf) != 1 || requirement.AnyOf[0] != trackers.MetadataFieldTMDB {
		t.Fatalf("default requirement = %#v", requirement)
	}

	policy.Requirements[0].Disposition = api.RuleDispositionWaivable
	if got := definition.MetadataPolicy().Requirements[0].Disposition; got != api.RuleDispositionStrict {
		t.Fatalf("mutated default disposition = %q", got)
	}
}

func TestMetadataPolicyAllowsSiteOverride(t *testing.T) {
	t.Parallel()

	override := &trackers.TrackerMetadataPolicy{Requirements: []trackers.MetadataRequirement{{
		Scope:       trackers.MetadataScopeAny,
		AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
		Disposition: api.RuleDispositionWaivable,
	}}}
	definition := NewWithProfile(Profile{Name: "EXAMPLE", MetadataPolicy: override})
	override.Requirements[0].Disposition = api.RuleDispositionStrict
	override.Requirements[0].AnyOf[0] = trackers.MetadataFieldIMDB

	policy := definition.MetadataPolicy()
	if policy.Requirements[0].Disposition != api.RuleDispositionWaivable ||
		policy.Requirements[0].AnyOf[0] != trackers.MetadataFieldTMDB {
		t.Fatalf("site override = %#v", policy.Requirements[0])
	}

	policy.Requirements[0].AnyOf[0] = trackers.MetadataFieldIMDB
	if got := definition.MetadataPolicy().Requirements[0].AnyOf[0]; got != trackers.MetadataFieldTMDB {
		t.Fatalf("mutated site override field = %q", got)
	}
}

func TestMetadataHelpersUseManualGenresAndAutomaticNamedProviders(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: ""}, IMDB: &api.IMDBMetadata{Genres: "IMDb"}}, EffectiveMetadata: api.EffectiveMetadata{Genres: []string{"Automatic"}}}
	if got := resolveTMDBGenres(meta); got != "" {
		t.Fatalf("automatic TMDB genres = %q", got)
	}
	if got := resolveIMDBGenres(meta); got != "IMDb" {
		t.Fatalf("automatic IMDb genres = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{Genres: []string{"Manual"}, GenresProvenance: api.FactProvenanceManual}
	if got := resolveTMDBGenres(meta); got != "Manual" || resolveIMDBGenres(meta) != "Manual" {
		t.Fatalf("manual genres were ignored: TMDB=%q IMDb=%q", resolveTMDBGenres(meta), resolveIMDBGenres(meta))
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{GenresProvenance: api.FactProvenanceManualEmpty}
	if got := resolveTMDBGenres(meta); got != "" || resolveIMDBGenres(meta) != "" {
		t.Fatalf("manual-empty genres were ignored: TMDB=%q IMDb=%q", resolveTMDBGenres(meta), resolveIMDBGenres(meta))
	}
}

func TestResolveOriginalLanguageUsesManualFactsOnlyWhenExplicit(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{OriginalLanguage: "fr"}}, EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "ja"}}
	if got := resolveOriginalLanguage(meta); got != "fr" {
		t.Fatalf("automatic effective language = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManual
	if got := resolveOriginalLanguage(meta); got != "ja" {
		t.Fatalf("manual language = %q", got)
	}
}
