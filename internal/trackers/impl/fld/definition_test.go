package fld

import (
	"context"
	"encoding/json"
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

func TestDefinitionBuildUploadDryRun(t *testing.T) {
	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "test.torrent")

	if err := os.WriteFile(mediaInfoPath, []byte("Format : Matroska\nFile size : 1.23 GiB"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("d8:announce31:http://localhost/announcee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(tmp, "test.mkv"),
		TorrentPath:       torrentPath,
		MediaInfoTextPath: mediaInfoPath,
		ExternalIDs: api.ExternalIDs{
			TMDBID:   12345,
			IMDBID:   67890,
			Category: "TV",
		},
		ReleaseName: "Example.Show.S01E01.1080p.WEB.DD+5.1-GRP",
		Release: api.ReleaseInfo{
			Edition: []string{"Unrated", "Director's Cut"},
		},
		TVPack: false,
	}

	req := trackers.UploadRequest{
		Tracker: "FLD",
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey: "my_fld_api_key",
			Anon:   true,
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: tmp,
			},
		},
		Logger: api.NopLogger{},
	}

	entry, err := New().BuildUploadDryRun(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected dry run error: %v", err)
	}

	if entry.Payload["name"] != "Example.Show.S01E01.1080p.WEB.DDP5.1-GRP" {
		t.Errorf("expected DD+ replaced with DDP, got: %q", entry.Payload["name"])
	}
	if entry.Payload["imdb_id"] != "tt67890" {
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
}

func TestDefinitionUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "test.torrent")

	if err := os.WriteFile(mediaInfoPath, []byte("MediaInfo Content"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	// A basic valid bencode torrent content
	if err := os.WriteFile(torrentPath, []byte("d8:announce31:http://localhost/announcee"), 0o600); err != nil {
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

		err := r.ParseMultipartForm(10 * 1024 * 1024)
		if err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}

		if r.FormValue("name") != "Example.Movie.2026-GRP" {
			t.Errorf("expected name Example.Movie.2026-GRP, got %q", r.FormValue("name"))
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
		resp := map[string]interface{}{
			"success":     true,
			"torrent_url": "https://flood.st/torrents/123",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Override URL to target httptest server
	originalBaseURL := baseURL
	originalAPIBaseURL := apiBaseURL
	baseURL = server.URL
	apiBaseURL = server.URL
	defer func() {
		baseURL = originalBaseURL
		apiBaseURL = originalAPIBaseURL
	}()

	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(tmp, "test.mkv"),
		TorrentPath:       torrentPath,
		MediaInfoTextPath: mediaInfoPath,
		ExternalIDs: api.ExternalIDs{
			IMDBID:   12345,
			TMDBID:   67890,
			Category: "MOVIE",
		},
		ReleaseName: "Example.Movie.2026-GRP",
	}

	req := trackers.UploadRequest{
		Tracker: "FLD",
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey: "test_key",
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: tmp,
			},
		},
		Logger: api.NopLogger{},
	}

	summary, err := New().Upload(context.Background(), req)
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
		t.Errorf("expected torrent url https://flood.st/torrents/123, got %q", torrent.TorrentURL)
	}
	if torrent.DownloadURL != "https://flood.st/torrents/123/download?api_key=test_key" {
		t.Errorf("expected download url with api key, got %q", torrent.DownloadURL)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call, got %d", calls)
	}
}

func TestDefinitionUploadNameDVD(t *testing.T) {
	cases := []struct {
		name        string
		releaseName string
		source      string
		audio       []string
		codec       []string
		expected    string
	}{
		{
			name:        "Standard DVD conversion",
			releaseName: "Example.Movie.2026.DVD.AC3.5.1-GRP",
			source:      "DVD",
			audio:       []string{"AC3", "5.1"},
			codec:       []string{"MPEG2"},
			expected:    "Example.Movie.2026.DVD.MPEG2.AC3.5.1-GRP",
		},
		{
			name:        "Non-DVD source untouched",
			releaseName: "Example.Movie.2026.1080p.BluRay.AC3.5.1-GRP",
			source:      "BluRay",
			audio:       []string{"AC3", "5.1"},
			codec:       []string{"x264"},
			expected:    "Example.Movie.2026.1080p.BluRay.AC3.5.1-GRP",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := api.PreparedMetadata{
				ReleaseName: tc.releaseName,
				Release: api.ReleaseInfo{
					Source: tc.source,
					Audio:  tc.audio,
					Codec:  tc.codec,
				},
			}
			got := resolveUploadName(meta)
			if got != tc.expected {
				t.Errorf("expected name %q, got %q", tc.expected, got)
			}
		})
	}
}
