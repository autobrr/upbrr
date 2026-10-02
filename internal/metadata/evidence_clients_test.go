// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func openProviderEvidenceStore(t *testing.T) (*db.SQLiteRepository, string) {
	t.Helper()
	repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "evidence.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return repo, filepath.Join(t.TempDir(), "Example.Release.mkv")
}

func evidenceHTTPClient(respond func(*http.Request) (int, string)) (*http.Client, func(string) int) {
	var mu sync.Mutex
	calls := make(map[string]int)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[request.URL.Path]++
		calls[""]++
		mu.Unlock()
		status, body := respond(request)
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	return client, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[path]
	}
}

func TestTMDBEvidenceReappliesLocalCorrectionsWithoutRequests(t *testing.T) {
	t.Parallel()
	repo, source := openProviderEvidenceStore(t)
	httpClient, calls := evidenceHTTPClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/3/movie/11":
			return http.StatusOK, `{"id":11,"title":"Example Movie","original_title":"Original Movie","release_date":"2026-01-01","original_language":"en","production_companies":[{"id":1,"name":"Example Studio"}]}`
		case "/3/movie/11/external_ids":
			return http.StatusOK, `{"imdb_id":"tt0000022","tvdb_id":33}`
		case "/3/movie/11/translations":
			return http.StatusOK, `{"translations":[{"iso_639_1":"de","data":{"title":"Example Localized"}}]}`
		case "/3/movie/11/videos":
			return http.StatusOK, `{"results":[{"site":"YouTube","type":"Trailer","key":"example"}]}`
		case "/3/movie/11/keywords":
			return http.StatusOK, `{"keywords":[{"name":"example"}]}`
		case "/3/movie/11/credits":
			return http.StatusOK, `{"cast":[{"name":"Example Actor","known_for_department":"Acting"}],"crew":[{"name":"Example Director","job":"Director"}]}`
		default:
			t.Errorf("unexpected provider path: %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})
	client := tmdb.NewClient(httpClient, nil, "synthetic-api-key")
	ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	input := tmdb.MetadataInput{TMDBID: 11, Category: "MOVIE"}
	first, err := client.FetchMetadata(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	initialCalls := calls("")
	if initialCalls != 6 {
		t.Fatalf("initial requests = %d, want 6 primitive facts", initialCalls)
	}
	if len(first.Cast) != 1 || len(first.ProductionCompanies) != 1 {
		t.Fatalf("incomplete provider fixture: %#v", first)
	}
	first.Cast[0] = "User correction"
	first.ProductionCompanies[0].Name = "User correction"
	first.LocalizedTitles["de"] = "User correction"
	input.IMDbID = 22
	input.TVDBID = 33
	input.Poster = "https://images.example.invalid/corrected.jpg"
	input.OriginalLanguage = "fr"
	input.AKA = "User alternative"
	for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
		ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", freshness, nil)
		got, err := client.FetchMetadata(ctx, input)
		if err != nil || scope.Err() != nil {
			t.Fatalf("correction replay: %v; scope: %v", err, scope.Err())
		}
		if got.Poster != input.Poster || got.OriginalLanguage != input.OriginalLanguage || got.IMDbID != 22 || got.TVDBID != 33 {
			t.Fatalf("local correction or hydrated IDs lost: %#v", got)
		}
		if len(got.Cast) != 1 || len(got.ProductionCompanies) != 1 || got.Cast[0] != "Example Actor" || got.ProductionCompanies[0].Name != "Example Studio" || got.LocalizedTitles["de"] != "Example Localized" {
			t.Fatalf("provider evidence contaminated by prior result mutation: %#v", got)
		}
		if got := calls(""); got != initialCalls {
			t.Fatalf("freshness %q refetched after local edits: requests=%d, want %d", freshness, got, initialCalls)
		}
	}
}

func TestTMDBEvidenceScopesActualQueryDependencies(t *testing.T) {
	t.Parallel()
	repo, source := openProviderEvidenceStore(t)
	httpClient, calls := evidenceHTTPClient(func(request *http.Request) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"title":%q,"language":%q}`, request.URL.Path, request.URL.Query().Get("language"))
	})
	client := tmdb.NewClient(httpClient, nil, "synthetic-api-key")
	ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	inputs := []tmdb.LocalizedDataInput{
		{
			DataType: "main",
			Category: "MOVIE",
			TMDBID:   11,
			Language: "de",
		},
		{
			DataType: "main",
			Category: "TV",
			TMDBID:   11,
			Language: "de",
		},
		{
			DataType: "main",
			Category: "TV",
			TMDBID:   12,
			Language: "de",
		},
		{
			DataType: "episode",
			TMDBID:   11,
			Season:   1,
			Episode:  1,
			Language: "de",
		},
		{
			DataType: "episode",
			TMDBID:   11,
			Season:   1,
			Episode:  2,
			Language: "de",
		},
		{
			DataType: "episode",
			TMDBID:   11,
			Season:   2,
			Episode:  1,
			Language: "de",
		},
		{
			DataType: "episode",
			TMDBID:   11,
			Season:   1,
			Episode:  1,
			Language: "fr",
		},
	}
	original := make([]map[string]any, len(inputs))
	for i, input := range inputs {
		got, err := client.GetLocalizedData(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		original[i] = got
	}
	if got := calls(""); got != len(inputs) {
		t.Fatalf("independent queries = %d, want %d", got, len(inputs))
	}
	ctx, _ = evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
	for i, input := range inputs {
		got, err := client.GetLocalizedData(ctx, input)
		if err != nil || !reflect.DeepEqual(got, original[i]) {
			t.Fatalf("query %d replay = %v, %v; want %v", i, got, err, original[i])
		}
	}
	if got := calls(""); got != len(inputs) {
		t.Fatalf("replay repeated requests: %d", got)
	}
	for _, change := range []struct {
		fingerprint string
		apiKey      string
	}{
		{fingerprint: "changed-source", apiKey: "synthetic-api-key"},
		{fingerprint: "fingerprint", apiKey: "changed-synthetic-key"},
	} {
		ctx, _ := evidence.WithScope(t.Context(), repo, source, change.fingerprint, api.ExternalFreshnessReuse, nil)
		if _, err := tmdb.NewClient(httpClient, nil, change.apiKey).GetLocalizedData(ctx, inputs[0]); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls(""); got != len(inputs)+2 {
		t.Fatalf("source/configuration change reused stale facts: requests=%d", got)
	}
}

func TestTMDBEvidenceRetainsSwallowedFailuresAndRetriesEmpty(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			repo, source := openProviderEvidenceStore(t)
			httpClient, calls := evidenceHTTPClient(func(_ *http.Request) (int, string) {
				return status, `{"page":1,"results":[],"total_results":0,"total_pages":1}`
			})
			client := tmdb.NewClient(httpClient, nil, "synthetic-api-key")
			input := tmdb.SearchInput{
				Filename:   "Example",
				Category:   "MOVIE",
				DontSwitch: true,
			}
			passes := []struct {
				freshness api.ExternalFreshness
				wantCalls int
			}{
				{freshness: api.ExternalFreshnessLoad, wantCalls: 1},
				{freshness: api.ExternalFreshnessReuse, wantCalls: 1},
				{freshness: api.ExternalFreshnessLoad, wantCalls: 2},
				{freshness: api.ExternalFreshnessRefresh, wantCalls: 3},
			}
			for _, pass := range passes {
				ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", pass.freshness, nil)
				for range 2 {
					got, err := client.SearchID(ctx, input)
					if err != nil || got.TMDBID != 0 || len(got.Candidates) != 0 || scope.Err() != nil {
						t.Fatalf("search fallback = %#v, %v; scope=%v", got, err, scope.Err())
					}
				}
				want := pass.wantCalls
				if status == http.StatusInternalServerError {
					want = 1
				}
				if got := calls(""); got != want {
					t.Fatalf("status=%d freshness=%q requests=%d, want %d", status, pass.freshness, got, want)
				}
			}
			if err := repo.PurgeContentData(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			before := calls("")
			ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
			if _, err := client.SearchID(ctx, input); err != nil || calls("") != before+1 {
				t.Fatalf("history purge did not permit retry: error=%v, requests=%d", err, calls(""))
			}
		})
	}
}

func TestTMDBEvidenceRetainsEpisodeWithEmptyOptionalCollections(t *testing.T) {
	t.Parallel()
	repo, source := openProviderEvidenceStore(t)
	httpClient, calls := evidenceHTTPClient(func(_ *http.Request) (int, string) {
		return http.StatusOK, `{"id":11,"name":"Example Episode","season_number":1,"episode_number":2,"crew":[],"guest_stars":[]}`
	})
	client := tmdb.NewClient(httpClient, nil, "synthetic-api-key")
	for range 2 {
		ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
		got, err := client.GetEpisodeDetails(ctx, 11, 1, 2)
		if err != nil || got.Name != "Example Episode" || calls("") != 1 {
			t.Fatalf("successful episode replay = %#v, %v; requests=%d", got, err, calls(""))
		}
	}
}

func TestTVmazeEvidencePreservesNotFoundDateFallback(t *testing.T) {
	t.Parallel()
	repo, source := openProviderEvidenceStore(t)
	httpClient, calls := evidenceHTTPClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/shows/11/episodebynumber":
			return http.StatusNotFound, `{}`
		case "/shows/11/episodesbydate":
			return http.StatusOK, fmt.Sprintf(`[{"id":11,"name":"Example Episode","season":1,"number":2,"airdate":%q}]`, request.URL.Query().Get("date"))
		default:
			t.Errorf("unexpected provider path: %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})
	client := tvmaze.NewClient(httpClient, nil)
	for i, date := range []string{"2026-01-01", "2026-01-02", "2026-01-02"} {
		ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
		got, err := client.GetEpisodeByNumber(ctx, 11, 1, 2, tvmaze.EpisodeLookupContext{ManualDate: date})
		if err != nil || got == nil || got.AirDate != date {
			t.Fatalf("date correction %d = %#v, %v", i, got, err)
		}
	}
	if number, date := calls("/shows/11/episodebynumber"), calls("/shows/11/episodesbydate"); number != 1 || date != 2 {
		t.Fatalf("number/date query requests = %d/%d, want 1/2", number, date)
	}
}

func TestProviderEvidenceSurvivesRepositoryReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "evidence.sqlite")
	source := filepath.Join(dir, "Example.Release.mkv")
	httpClient, calls := evidenceHTTPClient(func(_ *http.Request) (int, string) {
		return http.StatusOK, `{"name":"Example Episode","season_number":1,"episode_number":2}`
	})
	for pass := range 2 {
		repo, err := db.OpenContext(t.Context(), databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.MigrateContext(t.Context()); err != nil {
			_ = repo.Close()
			t.Fatal(err)
		}
		ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, nil)
		client := tmdb.NewClient(httpClient, nil, "synthetic-api-key")
		got, err := client.GetEpisodeDetails(ctx, 11, 1, 2)
		closeErr := repo.Close()
		if err != nil || scope.Err() != nil || closeErr != nil || got.Name != "Example Episode" || calls("") != 1 {
			t.Fatalf("pass %d: result=%#v error=%v scope=%v close=%v requests=%d", pass, got, err, scope.Err(), closeErr, calls(""))
		}
	}
}

var _ api.MetadataEvidenceRepository = (*db.SQLiteRepository)(nil)
