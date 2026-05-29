// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionBuildDescriptionUsesPTPGroup(t *testing.T) {
	d := New()
	result, err := d.BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "PTP",
		Meta:    api.PreparedMetadata{},
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Group != "ptp" {
		t.Fatalf("expected ptp group, got %q", result.Group)
	}
}

func TestDefinitionBuildUploadDryRunForExistingGroup(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case ptpTorrentPath:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Movies": []map[string]any{{"GroupId": "1234"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	entry, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "PTP",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ReleaseName: "Movie.2026.1080p.BluRay.x264",
			Source:      "BluRay",
			VideoCodec:  "AVC",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 1234567},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Title: "Movie", Year: 2026, Poster: "https://img.example/poster.jpg", Genres: "Action"},
			},
		},
		TrackerConfig: config.TrackerConfig{
			URL:        server.URL,
			PTPAPIUser: "user",
			PTPAPIKey:  "key",
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}},
		Logger:    api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	if got := entry.Payload["groupid"]; got != "1234" {
		t.Fatalf("expected existing group id, got %q", got)
	}
	if _, exists := entry.Payload["title"]; exists {
		t.Fatal("did not expect new-group title field when group already exists")
	}
	if entry.Questionnaire != nil {
		t.Fatal("did not expect questionnaire for existing group upload")
	}
}

func TestDefinitionBuildUploadDryRunForNewGroupIncludesQuestionnaire(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)

	entry, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "PTP",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ReleaseName: "Movie.2026.1080p.BluRay.x264",
			Source:      "BluRay",
			VideoCodec:  "AVC",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 1234567},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{
					Title:    "Movie",
					Year:     2026,
					Poster:   "https://img.example/poster.jpg",
					Genres:   "Action",
					Overview: "Plot",
				},
			},
		},
		TrackerConfig: config.TrackerConfig{},
		AppConfig:     config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	if entry.Questionnaire == nil {
		t.Fatal("expected questionnaire for new group upload")
	}
	if got := len(entry.Questionnaire.Fields); got == 0 {
		t.Fatal("expected questionnaire fields")
	}
	if entry.Questionnaire.Fields[0].Key != "title" {
		t.Fatalf("expected first questionnaire field to be title, got %q", entry.Questionnaire.Fields[0].Key)
	}
}

func TestDefinitionUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	torrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case ptpLoginPath:
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse login: %v", err)
			}
			if r.FormValue("username") != "user" || r.FormValue("password") != "pass" {
				t.Fatalf("unexpected login credentials")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "cookievalue", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Result":        "Ok",
				"AntiCsrfToken": "csrf-token",
			})
		default:
			switch r.URL.Path {
			case ptpUploadPath:
				if err := r.ParseMultipartForm(5 << 20); err != nil {
					t.Fatalf("parse multipart: %v", err)
				}
				if r.FormValue("AntiCsrfToken") != "csrf-token" {
					t.Fatalf("expected csrf token, got %q", r.FormValue("AntiCsrfToken"))
				}
				if r.FormValue("title") != "Movie" {
					t.Fatalf("expected new-group title field")
				}
				http.Redirect(w, r, "/torrents.php?id=555&torrentid=666", http.StatusFound)
			case ptpTorrentPath:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}))
	defer server.Close()

	result, err := New().Upload(context.Background(), trackers.UploadRequest{
		Tracker: "PTP",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ReleaseName: "Movie.2026.1080p.BluRay.x264",
			Source:      "BluRay",
			VideoCodec:  "AVC",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 1234567},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{
					Title:    "Movie",
					Year:     2026,
					Poster:   "https://img.example/poster.jpg",
					Genres:   "Action",
					Overview: "Plot",
				},
			},
		},
		TrackerConfig: config.TrackerConfig{
			URL:         server.URL,
			Username:    "user",
			Password:    "pass",
			AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Logger:    api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", result.Uploaded)
	}
	if len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected uploaded torrent artifact")
	}
	if result.UploadedTorrents[0].TorrentID != "666" {
		t.Fatalf("expected torrent id 666, got %q", result.UploadedTorrents[0].TorrentID)
	}
	artifactPath := result.UploadedTorrents[0].TorrentPath
	if strings.TrimSpace(artifactPath) == "" {
		t.Fatal("expected tracker torrent path")
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected tracker torrent file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "cookies", ptpCookieFile)); !os.IsNotExist(err) {
		t.Fatalf("expected no legacy PTP cookie file after upload, got err=%v", err)
	}
}

func TestRehostPosterToSelectedHostUploadsPoster(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	sourcePath := filepath.Join(tmp, "Movie.mkv")
	if err := os.WriteFile(sourcePath, []byte("movie"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	posterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("poster-bytes"))
	}))
	defer posterServer.Close()

	images := &stubPTPImageHosting{
		uploaded: []api.UploadedImageLink{{
			Host:   "ptpimg",
			RawURL: "https://ptpimg.me/rehosted.png",
		}},
	}
	got := rehostPosterToSelectedHost(context.Background(), trackers.UploadRequest{
		Meta: api.PreparedMetadata{
			SourcePath: sourcePath,
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Logger:    api.NopLogger{},
		Images:    images,
	}, posterServer.URL+"/poster")

	if got != "https://ptpimg.me/rehosted.png" {
		t.Fatalf("expected rehosted poster URL, got %q", got)
	}
	if images.host != "ptpimg" {
		t.Fatalf("expected ptpimg upload host, got %q", images.host)
	}
	if len(images.images) != 1 {
		t.Fatalf("expected one uploaded poster, got %d", len(images.images))
	}
	posterPath := images.images[0].Path
	if filepath.Base(posterPath) != "PTP_POSTER.png" {
		t.Fatalf("expected content-type poster extension, got %q", filepath.Base(posterPath))
	}
	body, err := os.ReadFile(posterPath)
	if err != nil {
		t.Fatalf("read poster: %v", err)
	}
	if string(body) != "poster-bytes" {
		t.Fatalf("unexpected poster body %q", string(body))
	}
}

func TestRehostPosterToSelectedHostSkipsSelectedHost(t *testing.T) {
	images := &stubPTPImageHosting{}
	got := rehostPosterToSelectedHost(context.Background(), trackers.UploadRequest{
		Images: images,
	}, "https://ptpimg.me/existing.jpg")
	if got != "https://ptpimg.me/existing.jpg" {
		t.Fatalf("expected original poster URL, got %q", got)
	}
	if len(images.images) != 0 {
		t.Fatalf("expected no upload for selected host poster")
	}
}

type stubPTPImageHosting struct {
	host     string
	scope    string
	images   []api.ScreenshotImage
	uploaded []api.UploadedImageLink
}

func (s *stubPTPImageHosting) ListCandidates(context.Context, api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *stubPTPImageHosting) Upload(_ context.Context, _ api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	s.host = host
	s.scope = usageScope
	s.images = append([]api.ScreenshotImage(nil), images...)
	return s.uploaded, nil
}

func createTestTorrent(t *testing.T, sourcePath string, torrentPath string) {
	t.Helper()

	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       sourcePath,
		OutputPath: torrentPath,
		IsPrivate:  true,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
}
