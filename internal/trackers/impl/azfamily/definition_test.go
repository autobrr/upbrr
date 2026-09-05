// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	writeAZCookieFile(t, tmp, parsedServerURL.Hostname())

	plan, failure := testDefinitionAt(server.URL).Prepare(context.Background(), trackers.PreparationInput{
		Intent:  trackers.PreparationIntentDryRun,
		Tracker: "AZ",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Release.2026.1080p-GRP",
			Identity:    api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
				Source:     "WEB-DL",
				Type:       "WEBDL",
			},
			Source: "WEB-DL",
			Type:   "WEBDL",
		},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
	})
	if failure != nil {
		t.Fatalf("unexpected error: %v", failure)
	}
	entry := plan.DryRun()
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
}

func TestUploadMissingMediaDecisionAndScreenshotProtocol(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, []byte("mediainfo"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	imageBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	menuPath := "/img/menu.png"
	imagePaths := []string{"/img/1.png", "/img/2.png", "/img/3.png"}
	var mediaAdded atomic.Bool
	var addCount atomic.Int32
	var taskCount atomic.Int32
	var imageIndex atomic.Int32
	var uploadMu sync.Mutex
	uploadedNames := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			_, _ = io.WriteString(w, `<meta name="_token" content="secret-token">`)
		case r.URL.Path == "/ajax/movies/1":
			if mediaAdded.Load() {
				_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
		case r.URL.Path == "/add/movie":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse add media form: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for field, want := range map[string]string{
				"_token":  "secret-token",
				"type_id": "1",
				"title":   "Example Release",
				"imdb_id": "tt0000123",
				"tmdb_id": "",
			} {
				if got := r.Form.Get(field); got != want {
					t.Errorf("add media field %s = %q, want %q", field, got, want)
				}
			}
			addCount.Add(1)
			mediaAdded.Store(true)
			w.Header().Set("Location", "/movies/77")
			w.WriteHeader(http.StatusFound)
		case strings.Contains(r.URL.Path, "/upload/") && strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data"):
			taskCount.Add(1)
			http.Redirect(w, r, "/upload/movie/task/999", http.StatusFound)
		case strings.Contains(r.URL.Path, "/upload/movie/task/999"):
			http.Redirect(w, r, "/torrent/123", http.StatusFound)
		case r.URL.Path == "/requests":
			_, _ = io.WriteString(w, "<html></html>")
		case r.URL.Path == "/ajax/image/upload":
			if got := r.Header.Get("User-Agent"); got != azImageUserAgent {
				t.Errorf("image upload user agent = %q", got)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse image upload: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("qqfile")
			if err != nil {
				t.Errorf("image upload file: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = file.Close()
			if got := header.Header.Get("Content-Type"); got != "image/png" {
				t.Errorf("image upload content type = %q", got)
			}
			uploadMu.Lock()
			uploadedNames = append(uploadedNames, r.FormValue("qqfilename"))
			uploadMu.Unlock()
			index := imageIndex.Add(1)
			_, _ = io.WriteString(w, `{"success":true,"imageId":"img`+strconv.Itoa(int(index))+`","error":[]}`)
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
	writeAZCookieFile(t, tmp, parsedServerURL.Hostname())

	input := trackers.PreparationInput{
		Intent:  trackers.PreparationIntentUpload,
		Tracker: "AZ",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Example.Release.2026.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
			ReleaseName:       "Example.Release.2026.1080p.WEB-DL.x265-GRP",
			Release: api.ReleaseInfo{
				Title:      "Example Release",
				Year:       2026,
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
		Assets: &trackers.DescriptionAssets{
			MenuImages: []api.ScreenshotImage{{RawURL: server.URL + menuPath}},
			Screenshots: []api.ScreenshotImage{
				{RawURL: server.URL + imagePaths[0]},
				{RawURL: server.URL + imagePaths[1]},
				{RawURL: server.URL + imagePaths[2]},
			},
		},
	}

	plan, failure := testDefinitionAt(server.URL).Prepare(context.Background(), input)
	if failure != nil {
		t.Fatalf("unexpected upload preparation error: %v", failure)
	}
	if actions := plan.DryRun().RequiredActions; len(actions) != 1 || actions[0].Kind != api.RequiredActionResolveTrackerPreparation {
		t.Fatalf("missing media actions = %+v", actions)
	}
	if _, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, false); err == nil {
		t.Fatal("declining missing media action returned no error")
	} else {
		var skipped *trackers.PreparationFailure
		if !errors.As(err, &skipped) || skipped.Code() != trackers.PreparationFailureCodeSkipped {
			t.Fatalf("decline error = %v", err)
		}
	}
	if addCount.Load() != 0 || taskCount.Load() != 0 {
		t.Fatalf("decline mutations add=%d task=%d", addCount.Load(), taskCount.Load())
	}

	plan, failure = testDefinitionAt(server.URL).Prepare(context.Background(), input)
	if failure != nil {
		t.Fatalf("prepare confirmed upload: %v", failure)
	}
	plan, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, true)
	if err != nil {
		t.Fatalf("confirm missing media action: %v", err)
	}
	if len(plan.DryRun().RequiredActions) != 0 {
		t.Fatalf("resolved actions = %+v", plan.DryRun().RequiredActions)
	}
	if addCount.Load() != 1 || taskCount.Load() != 1 {
		t.Fatalf("confirmed mutations add=%d task=%d", addCount.Load(), taskCount.Load())
	}
	uploadMu.Lock()
	firstUploadedName := ""
	if len(uploadedNames) > 0 {
		firstUploadedName = uploadedNames[0]
	}
	uploadMu.Unlock()
	if firstUploadedName != "menu.png" {
		t.Fatalf("first uploaded image = %q, want menu.png", firstUploadedName)
	}

	result, err := plan.Submit(context.Background())
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

func TestUploadMissingMediaUnattendedSkips(t *testing.T) {
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
	writeAZCookieFile(t, tmp, parsedServerURL.Hostname())

	_, failure := testDefinitionAt(server.URL).Prepare(context.Background(), trackers.PreparationInput{
		Intent:  trackers.PreparationIntentUpload,
		Tracker: "AZ",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Release.2026.1080p-GRP",
			Identity:    api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
			Release: api.ReleaseInfo{
				Title:      "Example Release",
				Resolution: "1080p",
			},
			Source:  "WEB-DL",
			Type:    "WEBDL",
			Options: api.UploadOptions{InteractionMode: api.InteractionModeUnattended},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:  api.NopLogger{},
	})
	if failure == nil || failure.Code() != trackers.PreparationFailureCodeSkipped {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestUploadScreenshotPreflightRejectsBeforeTask(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, []byte("mediainfo"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	var taskCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			_, _ = io.WriteString(w, `<meta name="_token" content="secret-token">`)
		case r.URL.Path == "/ajax/movies/1":
			_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123"}]}`)
		case strings.Contains(r.URL.Path, "/upload/") && strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data"):
			taskCount.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/img/"):
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	parsedServerURL, _ := url.Parse(server.URL)
	writeAZCookieFile(t, tmp, parsedServerURL.Hostname())

	_, failure := testDefinitionAt(server.URL).Prepare(context.Background(), trackers.PreparationInput{
		Intent:  trackers.PreparationIntentUpload,
		Tracker: "AZ",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Example.Release.2026.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
			ReleaseName:       "Example.Release.2026.1080p.WEB-DL.x265-GRP",
			Release: api.ReleaseInfo{
				Title:      "Example Release",
				Resolution: "1080p",
			},
			Type: "WEBDL",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{Screenshots: []api.ScreenshotImage{
			{RawURL: server.URL + "/img/1.png"},
			{RawURL: server.URL + "/img/2.png"},
		}},
	})
	if failure == nil || !strings.Contains(failure.Message(), "only 2 of 3 required screenshot sources") {
		t.Fatalf("failure = %#v", failure)
	}
	if taskCount.Load() != 0 {
		t.Fatalf("created %d remote tasks before screenshot preflight", taskCount.Load())
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
	writeAZCookieFile(t, tmp, parsedServerURL.Hostname())

	plan, failure := testDefinitionAt(server.URL).Prepare(context.Background(), trackers.PreparationInput{
		Intent:  trackers.PreparationIntentDryRun,
		Tracker: "AZ",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Example.Show.S01E02.1080p.WEB-DL.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{Category: "TV", IMDBID: 123},
			ReleaseName:       "Example.Show.S01E02.1080p.WEB-DL-GRP",
			Release: api.ReleaseInfo{
				Category:   "TV",
				Title:      "Example Show",
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
	if failure != nil {
		t.Fatalf("unexpected error: %v", failure)
	}
	entry := plan.DryRun()
	if entry.Status == "blocked" && strings.Contains(entry.Message, "rip type") {
		t.Fatalf("expected WEB-DL TV release not to be blocked by rip type, got %q", entry.Message)
	}
	if entry.Status == "blocked" {
		t.Fatalf("expected WEB-DL TV release not to be blocked, got status %q: %s", entry.Status, entry.Message)
	}
}

func writeAZCookieFile(t *testing.T, tmp string, domain string) {
	t.Helper()
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	content := "# Netscape HTTP Cookie File\n" + domain + "\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, "AZ.txt"), []byte(content), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
}
