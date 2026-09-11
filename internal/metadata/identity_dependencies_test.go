// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveExternalIdentityClearIMDbRefreshesDependentTVDB(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
	tmdbClient := &stubTMDB{}
	imdbClient := &stubIMDB{}
	tvdbClient := &stubTVDB{id: 1003, name: "Example Series"}
	svc := NewService(&fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))

	result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        sourcePath,
		StoredDataFresh:   true,
		MediaInfoCategory: "TV",
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			TMDBID:     1001,
			IMDBID:     1002,
			TVDBID:     1004,
			Dependencies: api.IdentityDependencySet{
				TMDB: api.IdentityDependency{ID: 1001},
				IMDB: api.IdentityDependency{ID: 1002},
				TVDB: api.IdentityDependency{ID: 1004, IMDBID: 1002},
			},
			Provenance: api.IdentityProvenanceSet{
				TMDB: api.IdentityProvenanceProvider,
				IMDB: api.IdentityProvenanceProvider,
				TVDB: api.IdentityProvenanceProvider,
			},
		},
		ExternalIDOverrides: api.ExternalIDOverrides{IMDBID: new(0)},
	})
	if err != nil {
		t.Fatalf("resolve after IMDb removal: %v", err)
	}
	if result.Identity.IMDBID != 0 || result.Identity.TMDBID != 1001 || result.Identity.TVDBID != 1003 {
		t.Fatalf("refreshed identity = %#v", result.Identity)
	}
	if result.Identity.Dependencies.TVDB != (api.IdentityDependency{ID: 1003, TMDBID: 1001}) {
		t.Fatalf("TVDB dependency = %#v", result.Identity.Dependencies.TVDB)
	}
	if len(tvdbClient.externalIMDbIDs) == 0 || tvdbClient.externalIMDbIDs[0] != "" || tvdbClient.externalTMDBIDs[0] != "1001" {
		t.Fatalf("TVDB lookup anchors = IMDb:%v TMDB:%v", tvdbClient.externalIMDbIDs, tvdbClient.externalTMDBIDs)
	}
	if tmdbClient.findCalls+tmdbClient.searchCalls != 0 || imdbClient.searchCalls != 0 {
		t.Fatalf("retained TMDB triggered a search: tmdb_find=%d tmdb_search=%d imdb_search=%d", tmdbClient.findCalls, tmdbClient.searchCalls, imdbClient.searchCalls)
	}
}

func TestResolveExternalIdentityInvalidatesTransitiveDependenciesAndPreservesIndependentFacts(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
	tvdbClient := &stubTVDB{}
	svc := NewService(&fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))

	result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        sourcePath,
		StoredDataFresh:   true,
		MediaInfoCategory: "TV",
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			TMDBID:     1101,
			IMDBID:     1102,
			TVDBID:     1103,
			TVmazeID:   1104,
			MALID:      1105,
			Dependencies: api.IdentityDependencySet{
				TMDB:   api.IdentityDependency{ID: 1101},
				IMDB:   api.IdentityDependency{ID: 1102},
				TVDB:   api.IdentityDependency{ID: 1103, TMDBID: 1101},
				TVmaze: api.IdentityDependency{ID: 1104, TVDBID: 1103},
			},
			Provenance: api.IdentityProvenanceSet{
				TMDB:   api.IdentityProvenanceProvider,
				IMDB:   api.IdentityProvenanceProvider,
				TVDB:   api.IdentityProvenanceProvider,
				TVmaze: api.IdentityProvenanceProvider,
				MAL:    api.IdentityProvenanceTracker,
			},
		},
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: new(0)},
	})
	if err != nil {
		t.Fatalf("resolve after TMDB removal: %v", err)
	}
	if result.Identity.TMDBID != 0 || result.Identity.TVDBID != 0 || result.Identity.TVmazeID != 0 {
		t.Fatalf("dependent IDs survived TMDB removal: %#v", result.Identity)
	}
	if result.Identity.Dependencies.TVDB != (api.IdentityDependency{}) || result.Identity.Dependencies.TVmaze != (api.IdentityDependency{}) {
		t.Fatalf("stale dependencies survived: %#v", result.Identity.Dependencies)
	}
	if result.Identity.IMDBID != 1102 || result.Identity.Dependencies.IMDB != (api.IdentityDependency{ID: 1102}) {
		t.Fatalf("independent automatic IMDb ID changed: %#v", result.Identity)
	}
	if result.Identity.MALID != 1105 || result.Identity.Provenance.MAL != api.IdentityProvenanceTracker {
		t.Fatalf("direct tracker evidence changed: %#v", result.Identity)
	}
	if result.Identity.Overrides.TMDB != api.OverrideStateClear || result.Identity.Provenance.TMDB != api.IdentityProvenanceExplicit {
		t.Fatalf("TMDB clear did not remain authoritative: %#v", result.Identity)
	}

	pinnedTVmazeID := 1199
	pinned, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        filepath.Join(t.TempDir(), "Pinned.Series.S01E01.mkv"),
		MediaInfoCategory: "TV",
		Identity: api.ExternalIdentity{
			TMDBID:   1201,
			TVmazeID: 1202,
			Dependencies: api.IdentityDependencySet{
				TMDB:   api.IdentityDependency{ID: 1201},
				TVmaze: api.IdentityDependency{ID: 1202, TMDBID: 1201},
			},
			Provenance: api.IdentityProvenanceSet{
				TMDB:   api.IdentityProvenanceProvider,
				TVmaze: api.IdentityProvenanceProvider,
			},
		},
		StoredDataFresh: true,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID:   new(0),
			TVmazeID: &pinnedTVmazeID,
		},
	})
	if err != nil {
		t.Fatalf("resolve with pinned TVmaze ID: %v", err)
	}
	if pinned.Identity.TVmazeID != pinnedTVmazeID || pinned.Identity.Provenance.TVmaze != api.IdentityProvenanceExplicit ||
		pinned.Identity.Overrides.TVmaze != api.OverrideStateValue {
		t.Fatalf("pinned TVmaze ID changed: %#v", pinned.Identity)
	}
}

func TestResolveExternalIdentityClearReResolvesLegacyAutomaticIDs(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Legacy.Series.S01E01.mkv")
	imdbClient := &stubIMDB{searchResult: imdb.SearchResult{IMDbID: 1302}}
	tvdbClient := &stubTVDB{id: 1303, name: "Legacy Series"}
	tvmazeClient := &stubTVmaze{result: tvmaze.SearchResult{SelectedID: 1304}}
	svc := NewService(&fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(imdbClient),
		WithTVDBClient(tvdbClient), WithTVmazeClient(tvmazeClient))

	result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        sourcePath,
		StoredDataFresh:   true,
		MediaInfoCategory: "TV",
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			TMDBID:     1301,
			IMDBID:     1392,
			TVDBID:     1393,
			TVmazeID:   1394,
			Provenance: api.IdentityProvenanceSet{
				TMDB:   api.IdentityProvenanceLegacy,
				IMDB:   api.IdentityProvenanceLegacy,
				TVDB:   api.IdentityProvenanceLegacy,
				TVmaze: api.IdentityProvenanceLegacy,
			},
		},
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: new(0)},
	})
	if err != nil {
		t.Fatalf("resolve legacy identity after removal: %v", err)
	}
	if result.Identity.TMDBID != 0 || result.Identity.IMDBID != 1302 || result.Identity.TVDBID != 1303 || result.Identity.TVmazeID != 1304 {
		t.Fatalf("legacy identity was not re-resolved: %#v", result.Identity)
	}
	if imdbClient.searchCalls == 0 || tvdbClient.calls == 0 || tvmazeClient.calls == 0 {
		t.Fatalf("legacy automatic IDs were not re-searched: imdb=%d tvdb=%d tvmaze=%d", imdbClient.searchCalls, tvdbClient.calls, tvmazeClient.calls)
	}
	if result.Identity.Dependencies.IMDB != (api.IdentityDependency{ID: 1302}) || result.Identity.Dependencies.TVDB.ID != 1303 ||
		result.Identity.Dependencies.TVmaze.ID != 1304 {
		t.Fatalf("re-resolved dependencies = %#v", result.Identity.Dependencies)
	}
}

func TestResolveExternalIdentityTVDBNameFallbackPolicy(t *testing.T) {
	for _, tc := range []struct {
		name           string
		category       string
		identity       api.ExternalIdentity
		overrides      api.ExternalIDOverrides
		wantNameSearch bool
	}{
		{
			name:     "external IDs do not match",
			category: "TV",
			identity: api.ExternalIdentity{
				TMDBID:     1401,
				IMDBID:     1402,
				Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceProvider, IMDB: api.IdentityProvenanceProvider},
			},
			wantNameSearch: true,
		},
		{
name: "no external IDs",
 category: "TV",
 wantNameSearch: true,
},
		{
			name:           "TVDB clear",
			category:       "TV",
			overrides:      api.ExternalIDOverrides{TVDBID: new(0)},
			wantNameSearch: false,
		},
		{
			name:           "positive explicit anchor",
			category:       "TV",
			overrides:      api.ExternalIDOverrides{TMDBID: new(1401)},
			wantNameSearch: false,
		},
		{
name: "movie category",
 category: "MOVIE",
 wantNameSearch: false,
},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
			tvdbClient := &stubTVDB{
				searchID:      1403,
				searchResults: []tvdb.SeriesSearchResult{{TVDBID: 1403, Name: "Example Series"}},
			}
			svc := NewService(&fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(&stubIMDB{}),
				WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
			identity := tc.identity
			identity.SourcePath = sourcePath

			result, err := svc.resolveExternalIdentity(t.Context(), preparationstate.State{
				SourcePath:          sourcePath,
				StoredDataFresh:     true,
				MediaInfoCategory:   tc.category,
				Identity:            identity,
				ExternalIDOverrides: tc.overrides,
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got := len(tvdbClient.searchTitles); (got != 0) != tc.wantNameSearch {
				t.Fatalf("TVDB name searches = %d, want enabled=%t", got, tc.wantNameSearch)
			}
			if tc.wantNameSearch {
				if result.Identity.TVDBID != 1403 || result.Identity.Dependencies.TVDB != (api.IdentityDependency{ID: 1403}) {
					t.Fatalf("TVDB name fallback identity = %#v", result.Identity)
				}
			}
		})
	}
}
