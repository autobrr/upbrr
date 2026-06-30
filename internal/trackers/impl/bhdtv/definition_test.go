// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionBuildUploadDryRunBuildsPayload(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	torrentPath := filepath.Join(tempDir, "input.torrent")
	mediaPath := filepath.Join(tempDir, "MEDIAINFO_CLEANPATH.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaPath, []byte("mediainfo text"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	def := New()
	entry, err := def.BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "BHDTV",
		Meta: api.PreparedMetadata{
			SourcePath:                  filepath.Join(tempDir, "Show.S01E01.mkv"),
			TorrentPath:                 torrentPath,
			MediaInfoTextPath:           mediaPath,
			Type:                        "WEBDL",
			MediaInfoCategory:           "TV",
			TVPack:                      false,
			ReleaseName:                 "Show: S01E01 DD+ 1080p WEB-DL H.265",
			ReleaseNameNoTag:            "Show: S01E01 DD+ 1080p WEB-DL H.265",
			ExternalIDs:                 api.ExternalIDs{Category: "TV", TVmazeID: 321},
			ExternalMetadata:            api.ExternalMetadata{IMDB: &api.IMDBMetadata{IMDbURL: "https://www.imdb.com/title/tt1234567/"}},
			TrackerQuestionnaireAnswers: map[string]map[string]string{},
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}

	if entry.Tracker != "BHDTV" {
		t.Fatalf("unexpected tracker %q", entry.Tracker)
	}
	if entry.Payload["cat"] != "10" {
		t.Fatalf("expected tv episode cat 10, got %q", entry.Payload["cat"])
	}
	if entry.Payload["subcat"] != "8" {
		t.Fatalf("expected tv web subcat 8, got %q", entry.Payload["subcat"])
	}
	if entry.Payload["resolution"] != "3" {
		t.Fatalf("expected 1080p resolution 3, got %q", entry.Payload["resolution"])
	}
	if entry.Payload["url"] != "https://www.tvmaze.com/shows/321" {
		t.Fatalf("unexpected url %q", entry.Payload["url"])
	}
	if entry.Payload["name"] != "Show.S01E01.DDP.1080p.WEB-DL.H.265" {
		t.Fatalf("unexpected name %q", entry.Payload["name"])
	}
}

func TestUploadParsesViewAndWritesArtifact(t *testing.T) {
	tempDir := t.TempDir()
	torrentPath := filepath.Join(tempDir, "input.torrent")
	mediaPath := filepath.Join(tempDir, "MEDIAINFO_CLEANPATH.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaPath, []byte("mediainfo text"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		if got := r.FormValue("api_key"); got != "token" {
			t.Error("unexpected api key")
			return
		}
		if got := r.FormValue("cat"); got != "7" {
			t.Errorf("unexpected cat %q", got)
			return
		}
		if got := r.FormValue("subcat"); got != "48" {
			t.Errorf("unexpected subcat %q", got)
			return
		}
		if got := r.FormValue("resolution"); got != "4" {
			t.Errorf("unexpected resolution %q", got)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"view":"https://www.bit-hdtv.com/details.php?id=99"}}`))
	}))
	defer server.Close()

	originalURL := uploadURL
	uploadURL = server.URL
	defer func() { uploadURL = originalURL }()

	summary, err := upload(context.Background(), trackers.UploadRequest{
		Tracker: "BHDTV",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tempDir, "Movie.2160p.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaPath,
			Type:              "REMUX",
			ReleaseName:       "Movie 2160p REMUX x265",
			ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
		},
		TrackerConfig: config.TrackerConfig{
			APIKey: "token",
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "ua.db")},
		},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("unexpected summary %#v", summary)
	}
	if got := summary.UploadedTorrents[0].TorrentURL; got != "https://www.bit-hdtv.com/details.php?id=99" {
		t.Fatalf("unexpected torrent url %q", got)
	}
	if strings.TrimSpace(summary.UploadedTorrents[0].TorrentPath) != "" {
		t.Fatal("expected no personalized torrent path without my_announce_url")
	}
}
