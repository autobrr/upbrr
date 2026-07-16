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
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func testDefinitionAt(baseURL string) *Definition {
	site := siteFor("AZ")
	site.BaseURL = baseURL
	site.RequestsURL = strings.TrimRight(baseURL, "/") + "/requests"
	return &Definition{site: site}
}

func TestBuildUploadDryRunBlockedWhenMediaMissing(t *testing.T) {
	tmp := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents":
			_, _ = io.WriteString(w, `<meta name="_token" content="secret-token">`)
		case "/ajax/movies/1":
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	parsedServerURL, _ := url.Parse(server.URL)
	writeAZCookieFile(t, tmp, "AZ", parsedServerURL.Hostname())

	entry, err := testDefinitionAt(server.URL).prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker:       "AZ",
		Meta:          api.UploadSubject{Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 123}},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
}

func TestUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, []byte("mediainfo"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	imageBytes := []byte{0x89, 'P', 'N', 'G'}
	imagePaths := []string{"/img/1.png", "/img/2.png", "/img/3.png"}
	imageIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			_, _ = io.WriteString(w, `<meta name="_token" content="secret-token">`)
		case r.URL.Path == "/ajax/movies/1":
			_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123","tmdb":"0"}]}`)
		case strings.Contains(r.URL.Path, "/upload/") && strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data"):
			http.Redirect(w, r, "/upload/movie/task/999", http.StatusFound)
		case strings.Contains(r.URL.Path, "/upload/movie/task/999"):
			http.Redirect(w, r, "/torrent/123", http.StatusFound)
		case r.URL.Path == "/requests":
			_, _ = io.WriteString(w, "<html></html>")
		case r.URL.Path == "/ajax/image/upload":
			imageIndex++
			_, _ = io.WriteString(w, `{"success":true,"imageId":"img`+strconv.Itoa(imageIndex)+`"}`)
		case r.URL.Path == "/download/torrent/123":
			_, _ = w.Write([]byte("personalized-torrent"))
		case strings.HasPrefix(r.URL.Path, "/img/"):
			_, _ = w.Write(imageBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	parsedServerURL, _ := url.Parse(server.URL)
	writeAZCookieFile(t, tmp, "AZ", parsedServerURL.Hostname())

	result, err := testDefinitionAt(server.URL).submit(context.Background(), trackers.PreparationInput{
		Tracker: "AZ",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
			ReleaseName:       "Movie.2024.1080p.WEB-DL.x265-GRP",
			Release: api.ReleaseInfo{
				Title:      "Movie",
				Year:       2024,
				Resolution: "1080p",
			},
			Type:              "WEBDL",
			Container:         "mkv",
			AudioLanguages:    []string{"English"},
			SubtitleLanguages: []string{"English"},
			Options:           api.UploadOptions{KeepImages: true},
		},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
		Assets: &trackers.DescriptionAssets{Screenshots: []api.ScreenshotImage{
			{RawURL: server.URL + imagePaths[0]},
			{RawURL: server.URL + imagePaths[1]},
			{RawURL: server.URL + imagePaths[2]},
		}},
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
	if result.UploadedTorrents[0].TorrentID != "123" {
		t.Fatalf("expected torrent id 123, got %q", result.UploadedTorrents[0].TorrentID)
	}
}

func TestBuildUploadDryRunAllowsTVWebDLRipType(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, []byte("mediainfo"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents":
			_, _ = io.WriteString(w, `<meta name="_token" content="secret-token">`)
		case "/ajax/movies/2":
			_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123","tmdb":"0"}]}`)
		case "/requests":
			_, _ = io.WriteString(w, "<html></html>")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	parsedServerURL, _ := url.Parse(server.URL)
	writeAZCookieFile(t, tmp, "AZ", parsedServerURL.Hostname())

	entry, err := testDefinitionAt(server.URL).prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "AZ",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Show.S01E02.1080p.WEB-DL.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: "TV", IMDBID: 123},
			ReleaseName:       "Show.S01E02.1080p.WEB-DL-GRP",
			Release: api.ReleaseInfo{
				Category:   "TV",
				Title:      "Show",
				Resolution: "1080p",
				Source:     "WEB-DL",
				Type:       "WEB-DL",
			},
			Type:              "WEBDL",
			Source:            "WEB-DL",
			Container:         "mkv",
			AudioLanguages:    []string{"English"},
			SubtitleLanguages: []string{"English"},
			SeasonInt:         1,
			EpisodeInt:        2,
		},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
		Assets:        &trackers.DescriptionAssets{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status == "blocked" && strings.Contains(entry.Message, "rip type") {
		t.Fatalf("expected WEB-DL TV release not to be blocked by rip type, got %q", entry.Message)
	}
	if entry.Status == "blocked" {
		t.Fatalf("expected WEB-DL TV release not to be blocked, got status %q: %s", entry.Status, entry.Message)
	}
}

func writeAZCookieFile(t *testing.T, tmp string, tracker string, domain string) {
	t.Helper()
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	content := "# Netscape HTTP Cookie File\n" + domain + "\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, tracker+".txt"), []byte(content), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
}
