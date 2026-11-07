// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"encoding/json"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestLegacySeriesEvidenceRefreshesRemoteLinksAndReusesThemAfterRestart(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/series/123/extended":
			calls.Add(1)
			_, _ = w.Write([]byte(`{"data":{"id":123,"name":"Provider title","remoteIds":[{"id":"456","sourceName":"TheMovieDB.com","type":12}]}}`))
		case "/series/123/translations/eng":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/search":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dbPath := filepath.Join(t.TempDir(), "evidence.sqlite")
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Example.mkv")
	client := NewClient(server.Client(), nil, "synthetic", "")
	client.baseURL = server.URL
	client.authToken = "synthetic"
	ctx, scope := evidence.WithScope(t.Context(), repo, source, "source", api.ExternalFreshnessReuse, nil)
	legacy := seriesExtendedResponse{Data: seriesExtendedDataResponse{ID: 123, Name: "Retained Series"}}
	if err := evidence.JSON(ctx, "tvdb.response.v1", []any{client.baseURL, client.apiKey, "/series/123/extended", map[string]string{}}, &legacy, errNotFound, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	got, err := client.GetSeriesMetadataWithLanguage(ctx, 123, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Retained Series" || len(got.RemoteLinks.Links) != 1 || got.RemoteLinks.Links[0].ID != 456 || calls.Load() != 1 {
		t.Fatalf("legacy evidence treated as absence or renamed: %+v calls=%d", got, calls.Load())
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	ctx, _ = evidence.WithScope(t.Context(), repo, source, "source", api.ExternalFreshnessReuse, nil)
	client = NewClient(server.Client(), nil, "synthetic", "")
	client.baseURL = server.URL
	client.authToken = "synthetic"
	got, err = client.GetSeriesMetadataWithLanguage(ctx, 123, "")
	if err != nil || len(got.RemoteLinks.Links) != 1 || calls.Load() != 1 {
		t.Fatalf("remote links not reusable after restart: %+v %v calls=%d", got.RemoteLinks, err, calls.Load())
	}
}

func TestSeriesRemoteLinksRetainMissingMalformedUnsupportedAndConflictingEvidence(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		status    api.MetadataEvidenceStatus
		states    []string
	}{
		{"legacy", `{}`, api.MetadataEvidenceStatusPartial, nil},
		{"no links", `{"remoteIds":[]}`, api.MetadataEvidenceStatusComplete, nil},
		{"mixed", `{"remoteIds":[{"sourceName":"IMDB","type":2,"id":"tt0123456"},{"sourceName":"TheMovieDB.com","type":12,"id":"456"},{"sourceName":"IMDB","id":"tt12bad"},{"sourceName":"TheMovieDB.com","type":10,"id":"999"},{"sourceName":"Unknown","id":"123"}]}`, api.MetadataEvidenceStatusComplete, []string{"valid", "valid", "malformed", "unsupported", "unsupported"}},
		{"conflicting", `{"remoteIds":[{"sourceName":"IMDB","id":"tt0123456"},{"sourceName":"IMDB","id":"tt0654321"}]}`, api.MetadataEvidenceStatusContradictory, []string{"conflicting", "conflicting"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var data seriesExtendedDataResponse
			if err := json.Unmarshal([]byte(test.raw), &data); err != nil {
				t.Fatal(err)
			}
			got := parseSeriesRemoteLinks(data.RemoteIDs)
			if got.Status != test.status || len(got.Links) != len(test.states) {
				t.Fatalf("links=%+v", got)
			}
			for i, state := range test.states {
				if got.Links[i].State != state || state != "valid" && got.Links[i].ID != 0 {
					t.Fatalf("unsafe link=%+v", got.Links[i])
				}
			}
		})
	}
}

func TestTVDBCandidateSeriesOrderingAndAverageRuntime(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/search" {
			if r.URL.Query().Get("type") != "series" || r.URL.Query().Get("year") != "2025" || r.URL.Query().Get("query") != "Example" {
				t.Error("series request changed")
			}
			_, _ = w.Write([]byte(`{"data":[{"tvdb_id":"123","name":"Example","year":"2025"},{"tvdb_id":"124","name":"Other"}]}`))
			return
		}
		if r.URL.Path == "/series/124/extended" {
			_, _ = w.Write([]byte(`{"data":{"id":124,"characters":[{"personName":"Unordered","peopleType":"Actor","seriesId":124}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":123,"averageRuntime":42,"characters":[{"personName":"Series Director","peopleType":"Director","seriesId":123},{"personName":"Episode Director","peopleType":"Director","seriesId":123,"episodeId":77},{"personName":"Second","peopleType":"Actor","seriesId":123,"sort":2},{"personName":"Episode actor","peopleType":"Actor","seriesId":123,"episodeId":99,"sort":0},{"personName":"First","peopleType":"Actor","seriesId":123,"sort":1}]}}`))
	}))
	defer server.Close()
	client := NewClient(server.Client(), nil, "synthetic", "")
	client.baseURL = server.URL
	client.authToken = "synthetic"
	got, err := client.SearchCandidates(t.Context(), "Example", 2025, api.CanonicalCategoryTV)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RuntimeMinutes != 42 || got[0].RuntimeKind != "average_episode" || len(got[0].Cast) != 2 || got[0].Cast[0] != "First" || len(got[0].Directors) != 1 || got[0].Directors[0] != "Series Director" || got[1].RuntimeMinutes != 0 || len(got[1].Cast) != 0 {
		t.Fatalf("cards=%+v", got)
	}
	before := calls.Load()
	if got, err := client.SearchCandidates(t.Context(), "Example", 2025, api.CanonicalCategoryMovie); err != nil || len(got) != 0 || calls.Load() != before {
		t.Fatal("movie search contacted TVDB")
	}
}
