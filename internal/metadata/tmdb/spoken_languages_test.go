// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFetchMetadataNormalizesSpokenLanguageSet(t *testing.T) {
	for _, category := range []string{"MOVIE", "TV"} {
		for _, tc := range []struct {
			name, spoken string
			want         []string
		}{
			{"normalized set", `[{"iso_639_1":" FR "},{"iso_639_1":"fra"},{"iso_639_1":"de"}]`, []string{"de", "fr"}},
			{"missing", "", nil},
			{"empty", `[]`, nil},
			{"unusable", `[{"iso_639_1":"und"},{"iso_639_1":"mul"},{"iso_639_1":"mis"},{"iso_639_1":"zxx"},{"iso_639_1":"not-a-language"}]`, nil},
		} {
			t.Run(category+"/"+tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := `{}`
					if r.URL.Path == "/movie/555001" || r.URL.Path == "/tv/555001" {
						body = `{"title":"Example Movie","name":"Example Series","original_language":"en","release_date":"2026-01-01","first_air_date":"2026-01-01"}`
						if tc.spoken != "" {
							body = strings.TrimSuffix(body, "}") + `,"spoken_languages":` + tc.spoken + "}"
						}
					}
					_, _ = fmt.Fprint(w, body)
				}))
				t.Cleanup(server.Close)
				client := NewClient(server.Client(), nil, "synthetic-key")
				client.baseURL = server.URL
				result, err := client.FetchMetadata(t.Context(), MetadataInput{
					TMDBID:          555001,
					Category:        category,
					SkipAnimeLookup: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(result.SpokenLanguages, tc.want) || result.OriginalLanguage != "en" {
					t.Fatalf("spoken=%v scalar=%q; want set=%v and unchanged en", result.SpokenLanguages, result.OriginalLanguage, tc.want)
				}
			})
		}
	}
}

func TestMediaDetailsRecollectLegacyEvidenceWithoutSpokenLanguages(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `{"title":"Example Movie","original_language":"en","spoken_languages":[{"iso_639_1":"fr"}]}`)
	}))
	t.Cleanup(server.Close)
	repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, filepath.Join(t.TempDir(), "source.mkv"), "fingerprint", api.ExternalFreshnessReuse, api.NopLogger{})
	client := NewClient(server.Client(), nil, "synthetic-key")
	client.baseURL = server.URL
	path := "/movie/555001"
	params := map[string]string{"api_key": client.apiKey}
	legacy := map[string]string{"title": "Example Movie", "original_language": "en"}
	if err := evidence.JSON(ctx, "tmdb.response.v1", []any{client.baseURL, client.apiKey, path, params}, &legacy, errNotFound, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var media mediaResponse
		if err := client.getJSON(ctx, path, params, &media); err != nil {
			t.Fatal(err)
		}
		if requests.Load() != 1 || len(media.SpokenLanguages) != 1 || media.SpokenLanguages[0].Code != "fr" {
			t.Fatalf("legacy decoder evidence lost dialogue or disabled reuse: calls=%d spoken=%v", requests.Load(), media.SpokenLanguages)
		}
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
}
