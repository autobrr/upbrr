// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDSearchUsesResolvedTMDBFallbacks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		meta         api.PreparedMetadata
		wantTMDBID   string
		wantIMDBID   string
		wantCategory string
		wantType     string
		wantNilType  bool
		wantNilCat   bool
	}{
		{
			name: "source current external metadata",
			meta: api.PreparedMetadata{
				SourcePath: "source",
				Release:    api.ReleaseInfo{Resolution: "1080p"},
				ExternalMetadata: api.ExternalMetadata{
					SourcePath: "source",
					TMDB:       &api.TMDBMetadata{TMDBID: 123, Category: "TV"},
				},
			},
			wantTMDBID:   "tv/123",
			wantCategory: "TV",
			wantType:     "1080p",
		},
		{
			name: "current tv external metadata overrides stale movie category",
			meta: api.PreparedMetadata{
				SourcePath:        "source",
				ExternalIDs:       api.ExternalIDs{SourcePath: "other", TMDBID: 999, Category: "MOVIE"},
				MediaInfoCategory: "MOVIE",
				Release:           api.ReleaseInfo{Category: "MOVIE", Resolution: "1080p"},
				ExternalMetadata: api.ExternalMetadata{
					SourcePath: "source",
					TMDB:       &api.TMDBMetadata{TMDBID: 234, Category: "TV"},
				},
			},
			wantTMDBID:   "tv/234",
			wantCategory: "TV",
			wantType:     "1080p",
		},
		{
			name: "current movie external metadata overrides stale tv category",
			meta: api.PreparedMetadata{
				SourcePath:        "source",
				ExternalIDs:       api.ExternalIDs{SourcePath: "other", TMDBID: 999, Category: "TV"},
				MediaInfoCategory: "TV",
				Release:           api.ReleaseInfo{Category: "TV", Resolution: "1080p"},
				ExternalMetadata: api.ExternalMetadata{
					SourcePath: "source",
					TMDB:       &api.TMDBMetadata{TMDBID: 345, Category: "MOVIE"},
				},
			},
			wantTMDBID:   "movie/345",
			wantCategory: "Movies",
			wantType:     "1080p",
		},
		{
			name: "tmdb takes precedence over imdb",
			meta: api.PreparedMetadata{
				SourcePath:  "source",
				ExternalIDs: api.ExternalIDs{TMDBID: 123, IMDBID: 7654321, Category: "MOVIE"},
				Release:     api.ReleaseInfo{Resolution: "2160p"},
			},
			wantTMDBID:   "movie/123",
			wantCategory: "Movies",
			wantType:     "2160p",
		},
		{
			name: "mediainfo id",
			meta: api.PreparedMetadata{
				SourcePath:        "source",
				MediaInfoTMDBID:   234,
				MediaInfoCategory: "TV",
				Release:           api.ReleaseInfo{Resolution: "720p"},
			},
			wantTMDBID:   "tv/234",
			wantCategory: "TV",
			wantType:     "720p",
		},
		{
			name: "arr id",
			meta: api.PreparedMetadata{
				SourcePath: "source",
				ArrTMDBID:  345,
				Release:    api.ReleaseInfo{Resolution: "1080i"},
			},
			wantTMDBID:   "movie/345",
			wantCategory: "Movies",
			wantType:     "1080i",
		},
		{
			name: "imdb fallback",
			meta: api.PreparedMetadata{
				SourcePath:  "source",
				ExternalIDs: api.ExternalIDs{IMDBID: 1234567, Category: "MOVIE"},
				Release:     api.ReleaseInfo{Resolution: "1080p"},
			},
			wantIMDBID:   "tt1234567",
			wantCategory: "Movies",
			wantType:     "1080p",
		},
		{
			name: "mediainfo imdb fallback",
			meta: api.PreparedMetadata{
				SourcePath:        "source",
				MediaInfoIMDBID:   7654321,
				MediaInfoCategory: "TV",
				Release:           api.ReleaseInfo{Resolution: "1080p"},
			},
			wantIMDBID:   "tt7654321",
			wantCategory: "TV",
			wantType:     "1080p",
		},
		{
			name: "sd clears category and type filters",
			meta: api.PreparedMetadata{
				SourcePath:  "source",
				ExternalIDs: api.ExternalIDs{TMDBID: 123, Category: "MOVIE"},
				Release:     api.ReleaseInfo{Resolution: "576p"},
			},
			wantTMDBID:  "movie/123",
			wantNilType: true,
			wantNilCat:  true,
		},
		{
			name: "dvd clears type filter",
			meta: api.PreparedMetadata{
				SourcePath:  "source",
				DiscType:    "DVD",
				ExternalIDs: api.ExternalIDs{TMDBID: 123, Category: "MOVIE"},
				Release:     api.ReleaseInfo{Size: "DVD9"},
			},
			wantTMDBID:   "movie/123",
			wantCategory: "Movies",
			wantNilType:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var payload map[string]any
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
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
						`{"status_code":1,"results":[{"name":"Example.Release.2026.1080p-GRP"}]}`,
					)),
					Header: make(http.Header),
				}, nil
			})}
			handler := bhdHandler{
				cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"BHD": {APIKey: "placeholder"},
				}}},
				http: client,
			}

			entries, notes, err := handler.Search(context.Background(), tc.meta, "BHD")
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}
			if reason, ok := parseSkipReason(notes); ok {
				t.Fatalf("unexpected skip reason: %s", reason)
			}
			if len(entries) != 1 {
				t.Fatalf("expected one result, got %#v", entries)
			}
			if got := stringFromAny(payload["tmdb_id"]); got != tc.wantTMDBID {
				t.Fatalf("expected tmdb_id %q, got %q", tc.wantTMDBID, got)
			}
			if got := stringFromAny(payload["imdb_id"]); got != tc.wantIMDBID {
				t.Fatalf("expected imdb_id %q, got %q", tc.wantIMDBID, got)
			}
			if tc.wantNilCat {
				if value, ok := payload["categories"]; !ok || value != nil {
					t.Fatalf("expected nil category filter, got %#v", value)
				}
			} else if got := stringFromAny(payload["categories"]); got != tc.wantCategory {
				t.Fatalf("expected category %q, got %q", tc.wantCategory, got)
			}
			if tc.wantNilType {
				if value, ok := payload["types"]; !ok || value != nil {
					t.Fatalf("expected nil type filter, got %#v", value)
				}
			} else if got := stringFromAny(payload["types"]); got != tc.wantType {
				t.Fatalf("expected type %q, got %q", tc.wantType, got)
			}
		})
	}
}

func TestResolveSearchTMDBIDIgnoresStaleExternalMetadata(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SourcePath: "source",
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "other",
			TMDB:       &api.TMDBMetadata{TMDBID: 123},
		},
	}

	if got := resolveSearchTMDBID(meta); got != 0 {
		t.Fatalf("expected stale external metadata to be ignored, got %d", got)
	}
}
