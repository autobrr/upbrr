// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"io"
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

func TestDefinitionBuildDescriptionUsesHDBGroup(t *testing.T) {
	d := New()
	result, err := d.BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "HDB",
		Meta:    api.PreparedMetadata{},
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Group != "hdb" {
		t.Fatalf("expected hdb group, got %q", result.Group)
	}
}

func TestDefinitionBuildDescriptionUsesProvidedAssets(t *testing.T) {
	d := New()
	result, err := d.BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "HDB",
		Meta:    api.PreparedMetadata{},
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://t.hdbits.org/example.jpg",
				WebURL: "https://img.hdbits.org/example",
				RawURL: "https://img.hdbits.org/example",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Description, "https://t.hdbits.org/example.jpg") {
		t.Fatalf("expected provided screenshot in description, got %q", result.Description)
	}
}

func TestDefinitionUploadMissingCredentials(t *testing.T) {
	d := New()
	_, err := d.Upload(context.Background(), trackers.UploadRequest{Tracker: "HDB", Logger: api.NopLogger{}})
	if err == nil {
		t.Fatal("expected missing credentials error")
	}
}

func TestDefinitionUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	dbPath := filepath.Join(tmp, "ua.db")
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	cookieText := "# Netscape HTTP Cookie File\n.hdbits.org\tTRUE\t/\tTRUE\t0\tuid\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, "HDB.txt"), []byte(cookieText), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	uploadSeen := false
	downloadSeen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/upload/upload":
			uploadSeen = true
			if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "multipart/form-data") {
				t.Errorf("expected multipart content-type, got %q", ct)
			}
			if err := r.ParseMultipartForm(5 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			if r.FormValue("name") == "" {
				t.Fatal("expected upload name field")
			}
			if descr := r.FormValue("descr"); !strings.Contains(descr, "https://t.hdbits.org/rehosted.jpg") {
				t.Fatalf("expected provided rehosted screenshot in description, got %q", descr)
			}
			files := r.MultipartForm.File["file"]
			if len(files) == 0 {
				t.Fatal("expected torrent file in multipart form")
			}
			f, err := files[0].Open()
			if err != nil {
				t.Fatalf("open uploaded file: %v", err)
			}
			_, _ = io.ReadAll(f)
			_ = f.Close()
			http.Redirect(w, r, "/details.php?id=123&uploaded=1", http.StatusFound)
		case r.URL.Path == "/details.php":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case strings.HasPrefix(r.URL.Path, "/download.php/"):
			downloadSeen = true
			if r.URL.Query().Get("id") != "123" {
				t.Fatalf("expected id=123, got %q", r.URL.Query().Get("id"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("reseed-torrent-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	d := New()
	result, err := d.Upload(context.Background(), trackers.UploadRequest{
		Tracker: "HDB",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			VideoCodec:  "HEVC",
			ReleaseName: "My.Release.2026.2160p.WEBDL.HEVC",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			Username: "user",
			Passkey:  "pass",
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Logger:    api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://t.hdbits.org/rehosted.jpg",
				WebURL: "https://img.hdbits.org/rehosted",
				RawURL: "https://img.hdbits.org/rehosted",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", result.Uploaded)
	}
	if len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent artifact, got %d", len(result.UploadedTorrents))
	}
	if result.UploadedTorrents[0].Tracker != "HDB" {
		t.Fatalf("expected tracker HDB, got %q", result.UploadedTorrents[0].Tracker)
	}
	if result.UploadedTorrents[0].TorrentID != "123" {
		t.Fatalf("expected torrent id 123, got %q", result.UploadedTorrents[0].TorrentID)
	}
	if !uploadSeen {
		t.Fatal("expected upload endpoint to be called")
	}
	if !downloadSeen {
		t.Fatal("expected download endpoint to be called")
	}
	artifactPath := result.UploadedTorrents[0].TorrentPath
	if strings.TrimSpace(artifactPath) == "" {
		t.Fatal("expected tracker-specific torrent path")
	}
	updated, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read updated torrent: %v", err)
	}
	if string(updated) != "reseed-torrent-bytes" {
		t.Fatalf("expected downloaded torrent bytes, got %q", string(updated))
	}
	original, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read original torrent: %v", err)
	}
	if string(original) != "dummy" {
		t.Fatalf("expected original torrent to remain unchanged, got %q", string(original))
	}
}

func TestDefinitionBuildUploadDryRunUsesProvidedAssets(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := buildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "HDB",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			VideoCodec:  "HEVC",
			ReleaseName: "My.Release.2026.2160p.WEBDL.HEVC",
		},
		TrackerConfig: config.TrackerConfig{
			Username: "user",
			Passkey:  "pass",
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}},
		Logger:    api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://t.hdbits.org/dryrun.jpg",
				WebURL: "https://img.hdbits.org/dryrun",
				RawURL: "https://img.hdbits.org/dryrun",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	if !strings.Contains(entry.Description, "https://t.hdbits.org/dryrun.jpg") {
		t.Fatalf("expected provided screenshot in dry-run description, got %q", entry.Description)
	}
}

func TestBuildUploadFieldsSkipsTVDBForMovie(t *testing.T) {
	fields := buildUploadFields(api.PreparedMetadata{
		MediaInfoCategory: "TV",
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE", TVDBID: 765432},
	}, config.Config{}, 1, 5, 6, "description")

	if _, ok := fields["tvdb"]; ok {
		t.Fatalf("did not expect tvdb for movie payload")
	}
	if _, ok := fields["tvdb_season"]; ok {
		t.Fatalf("did not expect tvdb_season for movie payload")
	}
	if _, ok := fields["tvdb_episode"]; ok {
		t.Fatalf("did not expect tvdb_episode for movie payload")
	}
}

func TestBuildUploadFieldsIncludesTVDBForTV(t *testing.T) {
	fields := buildUploadFields(api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 765432},
		SeasonInt:   2,
		EpisodeInt:  3,
	}, config.Config{}, 2, 5, 6, "description")

	if got := fields["tvdb"]; got != "765432" {
		t.Fatalf("expected tvdb=765432, got %q", got)
	}
	if got := fields["tvdb_season"]; got != "2" {
		t.Fatalf("expected tvdb_season=2, got %q", got)
	}
	if got := fields["tvdb_episode"]; got != "3" {
		t.Fatalf("expected tvdb_episode=3, got %q", got)
	}
}
