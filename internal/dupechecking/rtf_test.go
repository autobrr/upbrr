// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRTFHandlerUsesIMDBIDParamAndParsesResults(t *testing.T) {
	t.Parallel()

	sawSearch := false
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/torrent":
				sawSearch = true
				query := req.URL.Query()
				if got := query.Get("includingDead"); got != "1" {
					t.Fatalf("unexpected includingDead value %q", got)
				}
				if got := query.Get("imdbId"); got != "tt0123456" {
					t.Fatalf("unexpected imdbId value %q", got)
				}
				if got := query.Get("imdb"); got != "" {
					t.Fatalf("unexpected imdb query value %q", got)
				}
				body := `[{"id":"42","name":"Movie.1990.1080p.BluRay-GRP","size":123456789,"files":[{"name":"Movie.1990.1080p.BluRay-GRP.mkv"}]}]`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected request path %q", req.URL.Path)
				return nil, nil
			}
		}),
	}

	handler := rtfHandler{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"RTF": {APIKey: "good-key"},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{IMDBID: 123456, Category: "MOVIE"},
		Release:     api.ReleaseInfo{Year: 1990},
	}

	entries, notes, err := handler.Search(context.Background(), meta, "RTF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	if !sawSearch {
		t.Fatalf("expected /api/torrent call")
	}

	entry := entries[0]
	if entry.ID != "42" {
		t.Fatalf("unexpected ID %q", entry.ID)
	}
	if entry.Link != rtfBrowsePrefix+"42" {
		t.Fatalf("unexpected link %q", entry.Link)
	}
	if entry.Download != rtfTorrentEndpoint+"/42/download" {
		t.Fatalf("unexpected download %q", entry.Download)
	}
	if !entry.SizeKnown || entry.SizeBytes != 123456789 {
		t.Fatalf("unexpected size known=%t size=%d", entry.SizeKnown, entry.SizeBytes)
	}
	if entry.FileCount != 1 || len(entry.Files) != 1 {
		t.Fatalf("unexpected files payload count=%d files=%#v", entry.FileCount, entry.Files)
	}
}

func TestRTFHandlerRefreshesAndRetriesOn401(t *testing.T) {
	t.Parallel()

	searchCalls := 0
	loginCalls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/login":
				loginCalls++
				raw, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read login body: %v", err)
				}
				body := string(raw)
				if !strings.Contains(body, `"username":"user"`) || !strings.Contains(body, `"password":"pass"`) {
					t.Fatal("unexpected login body fields")
				}
				return &http.Response{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(strings.NewReader(`{"token":"new-key"}`)),
					Header:     make(http.Header),
				}, nil
			case "/api/torrent":
				searchCalls++
				auth := req.Header.Get("Authorization")
				if searchCalls == 1 {
					if auth != "old-key" {
						t.Fatal("expected first search to use old key")
					}
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Body:       io.NopCloser(strings.NewReader(``)),
						Header:     make(http.Header),
					}, nil
				}
				if auth != "new-key" {
					t.Fatal("expected retry search to use new key")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`[]`)),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected request path %q", req.URL.Path)
				return nil, nil
			}
		}),
	}

	handler := rtfHandler{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"RTF": {APIKey: "old-key", Username: "user", Password: "pass"},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{IMDBID: 123456, Category: "MOVIE"},
		Release:     api.ReleaseInfo{Year: 1990},
	}

	entries, notes, err := handler.Search(context.Background(), meta, "RTF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
	if searchCalls != 2 {
		t.Fatalf("expected 2 search calls, got %d", searchCalls)
	}
	if loginCalls != 1 {
		t.Fatalf("expected 1 login call, got %d", loginCalls)
	}
}

func TestRTFHandlerSkipsTooRecentContent(t *testing.T) {
	t.Parallel()

	called := false
	client := &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			called = true
			t.Fatalf("no request should be sent for too-recent content")
			return nil, nil
		}),
	}

	handler := rtfHandler{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"RTF": {APIKey: "good-key"},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		Release:     api.ReleaseInfo{Title: "Recent Movie"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				Category:    "MOVIE",
				ReleaseDate: time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02"),
			},
		},
	}

	entries, notes, err := handler.Search(context.Background(), meta, "RTF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
	reason, ok := parseSkipReason(notes)
	if !ok {
		t.Fatalf("expected skip reason, got %#v", notes)
	}
	if !strings.Contains(strings.ToLower(reason), "10 years") {
		t.Fatalf("unexpected skip reason %q", reason)
	}
	if called {
		t.Fatalf("request should not have been made")
	}
}
