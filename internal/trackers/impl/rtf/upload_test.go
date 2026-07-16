// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildUploadDryRunUsesDescriptionPayloadAndLeavesNFOEmpty(t *testing.T) {
	root := t.TempDir()
	torrentPath := filepath.Join(root, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := buildUploadDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "RTF",
		Meta: api.UploadSubject{
			TorrentPath:         torrentPath,
			DescriptionOverride: "Custom description",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Assets:        &trackers.DescriptionAssets{Description: "Custom description"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Payload["description"] != "Custom description" {
		t.Fatalf("expected description payload, got %#v", entry.Payload)
	}
	if entry.Payload["descr"] != "Custom description" {
		t.Fatalf("expected descr payload mirror, got %#v", entry.Payload)
	}
	if entry.Payload["nfo"] != "" {
		t.Fatalf("expected empty nfo payload, got %#v", entry.Payload["nfo"])
	}
}

func TestUploadRefreshesExpiredAPIKeyAndPersistsIt(t *testing.T) {
	root := t.TempDir()
	torrentPath := filepath.Join(root, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	dbPath := filepath.Join(root, "upbrr.db")
	seedRTFConfig(t, dbPath, config.TrackerConfig{
		APIKey:   "old-token",
		Username: "user",
		Password: "pass",
	})

	var testedToken string
	var loginCalled bool
	var uploadToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/test":
			testedToken = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/login":
			loginCalled = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"new-token"}`))
		case "/api/upload":
			uploadToken = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"error":false,"torrent":{"id":123}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	summary, err := upload(context.Background(), trackers.PreparationInput{
		Tracker: "RTF",
		Meta: api.UploadSubject{
			TorrentPath: torrentPath,
			ReleaseName: "Release",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			APIKey:   "old-token",
			Username: "user",
			Password: "pass",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected upload success, got %#v", summary)
	}
	if testedToken != "old-token" {
		t.Fatal("expected old token to be tested")
	}
	if !loginCalled {
		t.Fatal("expected expired token to trigger API login")
	}
	if uploadToken != "new-token" {
		t.Fatal("expected upload to use refreshed token")
	}
	if got := loadStoredRTFAPIKey(t, dbPath); got != "new-token" {
		t.Fatal("expected refreshed token persisted")
	}
}

func TestUploadBlockedExpiredAPIKeyDoesNotRefreshOrPersist(t *testing.T) {
	root := t.TempDir()
	torrentPath := filepath.Join(root, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	dbPath := filepath.Join(root, "upbrr.db")
	seedRTFConfig(t, dbPath, config.TrackerConfig{
		APIKey:   "old-token",
		Username: "user",
		Password: "pass",
	})

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	_, err := upload(context.Background(), trackers.PreparationInput{
		Tracker: "RTF",
		Meta: api.UploadSubject{
			TorrentPath: torrentPath,
			ReleaseName: "Recent Release",
			Release:     api.ReleaseInfo{Year: 9999},
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			APIKey:   "old-token",
			Username: "user",
			Password: "pass",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
	})
	if err == nil {
		t.Fatal("expected blocked upload error")
	}
	if !strings.Contains(err.Error(), "content must be at least 10 years old") {
		t.Fatalf("expected eligibility error, got %v", err)
	}
	if requests != 0 {
		t.Fatalf("blocked upload must not call RTF auth or upload endpoints, got %d requests", requests)
	}
	if got := loadStoredRTFAPIKey(t, dbPath); got != "old-token" {
		t.Fatal("blocked upload must leave stored token unchanged")
	}
}

func TestUploadUsesValidAPIKeyWithoutRefresh(t *testing.T) {
	root := t.TempDir()
	torrentPath := filepath.Join(root, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	var loginCalled bool
	var uploadToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/test":
			w.WriteHeader(http.StatusOK)
		case "/api/login":
			loginCalled = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"new-token"}`))
		case "/api/upload":
			uploadToken = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"error":false,"torrent":{"id":123}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	if _, err := upload(context.Background(), trackers.PreparationInput{
		Tracker: "RTF",
		Meta: api.UploadSubject{
			TorrentPath: torrentPath,
			ReleaseName: "Release",
		},
		TrackerConfig: config.TrackerConfig{
			URL:    server.URL,
			APIKey: "valid-token",
		},
	}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if loginCalled {
		t.Fatal("valid token should not trigger API login")
	}
	if uploadToken != "valid-token" {
		t.Fatal("expected upload to use original token")
	}
}

func TestUploadGeneratesMissingAPIKeyFromCredentials(t *testing.T) {
	root := t.TempDir()
	torrentPath := filepath.Join(root, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent-bytes"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	dbPath := filepath.Join(root, "upbrr.db")
	seedRTFConfig(t, dbPath, config.TrackerConfig{Username: "user", Password: "pass"})

	var testCalled bool
	var uploadToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/test":
			testCalled = true
			w.WriteHeader(http.StatusOK)
		case "/api/login":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"generated-token"}`))
		case "/api/upload":
			uploadToken = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"error":false,"torrent":{"id":123}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	if _, err := upload(context.Background(), trackers.PreparationInput{
		Tracker: "RTF",
		Meta: api.UploadSubject{
			TorrentPath: torrentPath,
			ReleaseName: "Release",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			Username: "user",
			Password: "pass",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
	}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if testCalled {
		t.Fatal("missing token should refresh directly without API test")
	}
	if uploadToken != "generated-token" {
		t.Fatal("expected upload to use generated token")
	}
	if got := loadStoredRTFAPIKey(t, dbPath); got != "generated-token" {
		t.Fatal("expected generated token persisted")
	}
}

func TestUploadRejectsMalformedBaseURL(t *testing.T) {
	_, err := upload(context.Background(), trackers.PreparationInput{
		Tracker:       "RTF",
		TrackerConfig: config.TrackerConfig{URL: "not a url", APIKey: "token"},
	})
	if err == nil {
		t.Fatal("expected malformed base URL error")
	}
	if !strings.Contains(err.Error(), "invalid base URL") {
		t.Fatalf("expected invalid base URL error, got %v", err)
	}
}

func seedRTFConfig(t *testing.T, dbPath string, trackerCfg config.TrackerConfig) {
	t.Helper()

	repo, err := servicedb.OpenContext(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(context.Background()); err != nil {
		t.Fatalf("MigrateContext: %v", err)
	}
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{"RTF": trackerCfg},
		},
	}
	if err := config.SaveToDatabase(context.Background(), &cfg, repo); err != nil {
		t.Fatalf("SaveToDatabase: %v", err)
	}
}

func loadStoredRTFAPIKey(t *testing.T, dbPath string) string {
	t.Helper()

	repo, err := servicedb.OpenContext(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	cfg, err := config.LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabase: %v", err)
	}
	return cfg.Trackers.Trackers["RTF"].APIKey
}
