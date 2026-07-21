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
		wantType     string
		wantNilType  bool
		wantNilCat   bool
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
			wantType:     "1080p",
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
			wantType:     "1080p",
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
			wantType:     "2160p",
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
			wantType:     "1080p",
		},
		{
			name: "sd clears category and type filters",
			meta: api.DuplicateSubject{
				SourcePath: "source",
				Identity:   api.ExternalIdentity{TMDBID: 123, Category: "MOVIE"},
				Release:    api.ReleaseInfo{Resolution: "576p"},
			},
			wantTMDBID:  "movie/123",
			wantNilType: true,
			wantNilCat:  true,
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
			wantNilType:  true,
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
						`{"status_code":1,"results":[{"name":"Example.Release.2026.1080p-GRP"}]}`,
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
			if tc.wantNilCat {
				if value, ok := payload["categories"]; !ok || value != nil {
					t.Fatalf("expected nil category filter, got %#v", value)
				}
			} else if got := bhdStringFromAny(payload["categories"]); got != tc.wantCategory {
				t.Fatalf("expected category %q, got %q", tc.wantCategory, got)
			}
			if tc.wantNilType {
				if value, ok := payload["types"]; !ok || value != nil {
					t.Fatalf("expected nil type filter, got %#v", value)
				}
			} else if got := bhdStringFromAny(payload["types"]); got != tc.wantType {
				t.Fatalf("expected type %q, got %q", tc.wantType, got)
			}
		})
	}
}
