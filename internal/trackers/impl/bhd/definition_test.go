// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"fmt"
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

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "BHD",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			ExternalIDs:       api.ExternalIDs{TMDBID: 123, IMDBID: 456, Category: "TV"},
			ReleaseName:       "Movie.2025.1080p.WEB-DL.DD+5.1.H264-GRP",
			Release:           api.ReleaseInfo{Resolution: "1080p"},
			Type:              "WEBDL",
			Source:            "WEB",
			Audio:             "DD+ 5.1",
			HDR:               "HDR10+ DV",
			Edition:           "Hybrid",
			TVPack:            true,
			SeasonStr:         "S00",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token", DraftDefault: true},
		AppConfig: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"BHD": {DraftDefault: true},
				},
			},
		},
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Payload["category_id"] != "2" {
		t.Fatalf("expected tv category 2, got %q", entry.Payload["category_id"])
	}
	if entry.Payload["type"] != "1080p" {
		t.Fatalf("expected type 1080p, got %q", entry.Payload["type"])
	}
	if entry.Payload["source"] != "WEB" {
		t.Fatalf("expected source WEB, got %q", entry.Payload["source"])
	}
	if entry.Payload["live"] != "0" {
		t.Fatalf("expected live=0 for draft default, got %q", entry.Payload["live"])
	}
	if entry.Payload["pack"] != "1" {
		t.Fatalf("expected pack=1, got %q", entry.Payload["pack"])
	}
	if entry.Payload["special"] != "1" {
		t.Fatalf("expected special=1, got %q", entry.Payload["special"])
	}
	if got := entry.Payload["tags"]; !strings.Contains(got, "WEBDL") || !strings.Contains(got, "HDR10+") || !strings.Contains(got, "DV") {
		t.Fatalf("expected BHD tags in payload, got %q", got)
	}
}

func TestDefinitionBuildUploadDryRunRejectsInvalidContainer(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	_, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "BHD",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Movie.avi"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			ExternalIDs:       api.ExternalIDs{TMDBID: 123, Category: "MOVIE"},
			Type:              "REMUX",
			Container:         "avi",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Logger:        api.NopLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "container") {
		t.Fatalf("expected container validation error, got %v", err)
	}
}

func TestUploadRetriesInvalidIMDb(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/upload/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		calls++
		imdbID := r.FormValue("imdb_id")
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			if imdbID != "456" {
				t.Fatalf("expected first imdb_id 456, got %q", imdbID)
			}
			_, _ = fmt.Fprint(w, `{"status_code":0,"status_message":"Invalid imdb_id"}`)
			return
		}
		if imdbID != "1" {
			t.Fatalf("expected retried imdb_id 1, got %q", imdbID)
		}
		_, _ = fmt.Fprint(w, `{"status_code":1,"status_message":"https://beyond-hd.me/torrent/download/example.7890.torrent"}`)
	}))
	defer server.Close()

	originalBaseURL := bhdBaseURL
	bhdBaseURL = server.URL
	defer func() { bhdBaseURL = originalBaseURL }()

	summary, err := New().Upload(context.Background(), trackers.UploadRequest{
		Tracker: "BHD",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			ExternalIDs:       api.ExternalIDs{TMDBID: 123, IMDBID: 456, Category: "MOVIE"},
			ReleaseName:       "Movie.2025.1080p.BluRay.DD+5.1.H264-GRP",
			Release:           api.ReleaseInfo{Resolution: "1080p"},
			Type:              "REMUX",
			Source:            "BluRay",
			Audio:             "DD+ 5.1",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		AppConfig:     config.Config{},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 upload attempts, got %d", calls)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected one upload, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 || summary.UploadedTorrents[0].TorrentID != "7890" {
		t.Fatalf("unexpected uploaded torrents: %+v", summary.UploadedTorrents)
	}
}

func TestDefinitionBuildDescriptionUsesProvidedAssets(t *testing.T) {
	result, err := New().BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "BHD",
		Meta: api.PreparedMetadata{
			Options: api.UploadOptions{Screens: 4},
		},
		AppConfig: config.Config{},
		Logger:    api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: `[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`,
			Screenshots: []api.ScreenshotImage{{
				RawURL: "https://img.hdbits.org/full.jpg",
				ImgURL: "https://t.hdbits.org/thumb.jpg",
				WebURL: "https://img.hdbits.org/page",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(result.Description, "Created by upbrr") != 1 {
		t.Fatalf("expected single signature, got %q", result.Description)
	}
	if !strings.Contains(result.Description, "[img width=350]https://img.hdbits.org/full.jpg[/img]") {
		t.Fatalf("expected raw image url to be used, got %q", result.Description)
	}
}
