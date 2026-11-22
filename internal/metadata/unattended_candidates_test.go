// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveExternalIDsAmbiguousCLIModesKeepCandidates(t *testing.T) {
	for _, mode := range []api.InteractionMode{api.InteractionModeUnattended, api.InteractionModeUnattendedConfirm} {
		t.Run(string(mode), func(t *testing.T) {
			imdbSearches, tmdbSearches := 0, 0
			client, _ := evidenceHTTPClient(func(request *http.Request) (int, string) {
				if request.Method == http.MethodPost {
					imdbSearches++
					if imdbSearches > 1 {
						return http.StatusOK, `{"data":{"advancedTitleSearch":{"edges":[{"node":{"title":{"id":"tt0000023","titleText":{"text":"Example Title"},"releaseYear":{"year":2027},"titleType":{"text":"Movie"}}}}]}}}`
					}
					return http.StatusOK, `{"data":{"advancedTitleSearch":{"edges":[
						{"node":{"title":{"id":"tt0000021","titleText":{"text":"Example Title"},"releaseYear":{"year":2026},"titleType":{"text":"Movie"}}}},
						{"node":{"title":{"id":"tt0000022","titleText":{"text":"Example Title"},"releaseYear":{"year":2026},"titleType":{"text":"Movie"}}}}
					]}}}`
				}
				if request.URL.Path == "/3/search/movie" {
					tmdbSearches++
					if tmdbSearches > 1 {
						return http.StatusOK, `{"results":[{"id":13,"title":"Example Title","release_date":"2027-01-01"}]}`
					}
					return http.StatusOK, `{"results":[
						{"id":11,"title":"Example Title","release_date":"2026-01-01"},
						{"id":12,"title":"Example Title","release_date":"2026-01-01"}
					]}`
				}
				return http.StatusOK, `{}`
			})
			service := newTestService(t, &fakeRepo{},
				WithTMDBClient(tmdb.NewClient(client, nil, "synthetic-api-key")),
				WithIMDBClient(imdb.NewClient(client, nil)),
				WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}),
			)
			result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
				SourcePath: filepath.Join(t.TempDir(), "Example.Title.2026.mkv"),
				Policy:     preparationstate.CollectionPolicy{InteractionMode: mode},
				Release: api.ReleaseInfo{
					Title: "Example Title",
					Year:  2026,
					Type:  "movie",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Identity.TMDBID != 0 || result.Identity.IMDBID != 0 || len(result.ExternalIdentityCandidates) != 4 {
				t.Fatalf("ambiguous provider matches were promoted into unattended identity: %+v, candidates=%+v", result.Identity, result.ExternalIdentityCandidates)
			}
			if tmdbSearches != 1 || imdbSearches != 1 {
				t.Fatalf("unresolved candidates did not stop year fallback: tmdb=%d imdb=%d", tmdbSearches, imdbSearches)
			}
		})
	}
}

func TestResolveExternalIDsAmbiguityDoesNotStopSiblingYearFallback(t *testing.T) {
	for _, provider := range []api.IdentityProvider{api.IdentityProviderTMDB, api.IdentityProviderIMDB} {
		t.Run(string(provider), func(t *testing.T) {
			tmdbClient := &stubTMDB{
				searchFn: func(input tmdb.SearchInput) (tmdb.SearchOutcome, error) {
					if input.SearchYear != 2026 {
						return tmdb.SearchOutcome{TMDBID: 13, Category: "MOVIE"}, nil
					}
					if provider == api.IdentityProviderTMDB {
						return tmdb.SearchOutcome{Candidates: []tmdb.Candidate{{TMDBID: 11}, {TMDBID: 12}}}, nil
					}
					return tmdb.SearchOutcome{}, nil
				},
				metadata: tmdb.MetadataResult{Title: "Example Title", TMDBType: "Movie"},
			}
			imdbClient := &stubIMDB{
				searchFn: func(input imdb.SearchInput) (imdb.SearchResult, error) {
					if input.SearchYear != 2026 {
						return imdb.SearchResult{IMDbID: 23}, nil
					}
					if provider == api.IdentityProviderIMDB {
						return imdb.SearchResult{Candidates: []imdb.Candidate{{IMDbID: 21}, {IMDbID: 22}}}, nil
					}
					return imdb.SearchResult{}, nil
				},
				info: imdb.Info{
					IMDbID: "tt0000023",
					Title:  "Example Title",
					Type:   "movie",
				},
			}
			service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient),
				WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
			result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
				SourcePath: filepath.Join(t.TempDir(), "Example.Title.2026.mkv"),
				Release: api.ReleaseInfo{
					Title:    "Example Title",
					Year:     2026,
					Category: "MOVIE",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if provider == api.IdentityProviderTMDB {
				if result.Identity.TMDBID != 0 || result.Identity.IMDBID != 23 || tmdbClient.searchCalls != 1 || imdbClient.searchCalls != 2 {
					t.Fatalf("TMDB ambiguity affected sibling fallback: identity=%+v, searches=%d/%d", result.Identity, tmdbClient.searchCalls, imdbClient.searchCalls)
				}
			} else if result.Identity.TMDBID != 13 || result.Identity.IMDBID != 0 || tmdbClient.searchCalls != 2 || imdbClient.searchCalls != 1 {
				t.Fatalf("IMDb ambiguity affected sibling fallback: identity=%+v, searches=%d/%d", result.Identity, tmdbClient.searchCalls, imdbClient.searchCalls)
			}
			if len(result.ExternalIdentityCandidates) != 2 {
				t.Fatalf("lost unresolved candidates: %+v", result.ExternalIdentityCandidates)
			}
		})
	}
}

func TestResolveExternalIDsCategoryRetryStopsAtAmbiguousIMDbCandidates(t *testing.T) {
	tvSearches := 0
	imdbClient := &stubIMDB{searchFn: func(input imdb.SearchInput) (imdb.SearchResult, error) {
		if input.Category != "tv" {
			return imdb.SearchResult{}, nil
		}
		tvSearches++
		if input.SearchYear == 2026 {
			return imdb.SearchResult{Candidates: []imdb.Candidate{{IMDbID: 21, Type: "TV Series"}, {IMDbID: 22, Type: "TV Series"}}}, nil
		}
		return imdb.SearchResult{IMDbID: 23}, nil
	}}
	service := newTestService(t, &fakeRepo{},
		WithTMDBClient(&stubTMDB{searchOutcome: tmdb.SearchOutcome{TMDBID: 13}, metadata: tmdb.MetadataResult{Title: "Example Series", TMDBType: "TV"}}),
		WithIMDBClient(imdbClient), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
	result, err := service.resolveExternalIdentity(t.Context(), preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.Series.2026.mkv"),
		Release:    api.ReleaseInfo{Title: "Example Series", Year: 2026},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity.Category != api.CanonicalCategoryTV || result.Identity.IMDBID != 0 || tvSearches != 1 || len(result.ExternalIdentityCandidates) != 2 {
		t.Fatalf("category retry bypassed ambiguity: identity=%+v, tv searches=%d, candidates=%+v", result.Identity, tvSearches, result.ExternalIdentityCandidates)
	}
}
