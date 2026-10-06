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

func TestPackMediaRequirementPresenceUsesCollectedReports(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		facts api.MediaFileFacts
		want  bool
	}{
		{name: "missing reports"},
		{name: "no expected file count", facts: api.MediaFileFacts{Files: []api.MediaFileFact{{VideoTrackCount: 1}}}},
		{name: "missing file report", facts: api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{{VideoTrackCount: 1}}}},
		{name: "failed file report", facts: api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{{VideoTrackCount: 1}, {}}}},
		{
			name: "all reports with unresolved source",
			facts: api.MediaFileFacts{
				ExpectedFileCount: 2,
				Status:            api.MetadataEvidenceStatusPartial,
				TechnicalStatus:   api.MetadataEvidenceStatusPartial,
				Files:             []api.MediaFileFact{{VideoTrackCount: 1}, {VideoTrackCount: 1}},
			},
			want: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{
				TVPack:            true,
				MediaFileFacts:    test.facts,
				MetadataOverrides: api.MetadataOverrides{AudioLanguages: new([]string{"English"}), SubtitleLanguages: new([]string{"English"})},
			}
			if got := metadataRequirementFieldPresentForCollection(api.MetadataRequirementNonDiscTVPackMedia, meta, api.ExternalIdentity{}, api.SourceScopedMetadata{}); got != test.want {
				t.Fatalf("collected reports present=%t, want %t", got, test.want)
			}
		})
	}
}

func TestPackMediaRequirementNeverRefreshesProviders(t *testing.T) {
	t.Parallel()
	field := api.MetadataRequirementNonDiscTVPackMedia
	localOnly := api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{Scope: api.MetadataRequirementScopeTV, AnyOf: []api.MetadataRequirementField{field}}}}
	missing := preparationstate.State{
		TVPack:            true,
		MetadataOverrides: api.MetadataOverrides{OriginalLanguage: new("English"), AudioLanguages: new([]string{"English"})},
	}
	for _, provider := range []api.IdentityProvider{api.IdentityProviderTMDB, api.IdentityProviderIMDB, api.IdentityProviderTVDB, api.IdentityProviderTVmaze, api.IdentityProviderMAL} {
		if providerSuppliesMetadataRequirement(provider, field, missing) || requiresProviderMetadataRefresh(localOnly, api.CanonicalCategoryTV, missing, api.ExternalIdentity{}, api.SourceScopedMetadata{}, provider) {
			t.Fatalf("local pack demand triggered %s refresh", provider)
		}
	}
	if metadataRequirementOverrideBlocksProvider(field, missing.MetadataOverrides) {
		t.Fatal("ordinary metadata correction claimed authority over local pack evidence")
	}
	anyOf := api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{Scope: api.MetadataRequirementScopeTV, AnyOf: []api.MetadataRequirementField{field, "tmdb"}}}}
	if !requiresProviderMetadataRefresh(anyOf, api.CanonicalCategoryTV, missing, api.ExternalIdentity{}, api.SourceScopedMetadata{}, api.IdentityProviderTMDB) {
		t.Fatal("missing local evidence prevented the allowed provider alternative")
	}
	collected := missing
	collected.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{{VideoTrackCount: 1}, {VideoTrackCount: 1}}}
	if requiresProviderMetadataRefresh(anyOf, api.CanonicalCategoryTV, collected, api.ExternalIdentity{}, api.SourceScopedMetadata{}, api.IdentityProviderTMDB) {
		t.Fatal("collected AnyOf alternative unnecessarily refreshed TMDB")
	}
}
