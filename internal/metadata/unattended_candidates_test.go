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
			client, _ := evidenceHTTPClient(func(request *http.Request) (int, string) {
				if request.Method == http.MethodPost {
					return http.StatusOK, `{"data":{"advancedTitleSearch":{"edges":[
						{"node":{"title":{"id":"tt0000021","titleText":{"text":"Example Title"},"releaseYear":{"year":2026},"titleType":{"text":"Movie"}}}},
						{"node":{"title":{"id":"tt0000022","titleText":{"text":"Example Title"},"releaseYear":{"year":2026},"titleType":{"text":"Movie"}}}}
					]}}}`
				}
				if request.URL.Path == "/3/search/movie" {
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
		})
	}
}
