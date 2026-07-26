// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubmitPreparedUploadDownloadsRegisteredTorrentWithAPIKey(t *testing.T) {
	t.Parallel()

	registeredTorrent := dcRegisteredTorrentFixture(t)
	var uploadAuthSeen atomic.Bool
	var downloadAuthSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/torrents/upload":
			uploadAuthSeen.Store(request.Header.Get("X-Api-Key") == "synthetic-api-key")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"message":"ok"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/torrents/download/42":
			downloadAuthSeen.Store(request.Header.Get("X-Api-Key") == "synthetic-api-key")
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(registeredTorrent)
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)

	artifactPath := filepath.Join(t.TempDir(), "registered.torrent")
	summary, err := submitPreparedUpload(
		context.Background(),
		trackers.PreparationInput{Logger: api.NopLogger{}},
		[]byte("{}"),
		"application/json",
		server.Client(),
		server.URL,
		server.URL+"/api/v1/torrents",
		"synthetic-api-key",
		artifactPath,
	)
	if err != nil {
		t.Fatalf("submit DC upload: %v", err)
	}
	if !uploadAuthSeen.Load() || !downloadAuthSeen.Load() {
		t.Fatalf("DC API key missing: upload=%t download=%t", uploadAuthSeen.Load(), downloadAuthSeen.Load())
	}
	if summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("DC upload summary = %#v", summary)
	}
	artifact := summary.UploadedTorrents[0]
	if artifact.Tracker != "DC" || artifact.TorrentID != "42" || artifact.TorrentPath != artifactPath {
		t.Fatalf("DC registered artifact = %#v", artifact)
	}
	persisted, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read DC registered torrent: %v", err)
	}
	if !bytes.Equal(persisted, registeredTorrent) {
		t.Fatal("DC registered torrent bytes changed")
	}
}

func TestSubmitPreparedUploadPreservesRemoteSuccessWhenRegisteredTorrentDownloadFails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/torrents/upload":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"message":"ok"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/torrents/download/42":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)

	summary, err := submitPreparedUpload(
		context.Background(),
		trackers.PreparationInput{Logger: api.NopLogger{}},
		[]byte("{}"),
		"application/json",
		server.Client(),
		server.URL,
		server.URL+"/api/v1/torrents",
		"synthetic-api-key",
		filepath.Join(t.TempDir(), "registered.torrent"),
	)
	if err != nil {
		t.Fatalf("submit DC upload: %v", err)
	}
	if summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("DC upload summary = %#v", summary)
	}
	if summary.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("failed DC download returned artifact path %q", summary.UploadedTorrents[0].TorrentPath)
	}
}

func dcRegisteredTorrentFixture(t *testing.T) []byte {
	t.Helper()

	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name:        "Example.Release.2026.mkv",
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Length:      1,
	})
	if err != nil {
		t.Fatalf("marshal DC torrent info: %v", err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{
		Announce:  "https://tracker.example/announce",
		InfoBytes: infoBytes,
	}).Write(&payload); err != nil {
		t.Fatalf("write DC torrent: %v", err)
	}
	return payload.Bytes()
}
