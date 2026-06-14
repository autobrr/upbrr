// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBHandlerSearchBuildsPayloadAndParsesResults(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", req.Method)
			}
			if req.URL.String() != "https://hdbits.org/api/torrents" {
				t.Fatalf("unexpected endpoint %q", req.URL.String())
			}

			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if got := stringFromAny(payload["username"]); got != "user" {
				t.Fatalf("unexpected username %q", got)
			}
			if got := stringFromAny(payload["passkey"]); got != "pk" {
				t.Fatalf("unexpected passkey %q", got)
			}
			if got := intFromAny(payload["category"]); got != 1 {
				t.Fatalf("unexpected category %d", got)
			}
			if got := intFromAny(payload["codec"]); got != 5 {
				t.Fatalf("unexpected codec %d", got)
			}
			if got := intFromAny(payload["medium"]); got != 6 {
				t.Fatalf("unexpected medium %d", got)
			}
			imdb, ok := payload["imdb"].(map[string]any)
			if !ok || stringFromAny(imdb["id"]) != "1234567" {
				t.Fatalf("unexpected imdb payload %#v", payload["imdb"])
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb payload when imdb is present")
			}

			body := `{"status":0,"data":[{"id":42,"name":"Movie.Title.2024.1080p.WEB-DL.DDP5.1.H.265-GRP","filename":"Movie Title (2024).torrent","size":1234567890,"numfiles":3}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := hdbHandler{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		SourcePath: "C:/media/movie",
		ExternalIDs: api.ExternalIDs{
			IMDBID:   1234567,
			TVDBID:   765432,
			Category: "MOVIE",
		},
		VideoCodec: "HEVC",
		Type:       "WEBDL",
	}

	entries, notes, err := handler.Search(context.Background(), meta, "HDB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Name != "Movie.Title.2024.1080p.WEB-DL.DDP5.1.H.265-GRP" {
		t.Fatalf("expected entry name from 'name', got %q", entry.Name)
	}
	if entry.ID != "42" {
		t.Fatalf("unexpected id %q", entry.ID)
	}
	if entry.Link != "https://hdbits.org/details.php?id=42" {
		t.Fatalf("unexpected link %q", entry.Link)
	}
	if entry.Download != "https://hdbits.org/download.php/Movie+Title+%282024%29.torrent?id=42&passkey=pk" {
		t.Fatalf("unexpected download %q", entry.Download)
	}
	if !entry.SizeKnown || entry.SizeBytes != 1234567890 {
		t.Fatalf("unexpected size known=%t size=%d", entry.SizeKnown, entry.SizeBytes)
	}
	if entry.FileCount != 3 {
		t.Fatalf("unexpected file count %d", entry.FileCount)
	}
}

func TestHDBHandlerSearchFallsBackToTextSearchWhenIDsMissing(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if _, hasIMDB := payload["imdb"]; hasIMDB {
				t.Fatalf("did not expect imdb in payload")
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb in payload")
			}
			if got := stringFromAny(payload["search"]); got != "Some.Release.Name.2024.1080p.WEB-DL" {
				t.Fatalf("unexpected fallback search %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := hdbHandler{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		SourcePath:  "C:/media/no-ids",
		ReleaseName: "Some.Release.Name.2024.1080p.WEB-DL",
	}

	entries, notes, err := handler.Search(context.Background(), meta, "HDB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestHDBHandlerSearchUsesTVDBWhenIMDbMissing(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}

			if _, hasIMDB := payload["imdb"]; hasIMDB {
				t.Fatalf("did not expect imdb in payload")
			}
			tvdb, ok := payload["tvdb"].(map[string]any)
			if !ok || intFromAny(tvdb["id"]) != 765432 {
				t.Fatalf("unexpected tvdb payload %#v", payload["tvdb"])
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := hdbHandler{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		SourcePath: "C:/media/show",
		ExternalIDs: api.ExternalIDs{
			TVDBID:   765432,
			Category: "TV",
		},
	}

	entries, notes, err := handler.Search(context.Background(), meta, "HDB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestHDBHandlerSearchSkipsTVDBForMovie(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb in movie payload")
			}
			if got := stringFromAny(payload["search"]); got != "Movie.Release.2024.1080p.WEB-DL" {
				t.Fatalf("unexpected fallback search %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := hdbHandler{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		SourcePath:        "C:/media/movie",
		ReleaseName:       "Movie.Release.2024.1080p.WEB-DL",
		MediaInfoCategory: "TV",
		ExternalIDs: api.ExternalIDs{
			TVDBID:   765432,
			Category: "MOVIE",
		},
	}

	entries, notes, err := handler.Search(context.Background(), meta, "HDB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestFormatHDBIMDbID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id   int
		want string
	}{
		{id: 1, want: "0000001"},
		{id: 1335975, want: "1335975"},
		{id: 12345678, want: "12345678"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := formatHDBIMDbID(tc.id); got != tc.want {
				t.Fatalf("formatHDBIMDbID(%d) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestHDBHandlerSearchIncludesZeroValuedFilters(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}

			category, hasCategory := payload["category"]
			if !hasCategory {
				t.Fatalf("expected category key to be present")
			}
			if got := intFromAny(category); got != 0 {
				t.Fatalf("expected category 0, got %d", got)
			}

			codec, hasCodec := payload["codec"]
			if !hasCodec {
				t.Fatalf("expected codec key to be present")
			}
			if got := intFromAny(codec); got != 0 {
				t.Fatalf("expected codec 0, got %d", got)
			}

			medium, hasMedium := payload["medium"]
			if !hasMedium {
				t.Fatalf("expected medium key to be present")
			}
			if got := intFromAny(medium); got != 0 {
				t.Fatalf("expected medium 0, got %d", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := hdbHandler{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		},
		http:   client,
		logger: api.NopLogger{},
	}

	meta := api.PreparedMetadata{
		SourcePath:  "C:/media/zero-filters",
		ReleaseName: "Unknown.Release.2024",
	}

	entries, notes, err := handler.Search(context.Background(), meta, "HDB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestHDBMediumIDTypePrecedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		meta api.PreparedMetadata
		want int
	}{
		{
			name: "metadata type takes precedence",
			meta: api.PreparedMetadata{
				Type: "WEBDL",
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					Type: stringPtr("WEBRIP"),
				},
				Release: api.ReleaseInfo{Type: "REMUX"},
			},
			want: 6,
		},
		{
			name: "falls back to override type",
			meta: api.PreparedMetadata{
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					Type: stringPtr("WEBRIP"),
				},
				Release: api.ReleaseInfo{Type: "REMUX"},
			},
			want: 3,
		},
		{
			name: "falls back to release type",
			meta: api.PreparedMetadata{
				Release: api.ReleaseInfo{Type: "REMUX"},
			},
			want: 5,
		},
		{
			name: "disc type takes precedence",
			meta: api.PreparedMetadata{
				DiscType: "BDMV",
				Type:     "WEBDL",
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					Type: stringPtr("WEBRIP"),
				},
				Release: api.ReleaseInfo{Type: "REMUX"},
			},
			want: 1,
		},
		{
			name: "hdtv with encode settings maps to encode medium",
			meta: api.PreparedMetadata{
				Type:              "HDTV",
				HasEncodeSettings: true,
			},
			want: 3,
		},
		{
			name: "category type infers encode medium from metadata",
			meta: api.PreparedMetadata{
				Type:       "movie",
				Source:     "BluRay",
				VideoCodec: "AVC",
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			want: 3,
		},
		{
			name: "category type uses source hint for webdl medium",
			meta: api.PreparedMetadata{
				Type:   "movie",
				Source: "Web-DL",
			},
			want: 6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hdbMediumID(tc.meta); got != tc.want {
				t.Fatalf("hdbMediumID() = %d, want %d", got, tc.want)
			}
		})
	}
}

func stringPtr(value string) *string {
	return &value
}
