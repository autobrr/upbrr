// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTHandlerSendsAPIKeyHeader(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			if got := query.Get("apikey"); got != "" {
				t.Fatal("apikey should not be sent as a query parameter")
			}
			assertQueryParam(t, query, "t", "search")
			assertQueryParam(t, query, "o", "json")
			assertQueryParam(t, query, "tmdb", "123")
			if got := req.Header.Get("X-Api-Key"); got != "token" {
				t.Fatal("unexpected X-API-Key header")
			}
			if got := req.Header.Get("User-Agent"); got == "" {
				t.Fatal("expected User-Agent header")
			}

			body := `{"item":[{"fileName":"Example.1080p-GRP","resolution":"1080p","guid":"https://anthelion.me/torrents.php?id=1","link":"https://anthelion.me/download.php?id=1"}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := antHandler{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"ANT": {APIKey: "token"},
				},
			},
		},
		http: client,
	}

	entries, notes, err := handler.Search(context.Background(), api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{TMDBID: 123},
		Release:     api.ReleaseInfo{Resolution: "1080p"},
	}, "ANT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
}
