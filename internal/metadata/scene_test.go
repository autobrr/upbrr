// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSceneDetectorSRRDB(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := t.TempDir()
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	meta := api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"}
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if !strings.HasPrefix(result.SceneName, "Example.Release") {
		t.Fatalf("unexpected scene name: %q", result.SceneName)
	}
	if result.IMDBID != 1234567 {
		t.Fatalf("unexpected imdb id: %d", result.IMDBID)
	}
}

func TestSceneDetectorSRRDBFetchesIMDbWhenSearchOmitsIt(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","hasNFO":"no"}]}`))
	})
	handler.HandleFunc("/v1/imdb/Example.Release.2024.1080p-WEB", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"releases":[{"imdb":"tt7654321","title":"Example Release"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := t.TempDir()
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	meta := api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"}
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if result.IMDBID != 7654321 {
		t.Fatalf("unexpected imdb id: %d", result.IMDBID)
	}
}

func TestSceneDetectorSRRDBNFOFailureKeepsSceneWithoutNFOPath(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := filepath.Join(t.TempDir(), "nfo-file")
	if err := os.WriteFile(nfoDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write nfo dir placeholder: %v", err)
	}
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if result.NFOPath != "" {
		t.Fatalf("expected unavailable NFO path after save failure, got %q", result.NFOPath)
	}
}

func TestParseNFOExternalIDsText(t *testing.T) {
	ids := parseNFOExternalIDsText(`URL          : https://www.tvmaze.com/shows/54723/from
IMDb         : https://www.imdb.com/title/tt9813792/
TMDB         : https://www.themoviedb.org/tv/124364-from
TVDB         : https://thetvdb.com/series/401003
MAL          : https://myanimelist.net/anime/5114/fullmetal-alchemist-brotherhood`)

	if ids.TVmazeID != 54723 {
		t.Fatalf("expected tvmaze id 54723, got %d", ids.TVmazeID)
	}
	if ids.IMDBID != 9813792 {
		t.Fatalf("expected imdb id 9813792, got %d", ids.IMDBID)
	}
	if ids.TMDBID != 124364 {
		t.Fatalf("expected tmdb id 124364, got %d", ids.TMDBID)
	}
	if ids.TVDBID != 401003 {
		t.Fatalf("expected tvdb id 401003, got %d", ids.TVDBID)
	}
	if ids.MALID != 5114 {
		t.Fatalf("expected mal id 5114, got %d", ids.MALID)
	}
	if ids.Service != "" {
		t.Fatalf("expected no service without source field, got %q", ids.Service)
	}
}

func TestParseNFOExternalIDsTextService(t *testing.T) {
	ids := parseNFOExternalIDsText(`Source       : ITUNES
URL          : https://www.imdb.com/title/tt14850054/`)

	if ids.Service != "iT" {
		t.Fatalf("expected iT service, got %q", ids.Service)
	}
	if ids.ServiceLongName == "" {
		t.Fatalf("expected service long name")
	}
}
