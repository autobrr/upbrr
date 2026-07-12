// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type mediaSearchResponse struct {
	status int
	body   string
}

func newMediaSearchServer(t *testing.T, responses map[string]mediaSearchResponse) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, ok := responses[r.URL.Query().Get("term")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if response.status != 0 {
			w.WriteHeader(response.status)
		}
		_, _ = fmt.Fprint(w, response.body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLookupMediaCodeSupportsTVDBOnlyTV(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax/movies/2" || r.URL.Query().Get("term") != "456789" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"77","tvdb":"456789"}]}`)
	}))
	t.Cleanup(server.Close)

	result, err := lookupMediaCode(context.Background(), siteDefinition{Name: "AZ", BaseURL: server.URL}, sessionState{client: server.Client()}, api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 456789},
	})
	if err != nil {
		t.Fatalf("lookup TVDB media: %v", err)
	}
	if result.MediaCode != "77" || result.Missing {
		t.Fatalf("unexpected lookup result: %#v", result)
	}
}

func TestLookupMediaCodeReturnsExactMatchBeforeLaterProviderFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		meta      api.PreparedMetadata
		responses map[string]mediaSearchResponse
		wantCode  string
	}{
		{
			name: "imdb before tmdb failure",
			meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{
				Category: "MOVIE",
				IMDBID:   123,
				TMDBID:   456,
			}},
			responses: map[string]mediaSearchResponse{
				"tt0000123": {body: `{"data":[{"id":"11","imdb":"tt0000123"}]}`},
				"456":       {status: http.StatusBadGateway},
			},
			wantCode: "11",
		},
		{
			name: "tmdb before tvdb failure",
			meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{
				Category: "TV",
				IMDBID:   123,
				TMDBID:   456,
				TVDBID:   789,
			}},
			responses: map[string]mediaSearchResponse{
				"tt0000123": {body: `{"data":[]}`},
				"456":       {body: `{"data":[{"id":"22","tmdb":"456"}]}`},
				"789":       {status: http.StatusBadGateway},
			},
			wantCode: "22",
		},
		{
			name: "tvdb after earlier misses",
			meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{
				Category: "TV",
				IMDBID:   123,
				TMDBID:   456,
				TVDBID:   789,
			}},
			responses: map[string]mediaSearchResponse{
				"tt0000123": {body: `{"data":[]}`},
				"456":       {body: `{"data":[]}`},
				"789":       {body: `{"data":[{"id":"33","tvdb":"789"}]}`},
			},
			wantCode: "33",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := newMediaSearchServer(t, tt.responses)
			result, err := lookupMediaCode(
				context.Background(),
				siteDefinition{Name: "AZ", BaseURL: server.URL},
				sessionState{client: server.Client()},
				tt.meta,
			)
			if err != nil {
				t.Fatal("expected earlier exact provider match to succeed")
			}
			if result.MediaCode != tt.wantCode || result.Missing {
				t.Fatalf("expected media code %q, got %#v", tt.wantCode, result)
			}
		})
	}
}

func TestLookupMediaCodeReturnsProviderErrorWithoutEarlierExactMatch(t *testing.T) {
	t.Parallel()
	server := newMediaSearchServer(t, map[string]mediaSearchResponse{
		"tt0000123": {body: `{"data":[{"id":"11","imdb":"tt9999999"}]}`},
		"456":       {status: http.StatusBadGateway},
	})

	_, err := lookupMediaCode(
		context.Background(),
		siteDefinition{Name: "AZ", BaseURL: server.URL},
		sessionState{client: server.Client()},
		api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 123, TMDBID: 456}},
	)
	if err == nil || !strings.Contains(err.Error(), "media search by tmdb failed") {
		t.Fatal("expected later provider failure when no earlier exact match exists")
	}
}

func TestLookupMediaCodeSelectsExactIDFromTitleResults(t *testing.T) {
	t.Parallel()
	server := newMediaSearchServer(t, map[string]mediaSearchResponse{
		"456":                  {body: `{"data":[]}`},
		"Example Release 2026": {body: `{"data":[{"id":"11","tmdb":"999"},{"id":"22","tmdb":"456"}]}`},
	})

	result, err := lookupMediaCode(
		context.Background(),
		siteDefinition{Name: "AZ", BaseURL: server.URL},
		sessionState{client: server.Client()},
		api.PreparedMetadata{
			ExternalIDs: api.ExternalIDs{Category: "MOVIE", TMDBID: 456},
			Release:     api.ReleaseInfo{Title: "Example Release 2026", Year: 2026},
		},
	)
	if err != nil {
		t.Fatal("expected exact ID in title results to succeed")
	}
	if result.MediaCode != "22" || result.Missing {
		t.Fatalf("expected exact title-result media code, got %#v", result)
	}
}

func TestLookupMediaCodeRejectsUnmatchedTitleResults(t *testing.T) {
	t.Parallel()
	server := newMediaSearchServer(t, map[string]mediaSearchResponse{
		"456": {body: `{"data":[]}`},
		"Example Release 2026": {body: `{"data":[
			{"id":"11","title":"Example Release 2026","year":2026,"tmdb":"999"},
			{"id":"22","title":"Example Release 2026","year":2026,"tmdb":"998"}
		]}`},
	})

	result, err := lookupMediaCode(
		context.Background(),
		siteDefinition{Name: "AZ", BaseURL: server.URL},
		sessionState{client: server.Client()},
		api.PreparedMetadata{
			ExternalIDs: api.ExternalIDs{Category: "MOVIE", TMDBID: 456},
			Release:     api.ReleaseInfo{Title: "Example Release 2026", Year: 2026},
		},
	)
	if err != nil {
		t.Fatal("expected unmatched title results to return a missing result")
	}
	if !result.Missing || result.MediaCode != "" {
		t.Fatalf("expected unmatched title results to remain missing, got %#v", result)
	}
}
