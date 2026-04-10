// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNUploadEndToEndSuccess(t *testing.T) {
	t.Parallel()

	var autofillCalls atomic.Int32
	var uploadCalls atomic.Int32
	var loginCalls atomic.Int32
	var downloadCalls atomic.Int32
	var uploadFormMu sync.Mutex
	uploadFormValues := map[string]string{}
	uploadFileCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login.php" && r.Method == http.MethodPost:
			loginCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodPost:
			contentType := r.Header.Get("Content-Type")
			if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
				autofillCalls.Add(1)
				_, _ = w.Write([]byte(`
					<input name="artist" value="Example Show" />
					<input name="title" value="Episode One" />
					<input name="seriesid" value="999" />
					<input name="year" value="2025" />
					<input name="tags" value="action" />
					<input name="image" value="https://img.example/poster.jpg" />
					<textarea name="album_desc">Episode overview: TBA</textarea>
					<select name="format"><option selected value="MKV">MKV</option></select>
					<select name="bitrate"><option selected value="H.265">H.265</option></select>
					<select name="media"><option selected value="WEB-DL">WEB-DL</option></select>
					<select name="resolution"><option selected value="1080p">1080p</option></select>
				`))
				return
			}
			uploadCalls.Add(1)
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Fatalf("parse multipart form: %v", err)
			}
			uploadFormMu.Lock()
			for key, values := range r.MultipartForm.Value {
				if len(values) == 0 {
					continue
				}
				uploadFormValues[key] = values[0]
			}
			uploadFileCount = len(r.MultipartForm.File["file_input"])
			uploadFormMu.Unlock()
			w.Header().Set("Location", "/torrents.php?id=123&torrentid=456")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/torrents.php" && r.URL.Query().Get("action") == "download":
			downloadCalls.Add(1)
			_, _ = w.Write([]byte("d8:announce13:https://x.ee"))
		case r.URL.Path == "/torrents.php":
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "Example.Show.S01E01.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "input.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	req := trackers.UploadRequest{
		Tracker: "BTN",
		Meta: api.PreparedMetadata{
			SourcePath:      sourcePath,
			TorrentPath:     torrentPath,
			ReleaseName:     "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			Type:            "WEBDL",
			Source:          "WEB-DL",
			Container:       "MKV",
			VideoEncode:     "x265",
			VideoCodec:      "HEVC",
			SeasonInt:       1,
			EpisodeInt:      1,
			EpisodeTitle:    "Episode One",
			EpisodeOverview: "Overview",
			TVDBAiredDate:   "2025-01-01",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
			},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			DescriptionOverride: "[b]Test[/b] description",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			Username: "user",
			Password: "pass",
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			}},
		},
	}

	summary, err := upload(context.Background(), req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent, got %d", len(summary.UploadedTorrents))
	}
	if got := summary.UploadedTorrents[0].TorrentID; got != "456" {
		t.Fatalf("expected torrent id 456, got %q", got)
	}
	if strings.TrimSpace(summary.UploadedTorrents[0].TorrentPath) == "" {
		t.Fatalf("expected tracker torrent path")
	}
	payload, err := os.ReadFile(summary.UploadedTorrents[0].TorrentPath)
	if err != nil {
		t.Fatalf("expected tracker torrent file: %v", err)
	}
	if len(payload) == 0 || payload[0] != 'd' {
		t.Fatalf("expected bencode torrent payload")
	}

	if loginCalls.Load() != 1 {
		t.Fatalf("expected one login call, got %d", loginCalls.Load())
	}
	if autofillCalls.Load() != 1 {
		t.Fatalf("expected one autofill call, got %d", autofillCalls.Load())
	}
	if uploadCalls.Load() != 1 {
		t.Fatalf("expected one upload call, got %d", uploadCalls.Load())
	}
	if downloadCalls.Load() != 1 {
		t.Fatalf("expected one torrent download call, got %d", downloadCalls.Load())
	}

	uploadFormMu.Lock()
	defer uploadFormMu.Unlock()
	if uploadFileCount != 1 {
		t.Fatalf("expected one file_input upload, got %d", uploadFileCount)
	}
	if got := uploadFormValues["type"]; got != "Episode" {
		t.Fatalf("expected type Episode, got %q", got)
	}
	if got := uploadFormValues["format"]; got != "MKV" {
		t.Fatalf("expected format MKV, got %q", got)
	}
	if got := uploadFormValues["bitrate"]; got != "H.265" {
		t.Fatalf("expected bitrate H.265, got %q", got)
	}
	if got := uploadFormValues["media"]; got != "WEB-DL" {
		t.Fatalf("expected media WEB-DL, got %q", got)
	}
	if got := uploadFormValues["resolution"]; got != "1080p" {
		t.Fatalf("expected resolution 1080p, got %q", got)
	}
	if got := uploadFormValues["origin"]; got != "P2P" {
		t.Fatalf("expected origin P2P, got %q", got)
	}
	if got := uploadFormValues["scenename"]; !strings.Contains(got, "H.265") || strings.Contains(got, "x265") {
		t.Fatalf("expected scenename codec remap to H.265, got %q", got)
	}
}

func TestBTNPrepareUploadDataFailsOnAutofillFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			_, _ = io.WriteString(w, `<input name="artist" value="Autofill Fail"><input name="title" value="Autofill Fail">`)
		case "/login.php":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	uploadCtx := uploadContext{
		baseURL:   server.URL,
		uploadURL: server.URL + "/upload.php",
		client:    server.Client(),
	}

	req := trackers.UploadRequest{Meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}, ReleaseName: "Show.S01E01", Type: "WEBDL", Source: "WEB-DL", Container: "MKV", VideoEncode: "x265", VideoCodec: "HEVC", SeasonInt: 1, EpisodeInt: 1, Release: api.ReleaseInfo{Resolution: "1080p"}}}
	_, err := prepareUploadData(context.Background(), req, uploadCtx)
	if err == nil {
		t.Fatalf("expected autofill validation error")
	}
	if !strings.Contains(err.Error(), "autofill validation failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBTNUploadFallsBackToAPIResolution(t *testing.T) {
	t.Parallel()

	var apiSearchCalls atomic.Int32
	var apiDownloadCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login.php" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodPost:
			contentType := r.Header.Get("Content-Type")
			if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
				_, _ = w.Write([]byte(`
					<input name="artist" value="Example Show" />
					<input name="title" value="Episode One" />
					<input name="seriesid" value="999" />
					<textarea name="album_desc">Episode overview: TBA</textarea>
					<select name="format"><option selected value="MKV">MKV</option></select>
					<select name="bitrate"><option selected value="H.265">H.265</option></select>
					<select name="media"><option selected value="WEB-DL">WEB-DL</option></select>
					<select name="resolution"><option selected value="1080p">1080p</option></select>
				`))
				return
			}
			w.Header().Set("Location", "/torrents.php?id=123&torrentid=456")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/torrents.php" && r.URL.Query().Get("action") == "download":
			// Force fallback: looks like HTML page, not bencoded torrent payload.
			_, _ = w.Write([]byte("<html>not a torrent</html>"))
		case r.URL.Path == "/rpc" && r.Method == http.MethodPost:
			var rpc struct {
				Method string `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&rpc)
			switch rpc.Method {
			case "getTorrentsSearch":
				apiSearchCalls.Add(1)
				_, _ = w.Write([]byte(`{"result":{"torrents":{"777":{"GroupID":"123"}}}}`))
			case "getTorrentById":
				apiDownloadCalls.Add(1)
				_, _ = w.Write([]byte("d8:announce13:https://x.ee"))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "Example.Show.S01E01.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "input.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	req := trackers.UploadRequest{
		Tracker: "BTN",
		Meta: api.PreparedMetadata{
			SourcePath:          sourcePath,
			TorrentPath:         torrentPath,
			ReleaseName:         "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			Type:                "WEBDL",
			Source:              "WEB-DL",
			Container:           "MKV",
			VideoEncode:         "x265",
			VideoCodec:          "HEVC",
			SeasonInt:           1,
			EpisodeInt:          1,
			EpisodeTitle:        "Episode One",
			EpisodeOverview:     "Overview",
			TVDBAiredDate:       "2025-01-01",
			DescriptionOverride: "[b]Test[/b] description",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
			},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			Username: "user",
			Password: "pass",
			Unknown: map[string]interface{}{
				"api_url": server.URL + "/rpc",
			},
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			}},
		},
	}

	summary, err := upload(context.Background(), req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent, got %d", len(summary.UploadedTorrents))
	}
	payload, err := os.ReadFile(summary.UploadedTorrents[0].TorrentPath)
	if err != nil {
		t.Fatalf("expected tracker torrent file: %v", err)
	}
	if len(payload) == 0 || payload[0] != 'd' {
		t.Fatalf("expected bencode torrent payload from API fallback")
	}
	if apiSearchCalls.Load() != 1 {
		t.Fatalf("expected one API search call, got %d", apiSearchCalls.Load())
	}
	if apiDownloadCalls.Load() != 1 {
		t.Fatalf("expected one API download call, got %d", apiDownloadCalls.Load())
	}
}
