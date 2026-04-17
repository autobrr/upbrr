// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionBuildDescriptionUsesMTVGroup(t *testing.T) {
	d := New()
	result, err := d.BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "MTV",
		Meta:    api.PreparedMetadata{},
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Group != "mtv" {
		t.Fatalf("expected mtv group, got %q", result.Group)
	}
}

func TestDefinitionUploadMissingCookieFile(t *testing.T) {
	d := New()
	_, err := d.Upload(context.Background(), trackers.UploadRequest{
		Tracker:   "MTV",
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "ua.db")}},
		Logger:    api.NopLogger{},
	})
	if err == nil {
		t.Fatal("expected missing cookie file error")
	}
}

func TestDefinitionUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cookieDir, "MTV.json"), []byte(`{"session":"cookievalue"}`), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	torrentPath := filepath.Join(tmp, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("...authkey=1234567890abcdef1234567890abcdef..."))
		case "/upload.php":
			if err := r.ParseMultipartForm(5 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			if r.FormValue("auth") == "" {
				t.Fatal("expected auth field")
			}
			http.Redirect(w, r, "/torrents.php?id=55", http.StatusFound)
		case "/torrents.php":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	d := New()
	result, err := d.Upload(context.Background(), trackers.UploadRequest{
		Tracker: "MTV",
		Meta: api.PreparedMetadata{
			SourcePath:      filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:     torrentPath,
			ExternalIDs:     api.ExternalIDs{Category: "MOVIE"},
			Type:            "WEBDL",
			VideoCodec:      "HEVC",
			ReleaseName:     "My.Release.2026.2160p.WEBDL.HEVC",
			ServiceLongName: "Netflix",
		},
		TrackerConfig: config.TrackerConfig{URL: server.URL},
		AppConfig:     config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", result.Uploaded)
	}
}

func TestDefinitionUploadLoginBootstrapSuccess(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	torrentPath := filepath.Join(tmp, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<input name="token" value="123456789012345678901234567890123456789012345678"/>`))
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse login form: %v", err)
			}
			if r.FormValue("username") != "user" || r.FormValue("password") != "pass" {
				t.Fatalf("unexpected login creds user=%q pass=%q", r.FormValue("username"), r.FormValue("password"))
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "cookievalue", Path: "/"})
			http.Redirect(w, r, "/index.php", http.StatusFound)
		case "/index.php":
			if _, err := r.Cookie("session"); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("...authkey=abcdefabcdefabcdefabcdefabcdefab..."))
		case "/upload.php":
			if err := r.ParseMultipartForm(5 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			if r.FormValue("auth") == "" {
				t.Fatal("expected auth field")
			}
			http.Redirect(w, r, "/torrents.php?id=99", http.StatusFound)
		case "/torrents.php":
			if _, err := url.ParseRequestURI(r.RequestURI); err != nil {
				t.Fatalf("invalid request uri: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	d := New()
	result, err := d.Upload(context.Background(), trackers.UploadRequest{
		Tracker: "MTV",
		Meta: api.PreparedMetadata{
			SourcePath:      filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:     torrentPath,
			ExternalIDs:     api.ExternalIDs{Category: "MOVIE"},
			Type:            "WEBDL",
			VideoCodec:      "HEVC",
			ReleaseName:     "My.Release.2026.2160p.WEBDL.HEVC",
			ServiceLongName: "Netflix",
		},
		TrackerConfig: config.TrackerConfig{URL: server.URL, Username: "user", Password: "pass"},
		AppConfig:     config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", result.Uploaded)
	}
	if _, err := os.Stat(filepath.Join(tmp, "cookies", "MTV.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no legacy MTV cookie file after login bootstrap, got err=%v", err)
	}
}
