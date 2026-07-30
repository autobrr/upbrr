// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

func TestAZNetworkHandlerSearchParsesHTMLResults(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ajax/movies/1":
			_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123"}]}`)
		case "/movies/torrents/77":
			_, _ = io.WriteString(w, `<table class="table-bordered"><tbody><tr><td><a class="torrent-filename" href="/torrent/123">Movie.2024.1080p.WEB-DL.x265-GRP</a><span class="badge-extra">WEB-DL</span></td></tr></tbody></table>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	parsed, _ := url.Parse(server.URL)
	cookieText := "# Netscape HTTP Cookie File\n" + parsed.Hostname() + "\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, "AZ.txt"), []byte(cookieText), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	handler := dupe.NewAdapter(testDefinitionAt(server.URL), "AZ",
		config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: dbPath},
			Trackers:     config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"AZ": {}}},
		}, server.Client(), api.NopLogger{})
	entries, notes, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
		Release:  api.ReleaseInfo{Title: "Movie", Resolution: "1080p"},
		Type:     "WEBDL",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != "123" {
		t.Fatalf("expected torrent id 123, got %q", entries[0].ID)
	}
}
