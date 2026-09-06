// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLiveTestClientBlocksWritesAndAllowsReadOnlySearch(t *testing.T) {
	t.Parallel()
	var requests, mutations, searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			searches.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/torrents/add", "/api/v2/torrents/recheck":
			mutations.Add(1)
			_, _ = w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	policy, err := api.NewLiveTestPolicy("client-run", filepath.Join(root, "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{"qbit": {
			Type:     "qbit",
			URL:      server.URL,
			Username: "synthetic",
			Password: "synthetic",
		}},
		ClientSetup: config.ClientSetupConfig{SearchClients: config.CSVList{"qbit"}},
	}
	svc := NewServiceWithRegistry(cfg, nil, nil, policy)
	subject := api.ClientSubject{SourcePath: filepath.Join(root, "Example.Release.2026-GRP.mkv")}
	torrent := api.TorrentResult{Path: filepath.Join(root, "prepared.torrent")}
	if err := os.WriteFile(torrent.Path, []byte("synthetic torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Inject(t.Context(), subject, torrent); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Fatalf("injection = %v", err)
	}
	subject.ClientOverrides.ForceRecheck = new(true)
	if _, err := svc.SearchPathedTorrents(t.Context(), subject); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Fatalf("force recheck = %v", err)
	}
	if requests.Load() != 0 || mutations.Load() != 0 {
		t.Fatalf("blocked calls contacted client: requests=%d mutations=%d", requests.Load(), mutations.Load())
	}
	subject.ClientOverrides.ForceRecheck = new(false)
	if _, err := svc.SearchPathedTorrents(t.Context(), subject); err != nil {
		t.Fatalf("read-only search = %v", err)
	}
	if searches.Load() != 1 || mutations.Load() != 0 {
		t.Fatalf("read-only search calls=%d mutations=%d", searches.Load(), mutations.Load())
	}
	if got := policy.Snapshot().ClientMutation; got != (api.LiveTestEffectCounts{MutationCallsDenied: 2}) {
		t.Fatalf("client receipt = %#v", got)
	}
	ordinary := NewServiceWithRegistry(cfg, nil, nil, nil)
	if err := ordinary.Inject(t.Context(), subject, torrent); err != nil {
		t.Fatalf("ordinary injection = %v", err)
	}
	if mutations.Load() != 1 {
		t.Fatalf("ordinary injection mutations=%d, want 1", mutations.Load())
	}
}
