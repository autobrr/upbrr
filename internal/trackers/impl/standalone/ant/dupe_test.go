// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestDupeSearcherSendsAPIKeyHeader(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		if query.Get("apikey") != "" {
			t.Fatal("apikey should not be sent as a query parameter")
		}
		for key, want := range map[string]string{
			"t":     "search",
			"o":     "json",
			"imdb":  "0000456",
			"limit": "100",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		if got := req.Header.Get("X-Api-Key"); got != "token" {
			t.Fatal("unexpected X-API-Key header")
		}
		if req.Header.Get("User-Agent") == "" {
			t.Fatal("expected User-Agent header")
		}
		body := `{"item":[{"fileName":"Example.Release.2026.1080p-GRP","resolution":"1080p","guid":"https://example.invalid/torrents.php?id=1","link":"https://example.invalid/download.php?id=1"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "token"}}}}
	searcher := dupe.NewAdapter(New(), "ANT", cfg, client, api.NopLogger{})
	entries, notes, err := adapterEvidence(searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 456},
		Release:  api.ReleaseInfo{Resolution: "1080p"},
	}))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(notes) != 0 || len(entries) != 1 {
		t.Fatalf("entries=%#v notes=%#v", entries, notes)
	}
}

func TestDupeSearcherConsumesANTOffsetPages(t *testing.T) {
	for _, tc := range []struct {
		name              string
		includePagination bool
	}{
		{name: "advertised total", includePagination: true},
		{name: "page capacity fallback", includePagination: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var offsets []string
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				offsets = append(offsets, req.URL.Query().Get("offset"))
				var body string
				switch len(offsets) {
				case 1:
					body = antSearchPageJSON(t, 0, 101, 100, tc.includePagination)
				case 2:
					body = antSearchPageJSON(t, 100, 101, 1, tc.includePagination)
				default:
					t.Fatalf("unexpected request count: %d", len(offsets))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			})}

			cfg := antDupeTestConfig()
			searcher := dupe.NewAdapter(New(), "ANT", cfg, client, api.NopLogger{})
			result := searcher.Search(
				context.Background(),
				api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
			)
			if result.Cause() != nil {
				t.Fatalf("search: %v", result.Cause())
			}
			evidence := result.SearchEvidence()
			if !evidence.Complete || evidence.Pages != 2 {
				t.Fatalf("search evidence = %#v", evidence)
			}
			entries := result.Entries()
			if len(entries) != 101 || entries[100].Name != "Example.Release.100" {
				t.Fatalf("paginated entries = %d last=%#v", len(entries), entries[len(entries)-1])
			}
			if len(offsets) != 2 || offsets[0] != "" || offsets[1] != "100" {
				t.Fatalf("requested offsets = %#v", offsets)
			}
		})
	}
}

func TestDupeSearcherANTPaginationBoundFailsClosed(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(antSearchPageJSON(t, 0, 101, 100, true))),
			Header:     make(http.Header),
		}, nil
	})}
	searcher := &dupeSearcher{
		cfg:      antDupeTestConfig(),
		http:     client,
		endpoint: "https://example.invalid/api.php",
		maxPages: 1,
	}
	result := searcher.Search(
		context.Background(),
		api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
	)
	evidence := result.SearchEvidence()
	if evidence.Complete || evidence.Pages != 1 || len(evidence.Warnings) != 1 {
		t.Fatalf("bounded search evidence = %#v", evidence)
	}
	if len(result.Entries()) != 100 || len(result.Notes()) != 1 {
		t.Fatalf("bounded result entries=%d notes=%#v", len(result.Entries()), result.Notes())
	}
}

func TestDupeSearcherMissingCredentialsSkips(t *testing.T) {
	t.Parallel()
	searcher := dupe.NewAdapter(New(), "ANT", config.Config{}, http.DefaultClient, api.NopLogger{})
	result := searcher.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 1}})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingCredentials || result.SafeMessage() == "" {
		t.Fatalf("unexpected result disposition=%v code=%q message=%q", result.Disposition(), result.Code(), result.SafeMessage())
	}
}

func antSearchPageJSON(t *testing.T, offset int, total int, count int, includePagination bool) string {
	t.Helper()
	items := make([]map[string]any, count)
	for index := range count {
		items[index] = map[string]any{
			"fileName":   "Example.Release." + strconv.Itoa(offset+index),
			"resolution": "2160p",
		}
	}
	payload := map[string]any{
		"item": items,
	}
	if includePagination {
		payload["response"] = map[string]any{
			"offset": offset,
			"total":  total,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal ANT search page: %v", err)
	}
	return string(encoded)
}

func antDupeTestConfig() config.Config {
	return config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"ANT": {APIKey: "token"},
	}}}
}
