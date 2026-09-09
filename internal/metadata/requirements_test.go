// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRequiresProviderMetadataRefresh(t *testing.T) {
	t.Parallel()

	genres := []string{"Drama"}
	clearedLanguage := ""
	identity := api.ExternalIdentity{
		Category: api.CanonicalCategoryMovie,
		TMDBID:   11,
		IMDBID:   22,
	}
	metadata := api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{
			TMDBID:   11,
			Category: "movie",
			Title:    "Example",
		},
		IMDB: &api.IMDBMetadata{IMDBID: 22, Title: "Example"},
	}
	for _, test := range []struct {
		name     string
		set      api.MetadataRequirementSet
		meta     preparationstate.State
		provider api.IdentityProvider
		want     bool
	}{
		{
			name: "missing provider field",
			set: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
				Scope: api.MetadataRequirementScopeAny,
				AnyOf: []api.MetadataRequirementField{"tmdb_origin_countries"},
			}}},
			provider: api.IdentityProviderTMDB,
			want:     true,
		},
		{
			name: "available anyof alternative",
			set: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
				Scope: api.MetadataRequirementScopeAny,
				AnyOf: []api.MetadataRequirementField{"tmdb_origin_countries", "imdb"},
			}}},
			provider: api.IdentityProviderTMDB,
		},
		{
			name: "out of scope category",
			set: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
				Scope: api.MetadataRequirementScopeTV,
				AnyOf: []api.MetadataRequirementField{"tmdb_origin_countries"},
			}}},
			provider: api.IdentityProviderTMDB,
		},
		{
			name: "manual genres correction",
			set: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
				Scope: api.MetadataRequirementScopeAny,
				AnyOf: []api.MetadataRequirementField{"genres"},
			}}},
			meta:     preparationstate.State{MetadataOverrides: api.MetadataOverrides{Genres: &genres}},
			provider: api.IdentityProviderTMDB,
		},
		{
			name: "manual empty language correction",
			set: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
				Scope: api.MetadataRequirementScopeAny,
				AnyOf: []api.MetadataRequirementField{"original_language"},
			}}},
			meta:     preparationstate.State{MetadataOverrides: api.MetadataOverrides{OriginalLanguage: &clearedLanguage}},
			provider: api.IdentityProviderTMDB,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := requiresProviderMetadataRefresh(test.set, identity.Category, test.meta, identity, metadata, test.provider); got != test.want {
				t.Fatalf("requiresProviderMetadataRefresh() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRequiresTMDBLocalizedPTBRRefreshPreservesAnyOf(t *testing.T) {
	t.Parallel()

	identity := api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 22}
	metadata := api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 22, Title: "Example"}}
	set := api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
		Scope: api.MetadataRequirementScopeAny,
		AnyOf: []api.MetadataRequirementField{"tmdb_localized_pt_br", "imdb"},
	}}}
	if requiresTMDBLocalizedPTBRRefresh(set, identity.Category, preparationstate.State{}, identity, metadata) {
		t.Fatal("localized TMDB demand refreshed despite a cached AnyOf alternative")
	}
}

func TestManualYearClearDoesNotFallBackToProviderEvidence(t *testing.T) {
	t.Parallel()

	identity := api.ExternalIdentity{TMDBID: 11}
	metadata := api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 11, Year: 2026}}
	meta := preparationstate.State{ReleaseNameOverrides: api.ReleaseNameOverrides{ManualYear: new(0)}}
	if metadataRequirementFieldPresentForCollection("year", meta, identity, metadata) {
		t.Fatal("manual clear year accepted provider fallback")
	}
	meta.ReleaseNameOverrides.ManualYear = new(2027)
	if !metadataRequirementFieldPresentForCollection("year", meta, identity, metadata) {
		t.Fatal("positive manual year did not satisfy requirement")
	}
}
