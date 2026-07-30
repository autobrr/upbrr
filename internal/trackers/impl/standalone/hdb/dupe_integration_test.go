// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
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

type hdbRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hdbRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func hdbTestInt(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func hdbTestString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func TestHDBHandlerSearchBuildsPayloadAndParsesResults(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
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
			if got := hdbTestString(payload["username"]); got != "user" {
				t.Fatalf("unexpected username %q", got)
			}
			if got := hdbTestString(payload["passkey"]); got != "pk" {
				t.Fatalf("unexpected passkey %q", got)
			}
			if got := hdbTestInt(payload["category"]); got != 1 {
				t.Fatalf("unexpected category %d", got)
			}
			if got := hdbTestInt(payload["codec"]); got != 5 {
				t.Fatalf("unexpected codec %d", got)
			}
			if got := hdbTestInt(payload["medium"]); got != 6 {
				t.Fatalf("unexpected medium %d", got)
			}
			imdb, ok := payload["imdb"].(map[string]any)
			if !ok || hdbTestString(imdb["id"]) != "1234567" {
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

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
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
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath: "C:/media/movie",
		Identity: api.ExternalIdentity{
			IMDBID:   1234567,
			TVDBID:   765432,
			Category: "MOVIE",
		},
		VideoCodec: "HEVC",
		Type:       "WEBDL",
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
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
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
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
			if got := hdbTestString(payload["search"]); got != "Some.Release.Name.2024.1080p.WEB-DL" {
				t.Fatalf("unexpected fallback search %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
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
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath:  "C:/media/no-ids",
		ReleaseName: "Some.Release.Name.2024.1080p.WEB-DL",
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
}

func TestHDBHandlerSearchUsesTVDBWhenIMDbMissing(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}

			if _, hasIMDB := payload["imdb"]; hasIMDB {
				t.Fatalf("did not expect imdb in payload")
			}
			tvdb, ok := payload["tvdb"].(map[string]any)
			if !ok || hdbTestInt(tvdb["id"]) != 765432 {
				t.Fatalf("unexpected tvdb payload %#v", payload["tvdb"])
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
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
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath: "C:/media/show",
		Identity: api.ExternalIdentity{
			TVDBID:   765432,
			Category: "TV",
		},
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
}

func TestHDBHandlerSearchSkipsTVDBForMovie(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb in movie payload")
			}
			if got := hdbTestString(payload["search"]); got != "Movie.Release.2024.1080p.WEB-DL" {
				t.Fatalf("unexpected fallback search %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
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
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath:  "C:/media/movie",
		ReleaseName: "Movie.Release.2024.1080p.WEB-DL",
		Identity: api.ExternalIdentity{
			TVDBID:   765432,
			Category: "MOVIE",
		},
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
}

func TestHDBHandlerSearchIncludesZeroValuedFilters(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}

			category, hasCategory := payload["category"]
			if !hasCategory {
				t.Fatalf("expected category key to be present")
			}
			if got := hdbTestInt(category); got != 0 {
				t.Fatalf("expected category 0, got %d", got)
			}

			codec, hasCodec := payload["codec"]
			if !hasCodec {
				t.Fatalf("expected codec key to be present")
			}
			if got := hdbTestInt(codec); got != 0 {
				t.Fatalf("expected codec 0, got %d", got)
			}

			medium, hasMedium := payload["medium"]
			if !hasMedium {
				t.Fatalf("expected medium key to be present")
			}
			if got := hdbTestInt(medium); got != 0 {
				t.Fatalf("expected medium 0, got %d", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
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
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath:  "C:/media/zero-filters",
		ReleaseName: "Unknown.Release.2024",
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
}
