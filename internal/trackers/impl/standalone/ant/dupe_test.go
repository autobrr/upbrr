// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"io"
	"net/http"
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
			"t":    "search",
			"o":    "json",
			"tmdb": "123",
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
	entries, notes, err := adapterEvidence(searcher.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}, Release: api.ReleaseInfo{Resolution: "1080p"}}))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(notes) != 0 || len(entries) != 1 {
		t.Fatalf("entries=%#v notes=%#v", entries, notes)
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
