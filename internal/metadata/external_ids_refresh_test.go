// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveExternalIDsRefreshesChangedProviderIDs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parentID   string
		parentFail bool
		firstFail  bool
		wantIMDB   int
		wantTMDB   int
		wantCalls  int
	}{
		{
			name:      "resolved parent",
			parentID:  "tt1234567",
			wantIMDB:  1234567,
			wantTMDB:  202,
			wantCalls: 2,
		},
		{
			name:       "parent TMDB unavailable",
			parentID:   "tt1234567",
			parentFail: true,
			wantIMDB:   1234567,
			wantCalls:  2,
		},
		{
			name:      "episode TMDB unavailable but parent succeeds",
			parentID:  "tt1234567",
			firstFail: true,
			wantIMDB:  1234567,
			wantTMDB:  202,
			wantCalls: 2,
		},
		{
			name:      "no parent",
			wantIMDB:  7654321,
			wantTMDB:  101,
			wantCalls: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imdbClient := &stubIMDB{
				infoFn: func(id string) imdb.Info {
					kind := "tvSeries"
					if id == "tt7654321" {
						kind = "tvEpisode"
					}
					return imdb.Info{
						IMDbID: id,
						Title:  "Example Series",
						Type:   kind,
					}
				},
				episodeLookup: imdb.EpisodeLookup{Series: imdb.SeriesInfo{SeriesID: tc.parentID}},
			}
			tmdbClient := &stubTMDB{
				findFn: func(input tmdb.FindInput) (tmdb.FindResult, error) {
					id := 101
					if input.IMDbID == "tt1234567" {
						id = 202
					}
					return tmdb.FindResult{TMDBID: id, Category: "TV"}, nil
				},
				metadataFn: func(input tmdb.MetadataInput) (tmdb.MetadataResult, error) {
					if input.TMDBID == 202 && tc.parentFail || input.TMDBID == 101 && tc.firstFail {
						return tmdb.MetadataResult{}, errors.New("provider unavailable")
					}
					return tmdb.MetadataResult{Title: "Example Series", TMDBType: "TV"}, nil
				},
			}
			service := newTestService(t, &fakeRepo{}, WithIMDBClient(imdbClient), WithTMDBClient(tmdbClient), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
			result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
				SourcePath:        filepath.Join(t.TempDir(), "Example.Series.S01E02.mkv"),
				ExternalFreshness: api.ExternalFreshnessRefresh,
				MediaInfoIMDBID:   7654321,
				MediaInfoCategory: "TV",
				Release:           api.ReleaseInfo{Category: "TV", Title: "Example Series"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Identity.IMDBID != tc.wantIMDB || result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.IMDB.IMDBID != tc.wantIMDB {
				t.Fatalf("IMDb identity=%d metadata=%#v, want %d", result.Identity.IMDBID, result.ProviderMetadata.IMDB, tc.wantIMDB)
			}
			if result.Identity.TMDBID != tc.wantTMDB {
				t.Fatalf("TMDB identity=%d, want %d", result.Identity.TMDBID, tc.wantTMDB)
			}
			if tc.parentFail {
				if result.ProviderMetadata.TMDB != nil || !slices.Contains(result.LookupWarnings, "TMDB refresh failed; dependent metadata is unavailable until it succeeds.") {
					t.Fatalf("failed parent lookup retained metadata=%#v warnings=%v", result.ProviderMetadata.TMDB, result.LookupWarnings)
				}
			} else if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TMDB.TMDBID != tc.wantTMDB {
				t.Fatalf("TMDB metadata=%#v, want %d", result.ProviderMetadata.TMDB, tc.wantTMDB)
			}
			if !tc.parentFail && slices.Contains(result.LookupWarnings, "TMDB refresh failed; dependent metadata is unavailable until it succeeds.") {
				t.Fatalf("successful current provider fetch retained an earlier ID's failure: %v", result.LookupWarnings)
			}
			if imdbClient.infoCalls != tc.wantCalls || tmdbClient.metaCalls != tc.wantCalls || imdbClient.episodeLookupCalls != 1 {
				t.Fatalf("fetch calls: IMDb=%d TMDB=%d parent=%d, want %d per provider and one parent lookup", imdbClient.infoCalls, tmdbClient.metaCalls, imdbClient.episodeLookupCalls, tc.wantCalls)
			}
		})
	}
}

func TestCollectExternalIdentityRefreshesLegacyIMDbEditionEvidence(t *testing.T) {
	for _, wantRows := range []int{1, 0} {
		name := "with runtimes"
		if wantRows == 0 {
			name = "without runtimes"
		}
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Example.Movie.2026.mkv")
			info := imdb.Info{
				IMDbID: "tt1234567",
				Title:  "Example Movie",
				Type:   "movie",
			}
			if wantRows > 0 {
				info.EditionRuntimes = []imdb.EditionDetail{{Seconds: 6001}}
			}
			client := &stubIMDB{info: info}
			service := newTestService(t, &fakeRepo{}, WithIMDBClient(client), WithTMDBClient(&stubTMDB{}))
			state := preparationstate.State{
				SourcePath:        source,
				StoredDataFresh:   true,
				ExternalFreshness: api.ExternalFreshnessLoad,
				Identity: api.ExternalIdentity{
					SourcePath: source,
					IMDBID:     1234567,
					Category:   api.CanonicalCategoryMovie,
					Provenance: api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceExplicit},
					Overrides:  api.IdentityOverrideState{IMDB: api.OverrideStateValue},
					Resolution: api.IdentityResolutionKey{ContractVersion: externalidentity.ContractVersion},
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: source,
					IMDB: &api.IMDBMetadata{
						IMDBID: 1234567,
						Title:  "Example Movie",
						Type:   "movie",
					},
				},
				Release: api.ReleaseInfo{Category: "MOVIE", Title: "Example Movie"},
			}
			for range 2 {
				got, err := service.collectExternalIdentityEvidence(t.Context(), state)
				if err != nil {
					t.Fatal(err)
				}
				if got.Identity.IMDBID != 1234567 || got.Identity.Overrides.IMDB != api.OverrideStateValue || client.infoCalls != 1 {
					t.Fatalf("expected one refresh without changing the pin: id=%d override=%s calls=%d", got.Identity.IMDBID, got.Identity.Overrides.IMDB, client.infoCalls)
				}
				if md := got.ProviderMetadata.IMDB; md == nil || len(md.EditionRuntimes) != wantRows {
					t.Fatalf("expected refreshed IMDb metadata with %d rows", wantRows)
				}
				encoded, err := json.Marshal(got.ProviderMetadata)
				if err != nil {
					t.Fatal(err)
				}
				state.ProviderMetadata = api.SourceScopedMetadata{}
				if err := json.Unmarshal(encoded, &state.ProviderMetadata); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
