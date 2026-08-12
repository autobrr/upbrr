// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type bhdRoundTripFunc func(*http.Request) (*http.Response, error)

func (f bhdRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func bhdStringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func TestBHDSearchUsesExternalIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		meta         api.DuplicateSubject
		wantTMDBID   string
		wantIMDBID   string
		wantCategory string
	}{
		{
			name: "tv tmdb id",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				Identity:   api.ExternalIdentity{TMDBID: 123, Category: "TV"},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			},
			wantTMDBID:   "tv/123",
			wantCategory: "TV",
		},
		{
			name: "movie tmdb id",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				Identity:   api.ExternalIdentity{TMDBID: 234, Category: "MOVIE"},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			},
			wantTMDBID:   "movie/234",
			wantCategory: "Movies",
		},
		{
			name: "tmdb takes precedence over imdb",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				Identity: api.ExternalIdentity{
					TMDBID:   123,
					IMDBID:   7654321,
					Category: "MOVIE",
				},
				Release: api.ReleaseInfo{Resolution: "2160p"},
			},
			wantTMDBID:   "movie/123",
			wantCategory: "Movies",
		},
		{
			name: "imdb id",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				Identity:   api.ExternalIdentity{IMDBID: 1234567, Category: "MOVIE"},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			},
			wantIMDBID:   "tt1234567",
			wantCategory: "Movies",
		},
		{
			name: "sd keeps broad category and no type filter",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				Identity:   api.ExternalIdentity{TMDBID: 123, Category: "MOVIE"},
				Release:    api.ReleaseInfo{Resolution: "576p"},
			},
			wantTMDBID:   "movie/123",
			wantCategory: "Movies",
		},
		{
			name: "dvd clears type filter",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				DiscType:   "DVD",
				Identity:   api.ExternalIdentity{TMDBID: 123, Category: "MOVIE"},
				Release:    api.ReleaseInfo{Size: "DVD9"},
			},
			wantTMDBID:   "movie/123",
			wantCategory: "Movies",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var payload map[string]any
			client := &http.Client{Transport: bhdRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewBufferString(
						`{"status_code":1,"page":1,"total_pages":1,"total_results":1,"results":[{"name":"Example.Release.2026.1080p-GRP"}]}`,
					)),
					Header: make(http.Header),
				}, nil
			})}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BHD": {APIKey: "placeholder"},
			}}}
			handler := dupe.NewAdapter(New(), "BHD", cfg, client, api.NopLogger{})

			entries, notes, err := adapterEvidence(handler.Search(context.Background(), tc.meta))
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}
			if len(notes) != 0 {
				t.Fatalf("unexpected display notes: %v", notes)
			}
			if len(entries) != 1 {
				t.Fatalf("expected one result, got %#v", entries)
			}
			if got := bhdStringFromAny(payload["tmdb_id"]); got != tc.wantTMDBID {
				t.Fatalf("expected tmdb_id %q, got %q", tc.wantTMDBID, got)
			}
			if got := bhdStringFromAny(payload["imdb_id"]); got != tc.wantIMDBID {
				t.Fatalf("expected imdb_id %q, got %q", tc.wantIMDBID, got)
			}
			if got := bhdStringFromAny(payload["categories"]); got != tc.wantCategory {
				t.Fatalf("expected category %q, got %q", tc.wantCategory, got)
			}
			if value, ok := payload["types"]; !ok || value != nil {
				t.Fatalf("expected policy-safe nil type filter, got %#v", value)
			}
		})
	}
}

func TestBHDSearchContinuesFullPageWhenTotalPagesOmitted(t *testing.T) {
	t.Parallel()

	requestedPages := make([]int, 0, 2)
	client := &http.Client{Transport: bhdRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var request map[string]any
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		page := int(bhdInt(request["page"]))
		requestedPages = append(requestedPages, page)
		count := 100
		if page == 2 {
			count = 1
		}
		results := make([]map[string]any, count)
		for index := range count {
			results[index] = map[string]any{
				"name": fmt.Sprintf("Example.Release.2026.%03d.1080p-GRP", index+(page-1)*100),
			}
		}
		body, err := json.Marshal(map[string]any{
			"status_code": 1,
			"results":     results,
		})
		if err != nil {
			t.Fatalf("encode response: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BHD": {APIKey: "placeholder"},
		}}},
		http:     client,
		baseURL:  "https://example.invalid/",
		maxPages: 2,
	}

	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{TMDBID: 1234567, Category: api.CanonicalCategoryMovie},
	})
	search := result.SearchEvidence()
	if err := result.Cause(); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !slices.Equal(requestedPages, []int{1, 2}) ||
		!search.Complete ||
		search.Pages != 2 ||
		len(search.Warnings) != 0 ||
		len(result.Entries()) != 101 {
		t.Fatalf("requested pages=%v search=%#v entries=%d", requestedPages, search, len(result.Entries()))
	}
}

func TestBHDSearchRejectsPaginationMetadataPresenceChange(t *testing.T) {
	t.Parallel()

	requestedPages := make([]int, 0, 2)
	client := &http.Client{Transport: bhdRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var request map[string]any
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		page := int(bhdInt(request["page"]))
		requestedPages = append(requestedPages, page)
		results := make([]map[string]any, 100)
		for index := range results {
			results[index] = map[string]any{
				"name": fmt.Sprintf("Example.Release.2026.%03d.1080p-GRP", index+(page-1)*100),
			}
		}
		body := map[string]any{"status_code": 1, "results": results}
		if page == 2 {
			body["page"], body["total_pages"], body["total_results"] = 2, 2, 200
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode response: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(encoded)),
			Header:     make(http.Header),
		}, nil
	})}
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BHD": {APIKey: "placeholder"},
		}}},
		http:     client,
		baseURL:  "https://example.invalid/",
		maxPages: 2,
	}

	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{TMDBID: 1234567, Category: api.CanonicalCategoryMovie},
	})
	search := result.SearchEvidence()
	if err := result.Cause(); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !slices.Equal(requestedPages, []int{1, 2}) ||
		search.Complete ||
		search.Pages != 2 ||
		len(search.Warnings) != 1 ||
		len(result.Entries()) != 200 {
		t.Fatalf("requested pages=%v search=%#v entries=%d", requestedPages, search, len(result.Entries()))
	}
}

func TestBHDSearchCountMismatchFailsClosed(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: bhdRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"status_code":1,"page":1,"total_pages":1,"total_results":2,"results":[{"name":"Example.Release.2026.1080p-GRP"}]}`,
			)),
			Header: make(http.Header),
		}, nil
	})}
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BHD": {APIKey: "placeholder"},
		}}},
		http:     client,
		baseURL:  "https://example.invalid/",
		maxPages: 1,
	}
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{TMDBID: 1234567, Category: api.CanonicalCategoryMovie},
	})
	if search := result.SearchEvidence(); search.Complete || search.Pages != 1 || len(search.Warnings) != 1 || len(result.Entries()) != 1 {
		t.Fatalf("count mismatch search=%#v entries=%d", search, len(result.Entries()))
	}
}
