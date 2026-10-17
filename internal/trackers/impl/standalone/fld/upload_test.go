// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func validTorrentFixture(t *testing.T) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name:        "Example.Release.2026.mkv",
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Length:      1000,
	})
	if err != nil {
		t.Fatalf("marshal torrent info: %v", err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{
		Announce:  "http://localhost/announce",
		InfoBytes: infoBytes,
	}).Write(&payload); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	return payload.Bytes()
}

func TestPrepareUploadDryRunPayload(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "test.torrent")

	if err := os.WriteFile(mediaInfoPath, []byte("Format : Matroska\nFile size : 1.23 GiB"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, validTorrentFixture(t), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Show",
		Year:       2026,
		Season:     "1",
		Episode:    "1",
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "DD+ 5.1",
		Tag:        "-GRP",
	}, api.NopLogger{})

	meta := api.UploadSubject{
		SourcePath:        filepath.Join(tmp, "test.mkv"),
		TorrentPath:       torrentPath,
		MediaInfoTextPath: mediaInfoPath,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
			TMDBID:   12345,
			IMDBID:   67890,
		},
		ReleaseName:           generated.Name,
		ReleaseNameNoTag:      generated.NameNoTag,
		GeneratedName:         generated.GeneratedName,
		GeneratedReleaseNames: generated.GeneratedVariants,
		Release: api.ReleaseInfo{
			Edition: []string{"Unrated", "Director's Cut"},
		},
		TVPack: false,
	}

	req := trackers.PreparationInput{
		Tracker: "FLD",
		Intent:  trackers.PreparationIntentDryRun,
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey: "my_fld_api_key",
			Anon:   true,
		},
		Runtime: trackers.PreparationRuntime{
			DBPath: filepath.Join(tmp, "upbrr.db"),
		},
		Logger: api.NopLogger{},
	}

	plan, failure := New().Prepare(context.Background(), req)
	if failure != nil {
		t.Fatalf("unexpected prepare upload failure: %v", failure)
	}
	entry := plan.DryRun()

	if !strings.Contains(entry.Payload["name"], "DDP") || strings.Contains(entry.Payload["name"], "DD+") {
		t.Errorf("expected DD+ replaced with DDP, got: %q", entry.Payload["name"])
	}
	if entry.Payload["imdb_id"] != "tt0067890" {
		t.Errorf("expected tt prefixed imdb_id, got: %q", entry.Payload["imdb_id"])
	}
	if entry.Payload["tmdb_id"] != "tv/12345" {
		t.Errorf("expected tv prefixed tmdb_id, got: %q", entry.Payload["tmdb_id"])
	}
	if entry.Payload["anonymous"] != "checked" {
		t.Errorf("expected anonymous to be checked, got: %q", entry.Payload["anonymous"])
	}
	if entry.Payload["media_type"] != "show_episode" {
		t.Errorf("expected media_type show_episode, got: %q", entry.Payload["media_type"])
	}
	if entry.Payload["edition"] != "Unrated Director's Cut" {
		t.Errorf("expected edition joined, got: %q", entry.Payload["edition"])
	}
	if !strings.Contains(entry.Payload["media_info"], "Format : Matroska") {
		t.Errorf("expected mediainfo content, got: %q", entry.Payload["media_info"])
	}

	// Verify API key is NOT exposed in preview
	if _, ok := entry.Payload["api_key"]; ok {
		t.Errorf("api_key must not be exposed in preview payload: %#v", entry.Payload)
	}
	for _, f := range entry.Files {
		if strings.Contains(f.Field, "api_key") {
			t.Errorf("api_key must not be exposed in preview files: %#v", f)
		}
	}
}

func TestPrepareUploadMissingOrNonPositiveTMDbIDProducesEmptyString(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "test.torrent")

	if err := os.WriteFile(mediaInfoPath, []byte("Format : Matroska\nFile size : 1.23 GiB"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, validTorrentFixture(t), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	meta := api.UploadSubject{
		SourcePath:        filepath.Join(tmp, "test.mkv"),
		TorrentPath:       torrentPath,
		MediaInfoTextPath: mediaInfoPath,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			TMDBID:   0,
			IMDBID:   12345,
		},
		ReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP",
	}

	req := trackers.PreparationInput{
		Tracker: "FLD",
		Intent:  trackers.PreparationIntentDryRun,
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey: "my_fld_api_key",
		},
		Runtime: trackers.PreparationRuntime{
			DBPath: filepath.Join(tmp, "upbrr.db"),
		},
		Logger: api.NopLogger{},
	}

	plan, failure := New().Prepare(context.Background(), req)
	if failure != nil {
		t.Fatalf("unexpected prepare upload failure: %v", failure)
	}
	entry := plan.DryRun()

	if entry.Payload["tmdb_id"] != "" {
		t.Fatalf("expected empty tmdb_id for zero ID, got %q (must NEVER be movie/0 or tv/0)", entry.Payload["tmdb_id"])
	}
	if entry.Payload["imdb_id"] != "tt0012345" {
		t.Errorf("expected imdb_id tt0012345, got %q", entry.Payload["imdb_id"])
	}
}

func TestPrepareUploadDryRunDoesNotSubmitToNetwork(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "test.torrent")

	if err := os.WriteFile(mediaInfoPath, []byte("Format : Matroska\nFile size : 1.23 GiB"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, validTorrentFixture(t), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	meta := api.UploadSubject{
		SourcePath:        filepath.Join(tmp, "test.mkv"),
		TorrentPath:       torrentPath,
		MediaInfoTextPath: mediaInfoPath,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			TMDBID:   12345,
		},
		ReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP",
	}

	req := trackers.PreparationInput{
		Tracker: "FLD",
		Intent:  trackers.PreparationIntentDryRun,
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey: "my_fld_api_key",
		},
		Runtime: trackers.PreparationRuntime{
			DBPath: filepath.Join(tmp, "upbrr.db"),
		},
		Logger: api.NopLogger{},
	}

	plan, failure := New().Prepare(context.Background(), req)
	if failure != nil {
		t.Fatalf("unexpected prepare upload failure: %v", failure)
	}

	if calls != 0 {
		t.Fatalf("dry run must not make network requests, saw %d calls", calls)
	}
	if _, err := plan.Submit(context.Background()); !errors.Is(err, trackers.ErrPlanNotSubmittable) {
		t.Fatalf("dry-run plan must not be submittable, got %v", err)
	}
}

func TestSubmitPreparedUploadSuccess(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "test.torrent")

	if err := os.WriteFile(mediaInfoPath, []byte("Format : Matroska"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	torrentBytes := validTorrentFixture(t)
	if err := os.WriteFile(torrentPath, torrentBytes, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test_key" {
			t.Errorf("unexpected authorization header")
		}

		if err := r.ParseMultipartForm(10 * 1024 * 1024); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		if r.FormValue("name") != "Example.Movie.2026.1080p-GRP" {
			t.Errorf("expected name Example.Movie.2026.1080p-GRP, got %q", r.FormValue("name"))
		}
		if r.FormValue("imdb_id") != "tt12345" {
			t.Errorf("expected imdb_id tt12345, got %q", r.FormValue("imdb_id"))
		}
		if r.FormValue("tmdb_id") != "movie/67890" {
			t.Errorf("expected tmdb_id movie/67890, got %q", r.FormValue("tmdb_id"))
		}
		if r.FormValue("media_type") != "movie" {
			t.Errorf("expected media_type movie, got %q", r.FormValue("media_type"))
		}

		calls++
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"success":     true,
			"torrent_url": "https://flood.st/torrents/123",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	meta := api.UploadSubject{
		SourcePath:        filepath.Join(tmp, "test.mkv"),
		TorrentPath:       torrentPath,
		MediaInfoTextPath: mediaInfoPath,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			IMDBID:   12345,
			TMDBID:   67890,
		},
		ReleaseName: "Example.Movie.2026.1080p-GRP",
	}

	artifactPath := filepath.Join(tmp, "registered.torrent")
	state := uploadState{
		torrentPath: torrentPath,
		releaseName: "Example.Movie.2026.1080p-GRP",
		fields: map[string]string{
			"name":       "Example.Movie.2026.1080p-GRP",
			"imdb_id":    "tt12345",
			"tmdb_id":    "movie/67890",
			"media_type": "movie",
		},
	}

	body, contentType, err := submitMultipartPayload(state.fields, state.releaseName, state.torrentPath)
	if err != nil {
		t.Fatalf("build multipart payload: %v", err)
	}

	req := trackers.PreparationInput{
		Tracker: "FLD",
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey:      "test_key",
			AnnounceURL: "https://flood.st/announce/passkey",
		},
		Runtime: trackers.PreparationRuntime{DBPath: filepath.Join(tmp, "upbrr.db")},
		Logger:  api.NopLogger{},
	}

	summary, err := submitPreparedUpload(
		context.Background(),
		req,
		state,
		body,
		contentType,
		server.Client(),
		server.URL,
		"test_key",
		"https://flood.st/announce/passkey",
		artifactPath,
	)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}

	if summary.Uploaded != 1 {
		t.Errorf("expected Uploaded = 1, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected 1 uploaded torrent, got %d", len(summary.UploadedTorrents))
	}
	torrent := summary.UploadedTorrents[0]
	if torrent.TorrentURL != "https://flood.st/torrents/123" {
		t.Errorf("expected direct detail url https://flood.st/torrents/123, got %q", torrent.TorrentURL)
	}
	if torrent.TorrentID != "123" {
		t.Errorf("expected concrete torrent ID 123, got %q", torrent.TorrentID)
	}
	if torrent.DownloadURL != "https://flood.st/torrents/123/download?api_key=test_key" {
		t.Errorf("expected download url with api key, got %q", torrent.DownloadURL)
	}
	if torrent.TorrentPath != artifactPath {
		t.Errorf("expected registered torrent path %q, got %q", artifactPath, torrent.TorrentPath)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call, got %d", calls)
	}
}

func TestSubmitPreparedUploadRegisteredArtifactFailureDoesNotTurnSuccessIntoFailedUpload(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"success":     true,
			"torrent_url": "https://flood.st/torrents/456",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Non-existent base torrent path so WritePersonalizedTorrent will fail
	state := uploadState{
		torrentPath: filepath.Join(tmp, "nonexistent.torrent"),
		releaseName: "Example.Movie.2026.1080p-GRP",
		fields:      map[string]string{"name": "Example.Movie.2026.1080p-GRP"},
	}

	req := trackers.PreparationInput{
		Tracker: "FLD",
		TrackerConfig: config.TrackerConfig{
			APIKey:      "test_key",
			AnnounceURL: "https://flood.st/announce/passkey",
		},
		Runtime: trackers.PreparationRuntime{DBPath: filepath.Join(tmp, "upbrr.db")},
		Logger:  api.NopLogger{},
	}

	summary, err := submitPreparedUpload(
		context.Background(),
		req,
		state,
		[]byte("{}"),
		"application/json",
		server.Client(),
		server.URL,
		"test_key",
		"https://flood.st/announce/passkey",
		filepath.Join(tmp, "registered.torrent"),
	)
	if err != nil {
		t.Fatalf("remote success must not fail even when registered torrent creation fails: %v", err)
	}
	if summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected 1 uploaded torrent, got %#v", summary)
	}
	if summary.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("expected empty TorrentPath on artifact failure, got %q", summary.UploadedTorrents[0].TorrentPath)
	}
	if summary.UploadedTorrents[0].TorrentID != "456" {
		t.Errorf("expected torrent ID 456, got %q", summary.UploadedTorrents[0].TorrentID)
	}
}

func TestSubmitPreparedUploadRemoteFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"success": false,
			"message": "torrent with same infohash exists",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	state := uploadState{
		torrentPath: "fake.torrent",
		releaseName: "Example.Movie.2026.1080p-GRP",
		fields:      map[string]string{"name": "Example.Movie.2026.1080p-GRP"},
	}

	req := trackers.PreparationInput{
		Tracker: "FLD",
		TrackerConfig: config.TrackerConfig{
			APIKey: "test_key",
		},
		Runtime: trackers.PreparationRuntime{DBPath: filepath.Join(t.TempDir(), "upbrr.db")},
		Logger:  api.NopLogger{},
	}

	_, err := submitPreparedUpload(
		context.Background(),
		req,
		state,
		[]byte("{}"),
		"application/json",
		server.Client(),
		server.URL,
		"test_key",
		"",
		"",
	)
	if err == nil {
		t.Fatal("expected upload error on remote failure")
	}
	if !strings.Contains(err.Error(), "torrent with same infohash exists") {
		t.Fatalf("expected error message containing remote message, got %v", err)
	}
}

func submitMultipartPayload(fields map[string]string, releaseName string, torrentPath string) ([]byte, string, error) {
	body, contentType, err := commonhttp.BuildMultipartPayload(fields, []commonhttp.FileField{{
		FieldName: "meta_info",
		FileName:  releaseName + ".torrent",
		Path:      torrentPath,
	}})
	if err != nil {
		return nil, "", fmt.Errorf("build multipart payload: %w", err)
	}
	return body, contentType, nil
}
