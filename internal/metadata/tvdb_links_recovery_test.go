// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTVDBRejectedTMDBLinkCannotReturnDuringReconciliation(t *testing.T) {
	tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
		TVDBID: 303,
		Name:   "Example Series",
		RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
			Provider: api.IdentityProviderTMDB,
			ID:       101,
			State:    "valid",
		}}},
	}}
	tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{
		Title:          "Other Series",
		TMDBType:       "TV",
		TVDBID:         999,
		ExternalTVDBID: 999,
	}}
	service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(&stubIMDB{}), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
	result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath:        filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv"),
		MediaInfoCategory: "TV",
		ExternalIDOverrides: api.ExternalIDOverrides{
			TVDBID:   new(303),
			IMDBID:   new(0),
			TVmazeID: new(0),
			MALID:    new(0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity.TMDBID != 0 || result.ProviderMetadata.TMDB != nil || tmdbClient.metaCalls != 1 || tmdbClient.findCalls != 0 || result.ProviderMetadata.TVDB.RemoteLinks.Links[0].State != "conflicting" {
		t.Fatalf("rejected sibling returned: %+v tmdb=%d/%d links=%+v", result.Identity, tmdbClient.metaCalls, tmdbClient.findCalls, result.ProviderMetadata.TVDB.RemoteLinks)
	}
}

func TestTVDBMALAgreementDoesNotDependOnResolutionOrder(t *testing.T) {
	for _, late := range []bool{false, true} {
		for _, test := range []struct {
			name          string
			tmdbMAL, want int
			overrides     api.ExternalIDOverrides
			kept          bool
		}{
			{"conflict", 505, 0, api.ExternalIDOverrides{}, false},
			{"agreement", 404, 404, api.ExternalIDOverrides{}, false},
			{"manual TMDB", 505, 505, api.ExternalIDOverrides{TMDBID: new(101)}, false},
			{"manual MAL", 505, 777, api.ExternalIDOverrides{MALID: new(777)}, false},
			{"cleared MAL", 505, 0, api.ExternalIDOverrides{MALID: new(0)}, false},
			{"kept MAL", 505, 777, api.ExternalIDOverrides{KeepMAL: new(true)}, true},
		} {
			t.Run(test.name+map[bool]string{false: "/initial", true: "/late"}[late], func(t *testing.T) {
				source := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
				tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
					TVDBID: 303,
					Name:   "Example Series",
					RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{
						{
							Provider: api.IdentityProviderTMDB,
							ID:       101,
							State:    "valid",
						}, {
							Provider: api.IdentityProviderMAL,
							ID:       404,
							State:    "valid",
						},
					}},
				}}
				if late {
					tvdbClient.remoteLinksErr = errors.New("temporary links failure")
				}
				animeID := test.want
				if animeID == 0 {
					animeID = 404
				}
				tmdbClient := &stubTMDB{
					findResult: tmdb.FindResult{TMDBID: 101, Category: "TV"},
					metadata: tmdb.MetadataResult{
						Title:               "Example Series",
						TMDBType:            "TV",
						TVDBID:              303,
						ExternalTVDBID:      303,
						MALID:               test.tmdbMAL,
						LogoLookupAttempted: true,
					},
					anilistMetadata: tmdb.AniListMetadataResult{MALID: animeID, TitleRomaji: "Example Anime"},
				}
				test.overrides.TVDBID = new(303)
				test.overrides.IMDBID = new(0)
				test.overrides.TVmazeID = new(0)
				input := preparationstate.State{
					SourcePath:          source,
					MediaInfoCategory:   "TV",
					ExternalIDOverrides: test.overrides,
				}
				if test.kept {
					input.StoredDataFresh = true
					input.Identity = api.ExternalIdentity{
						SourcePath:   source,
						TVDBID:       303,
						MALID:        777,
						Category:     api.CanonicalCategoryTV,
						Provenance:   api.IdentityProvenanceSet{MAL: api.IdentityProvenanceProvider},
						Dependencies: api.IdentityDependencySet{MAL: api.IdentityDependency{ID: 777, TVDBID: 303}},
						Resolution:   api.IdentityResolutionKey{ContractVersion: externalidentity.ContractVersion},
					}
					input.ProviderMetadata = api.SourceScopedMetadata{SourcePath: source, AniList: &api.AniListMetadata{MALID: 777, TitleRomaji: "Kept Anime"}}
				}
				service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(&stubIMDB{}), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
				result, err := service.resolveExternalIdentity(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if result.Identity.MALID != test.want || tmdbClient.anilistCalls > 1 {
					t.Fatalf("MAL depends on order or repeated lookup: %+v calls=%d", result.Identity, tmdbClient.anilistCalls)
				}
				if test.want == 0 && result.ProviderMetadata.AniList != nil || test.want > 0 && (result.ProviderMetadata.AniList == nil || result.ProviderMetadata.AniList.MALID != test.want) {
					t.Fatalf("MAL metadata mismatched: %+v", result.ProviderMetadata.AniList)
				}
				if test.kept && tmdbClient.anilistCalls != 0 {
					t.Fatal("kept MAL metadata was fetched again")
				}
			})
		}
	}
}

func TestTVDBUnverifiedIMDbLinkPreservesRawEvidenceAndVerifiedAuthority(t *testing.T) {
	for _, mode := range []string{"provider failure", "missing type", "verified snapshot", "partial verified snapshot", "manual", "kept"} {
		t.Run(mode, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
			tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
				TVDBID: 303,
				Name:   "Example Series",
				RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
					Provider: api.IdentityProviderIMDB,
					ID:       202,
					State:    "valid",
				}}},
			}}
			imdbClient := &stubIMDB{info: imdb.Info{
				IMDbID: "tt0000202",
				Title:  "Example Series",
				Type:   "tvSeries",
			}, infoErr: errors.New("temporary IMDb failure")}
			input := preparationstate.State{
				SourcePath:        source,
				MediaInfoCategory: "TV",
				MediaInfoTVDBID:   303,
				ExternalIDOverrides: api.ExternalIDOverrides{
					TMDBID:   new(0),
					TVmazeID: new(0),
					MALID:    new(0),
				},
			}
			if mode == "missing type" {
				imdbClient.infoErr = nil
				imdbClient.info.Type = ""
			}
			if mode == "manual" {
				input.ExternalIDOverrides.IMDBID = new(202)
				input.ExternalIDOverrides.TVDBID = new(303)
			}
			if mode == "verified snapshot" || mode == "partial verified snapshot" || mode == "kept" {
				input.StoredDataFresh = true
				input.Identity = api.ExternalIdentity{
					SourcePath:   source,
					TVDBID:       303,
					IMDBID:       202,
					Category:     api.CanonicalCategoryTV,
					Provenance:   api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceProvider, TVDB: api.IdentityProvenanceMediaInfo},
					Dependencies: api.IdentityDependencySet{IMDB: api.IdentityDependency{ID: 202, TVDBID: 303}},
					Resolution:   api.IdentityResolutionKey{ContractVersion: externalidentity.ContractVersion},
				}
				input.ProviderMetadata = api.SourceScopedMetadata{SourcePath: source, IMDB: &api.IMDBMetadata{
					IMDBID: 202,
					Title:  "Verified Series",
					Type:   "tvSeries",
				}}
				input.MetadataRequirements = api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{Scope: api.MetadataRequirementScopeTV, AnyOf: []api.MetadataRequirementField{"original_language"}}}}
				if mode == "kept" {
					input.ExternalIDOverrides.KeepIMDB = new(true)
				}
			}
			if mode == "partial verified snapshot" {
				imdbClient.infoErr = nil
				imdbClient.info.Type = ""
			}
			service := newTestService(t, &fakeRepo{}, WithTMDBClient(&stubTMDB{}), WithIMDBClient(imdbClient), WithTVDBClient(tvdbClient), WithTVmazeClient(&stubTVmaze{}))
			result, err := service.resolveExternalIdentity(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			protected := mode == "verified snapshot" || mode == "partial verified snapshot" || mode == "manual" || mode == "kept"
			if protected {
				if result.Identity.IMDBID != 202 {
					t.Fatalf("verified/manual authority lost: %+v", result.Identity)
				}
			} else {
				if result.Identity.IMDBID != 0 || result.ProviderMetadata.IMDB != nil || imdbClient.infoCalls != 1 || result.ProviderMetadata.TVDB.RemoteLinks.Links[0].State != "valid" {
					t.Fatalf("unverified link escaped or raw evidence changed: %+v calls=%d", result.Identity, imdbClient.infoCalls)
				}
			}
			if mode == "verified snapshot" || mode == "partial verified snapshot" || mode == "kept" {
				if result.ProviderMetadata.IMDB == nil || result.ProviderMetadata.IMDB.Type != "tvSeries" || imdbClient.infoCalls == 0 || imdbClient.infoCalls > 2 {
					t.Fatal("transient failure discarded verified metadata or repeated lookup")
				}
			}
		})
	}
}

func TestTVDBEpisodeRejectionPreservesRetainedAuthorityAndDependencies(t *testing.T) {
	ids := api.ExternalIdentity{
		IMDBID:       202,
		TVDBID:       303,
		Provenance:   api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceRetained},
		Dependencies: api.IdentityDependencySet{IMDB: api.IdentityDependency{ID: 202, TVDBID: 303}},
	}
	metadata := api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
		IMDBID: 202,
		Title:  "Kept Episode",
		Type:   "tvEpisode",
	}, TVDB: &api.TVDBMetadata{TVDBID: 303}}
	meta := preparationstate.State{}
	if rejectTVDBEpisodeLink(&meta, &ids, &metadata) != 0 || ids.IMDBID != 202 || metadata.IMDB == nil || ids.Dependencies.IMDB.TVDBID != 303 {
		t.Fatal("kept resource or its dependency was cleared")
	}
	ids = ids.WithProviderRetention(api.ExternalIDOverrides{KeepIMDB: new(false)})
	ids.TVDBID = 404
	if invalidateIdentityDependencies(&ids, false) == 0 || ids.IMDBID != 0 {
		t.Fatal("turning Keep off did not restore dependency invalidation")
	}
}
