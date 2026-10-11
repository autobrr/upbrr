// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedRequirementsSatisfied(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		fields []api.MetadataRequirementField
		scope  api.MetadataRequirementScope
		mutate func(*api.PreparedRelease, *api.ReleaseFactInstructions)
		want   bool
	}{
		{
			name:   "matching provider",
			fields: []api.MetadataRequirementField{"tmdb"},
			want:   true,
		},
		{name: "missing provider", fields: []api.MetadataRequirementField{"tvdb"}},
		{
			name:   "available alternative",
			fields: []api.MetadataRequirementField{"tvdb", "tmdb"},
			want:   true,
		},
		{
			name:   "inapplicable category",
			fields: []api.MetadataRequirementField{"tvdb"},
			scope:  api.MetadataRequirementScopeTV,
			want:   true,
		},
		{
			name:   "stale provider identity",
			fields: []api.MetadataRequirementField{"tmdb"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.ProviderMetadata.TMDB.TMDBID++
			},
		},
		{
			name:   "wrong provider category",
			fields: []api.MetadataRequirementField{"tmdb"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.ProviderMetadata.TMDB.Category = "tv"
			},
		},
		{
			name:   "identity only",
			fields: []api.MetadataRequirementField{"tmdb_id_only"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.ProviderMetadata.TMDB = nil
			},
			want: true,
		},
		{
			name:   "manual genres",
			fields: []api.MetadataRequirementField{"genres"},
			mutate: func(_ *api.PreparedRelease, i *api.ReleaseFactInstructions) {
				i.Metadata.Genres = new([]string{"Drama"})
			},
			want: true,
		},
		{
			name:   "manual genres clear",
			fields: []api.MetadataRequirementField{"genres"},
			mutate: func(r *api.PreparedRelease, i *api.ReleaseFactInstructions) {
				r.ProviderMetadata.TMDB.Genres = "Drama"
				i.Metadata.Genres = new([]string{})
			},
		},
		{
			name:   "manual language clear",
			fields: []api.MetadataRequirementField{"original_language"},
			mutate: func(r *api.PreparedRelease, i *api.ReleaseFactInstructions) {
				r.ProviderMetadata.TMDB.OriginalLanguage = "en"
				i.Metadata.OriginalLanguage = new("")
			},
		},
		{
			name:   "manual movie year clear",
			fields: []api.MetadataRequirementField{"year"},
			mutate: func(_ *api.PreparedRelease, i *api.ReleaseFactInstructions) {
				i.ReleaseName.ManualYear = new(0)
			},
		},
		{
			name:   "TV year requires alias evidence",
			fields: []api.MetadataRequirementField{"year"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.Identity.Category = api.CanonicalCategoryTV
				r.Identity.TVDBID = 22
				r.ProviderMetadata.TVDB = &api.TVDBMetadata{TVDBID: 22, Year: 2026}
			},
		},
		{
			name:   "TV alias year",
			fields: []api.MetadataRequirementField{"year"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.Identity.Category = api.CanonicalCategoryTV
				r.Identity.TVDBID = 22
				r.ProviderMetadata.TVDB = &api.TVDBMetadata{
					TVDBID:        22,
					Year:          2026,
					YearFromAlias: true,
				}
			},
			want: true,
		},
		{
			name:   "movie localization",
			fields: []api.MetadataRequirementField{"tmdb_localized_pt_br"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.ProviderMetadata.TMDB.Localized = map[string]api.TMDBLocalizedData{"pt-BR": {Title: "Example", Overview: "Example overview"}}
			},
			want: true,
		},
		{
			name:   "TV localization needs episode overview",
			fields: []api.MetadataRequirementField{"tmdb_localized_pt_br"},
			mutate: func(r *api.PreparedRelease, _ *api.ReleaseFactInstructions) {
				r.Identity.Category = api.CanonicalCategoryTV
				r.Episode.Season = 1
				r.ProviderMetadata.TMDB.Category = "tv"
				r.ProviderMetadata.TMDB.Localized = map[string]api.TMDBLocalizedData{"pt-BR": {Title: "Example", Overview: "Example overview"}}
			},
		},
		{name: "unknown field", fields: []api.MetadataRequirementField{"future_field"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			release := api.PreparedRelease{
				Naming:   api.NamingFacts{Title: "Example", Year: 2026},
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 11},
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
					TMDBID:   11,
					Category: "movie",
					Title:    "Example",
					Year:     2026,
				}},
			}
			instructions := api.ReleaseFactInstructions{}
			if test.mutate != nil {
				test.mutate(&release, &instructions)
			}
			set := api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{Scope: test.scope, AnyOf: test.fields}}}
			if got := PreparedRequirementsSatisfied(set, release, instructions); got != test.want {
				t.Fatalf("PreparedRequirementsSatisfied() = %t, want %t", got, test.want)
			}
		})
	}
}

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

	identity := api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 11}
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

func TestLockedMetadataOverridesDoNotSatisfyRequirements(t *testing.T) {
	t.Parallel()

	meta := preparationstate.State{MetadataOverrides: api.MetadataOverrides{
		Title:         new("Legacy Title"),
		OriginalTitle: new("Legacy Original"),
	}}
	if metadataRequirementFieldPresentForCollection("title", meta, api.ExternalIdentity{}, api.SourceScopedMetadata{}) {
		t.Fatal("legacy title override satisfied title requirement")
	}
	if metadataRequirementFieldPresentForCollection("original_title", meta, api.ExternalIdentity{}, api.SourceScopedMetadata{}) {
		t.Fatal("legacy original title override satisfied original-title requirement")
	}
	if !providerSuppliesMetadataRequirement(api.IdentityProviderTMDB, "title", meta) ||
		!providerSuppliesMetadataRequirement(api.IdentityProviderTMDB, "original_title", meta) {
		t.Fatal("legacy title override blocked provider enrichment")
	}
}

func TestTVYearRequirementRequiresMatchingAliasDerivedTVDBYear(t *testing.T) {
	t.Parallel()

	identity := api.ExternalIdentity{
		Category: api.CanonicalCategoryTV,
		TVDBID:   22,
		TMDBID:   11,
		IMDBID:   33,
	}
	metadata := api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{TMDBID: 11, Year: 2026},
		IMDB: &api.IMDBMetadata{IMDBID: 33, Year: 2027},
		TVDB: &api.TVDBMetadata{TVDBID: 22, Year: 2024},
	}
	meta := preparationstate.State{Release: api.ReleaseInfo{Year: 2025}, ReleaseNameOverrides: api.ReleaseNameOverrides{ManualYear: new(2030)}}
	if metadataRequirementFieldPresentForCollection("year", meta, identity, metadata) {
		t.Fatal("TV year requirement accepted a non-alias TVDB or fallback year")
	}
	metadata.TVDB.YearFromAlias = true
	if !metadataRequirementFieldPresentForCollection("year", meta, identity, metadata) {
		t.Fatal("matching alias-derived TVDB year did not satisfy requirement")
	}
}
