// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type rtfRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rtfRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRTFHandlerUsesIMDBIDParamAndParsesResults(t *testing.T) {
	t.Parallel()

	sawSearch := false
	client := &http.Client{
		Transport: rtfRoundTripFunc(func(req *http.Request) (*http.Response, error) {
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
				body, err := os.ReadFile(filepath.Join("testdata", "search_variants.json"))
				if err != nil {
					t.Fatalf("read RTF fixture: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected request path %q", req.URL.Path)
				return nil, nil
			}
		}),
	}

	handler := dupe.NewAdapter(New(), "RTF",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"RTF": {APIKey: "good-key"},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 123456, Category: "MOVIE"},
		Release:  api.ReleaseInfo{Year: 1990},
	}

	result := handler.Search(context.Background(), meta)
	entries, notes, err := adapterEvidence(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 5 {
		t.Fatalf("expected five entries, got %d", len(entries))
	}
	if !sawSearch {
		t.Fatalf("expected /api/torrent call")
	}

	entry := entries[0]
	if entry.ID != "42" {
		t.Fatalf("unexpected ID %q", entry.ID)
	}
	if entry.Link != "https://retroflix.club/browse/t/42" {
		t.Fatalf("unexpected link %q", entry.Link)
	}
	if entry.Download != "https://retroflix.club/api/torrent/42/download" {
		t.Fatalf("unexpected download %q", entry.Download)
	}
	if !entry.SizeKnown || entry.SizeBytes != 123456789 {
		t.Fatalf("unexpected size known=%t size=%d", entry.SizeKnown, entry.SizeBytes)
	}
	if entry.FileCount != 2 || len(entry.Files) != 1 {
		t.Fatalf("unexpected files payload count=%d files=%#v", entry.FileCount, entry.Files)
	}
	if entry.Type != "ENCODE" || entry.Res != "1080p" || entry.Source != "BluRay" || entry.Codec != "x264" || entry.Container != "MKV" {
		t.Fatalf("unexpected structured RTF mapping: %#v", entry)
	}
	search := result.SearchEvidence()
	if !search.Complete || search.Pages != 1 || search.Scope != "work_identity" || len(search.Warnings) != 0 {
		t.Fatalf("unexpected RTF search evidence: %#v", search)
	}
}

func TestRTFTitleFallbackRemainsExplicitlyIncomplete(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: rtfRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("search") != "Example Release" {
			t.Fatalf("unexpected title query: %q", req.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`[]`)),
			Header:     make(http.Header),
		}, nil
	})}
	handler := dupe.NewAdapter(New(), "RTF", config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"RTF": {APIKey: "good-key"}},
	}}, client, api.NopLogger{})
	result := handler.Search(context.Background(), api.DuplicateSubject{
		Release: api.ReleaseInfo{Title: "Example Release", Year: 1990},
	})
	search := result.SearchEvidence()
	if search.Complete || search.Pages != 1 || search.Scope != "title_year" ||
		len(search.Warnings) != 1 || search.Warnings[0] != "RTF title search completeness is not evidenced" {
		t.Fatalf("RTF title search evidence = %#v", search)
	}
}

func TestRTFMalformedTorrentPayloadFails(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: rtfRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`[{"name":"Example.Release.2026.1080p-GRP"}]`)),
			Header:     make(http.Header),
		}, nil
	})}
	handler := dupe.NewAdapter(New(), "RTF", config.Config{Trackers: config.TrackersConfig{
		Trackers: map[string]config.TrackerConfig{"RTF": {APIKey: "good-key"}},
	}}, client, api.NopLogger{})
	result := handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 1234567},
		Release:  api.ReleaseInfo{Year: 1990},
	})
	if result.Disposition() != dupe.DispositionFailed || result.Code() != dupe.FailureResponseParse {
		t.Fatalf("malformed RTF torrent disposition=%v code=%q", result.Disposition(), result.Code())
	}
}

func TestRTFHandlerRefreshesAndRetriesOn401(t *testing.T) {
	t.Parallel()

	searchCalls := 0
	loginCalls := 0
	client := &http.Client{
		Transport: rtfRoundTripFunc(func(req *http.Request) (*http.Response, error) {
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

	handler := dupe.NewAdapter(New(), "RTF",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"RTF": {
						APIKey:   "old-key",
						Username: "user",
						Password: "pass",
					},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 123456, Category: "MOVIE"},
		Release:  api.ReleaseInfo{Year: 1990},
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
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
		Transport: rtfRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			called = true
			t.Fatalf("no request should be sent for too-recent content")
			return nil, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "RTF",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"RTF": {APIKey: "good-key"},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		Release:  api.ReleaseInfo{Title: "Recent Movie"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Category:    "MOVIE",
				ReleaseDate: time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02"),
			},
		},
	}

	result := handler.Search(context.Background(), meta)
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunUnsupportedContent ||
		!strings.Contains(strings.ToLower(result.SafeMessage()), "10 years and 1 month") {
		t.Fatalf("unexpected result disposition=%v code=%q message=%q", result.Disposition(), result.Code(), result.SafeMessage())
	}
	if called {
		t.Fatalf("request should not have been made")
	}
}
