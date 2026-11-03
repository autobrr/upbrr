// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
	"github.com/autobrr/go-torrent/metainfo"
)

func TestVerifiedQbitInjectionRejectsUnverifiableArtifactBeforeClientIO(t *testing.T) {
	t.Parallel()
	for _, artifact := range []string{"URL only", "invalid metainfo", "v2 only"} {
		t.Run(artifact, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			torrent := api.TorrentResult{URL: "https://registered.example/download", VerifyInjection: true}
			if artifact != "URL only" {
				torrent.Path = filepath.Join(t.TempDir(), "registered.torrent")
				payload := []byte("invalid torrent")
				if artifact == "v2 only" {
					payload = []byte("d4:infod12:meta versioni2e4:name1:x12:piece lengthi16384eee")
				}
				if err := os.WriteFile(torrent.Path, payload, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			service := NewService(config.Config{TorrentClients: map[string]config.TorrentClientConfig{
				"qbit": {Type: "qbit", URL: server.URL},
			}}, nil)
			if err := service.Inject(t.Context(), api.ClientSubject{}, torrent); err == nil || requests.Load() != 0 {
				t.Fatalf("unverifiable injection error=%v client requests=%d", err, requests.Load())
			}
		})
	}
}

func TestVerifiedQbitInjectionStagesOnlyForAnAdd(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"already installed", "fresh add", "unconfirmed add"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			source := filepath.Join(root, "Example.Release.2026.mkv")
			if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			torrentPath := filepath.Join(root, "registered.torrent")
			writeQbitTestTorrentForSource(t, torrentPath, source)
			meta, err := metainfo.LoadFromFile(torrentPath)
			if err != nil {
				t.Fatal(err)
			}
			var adds atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					_, _ = w.Write([]byte("Ok."))
				case "/api/v2/torrents/info":
					switch {
					case adds.Load() > 0 && outcome == "unconfirmed add":
						w.WriteHeader(http.StatusServiceUnavailable)
					case adds.Load() > 0 || outcome == "already installed":
						_ = json.NewEncoder(w).Encode([]map[string]string{{"hash": meta.HashInfoBytes().HexString()}})
					default:
						_, _ = w.Write([]byte("[]"))
					}
				case "/api/v2/torrents/add":
					adds.Add(1)
					_, _ = w.Write([]byte("Ok."))
				default:
					t.Errorf("unexpected client request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			linkRoot := t.TempDir()
			// Existing client data must survive reuse and uncertain add outcomes.
			retained := filepath.Join(linkRoot, "retained.mkv")
			if err := os.WriteFile(retained, []byte("existing data"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewService(config.Config{
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ALPHA": {LinkDirName: "alpha"}}},
				TorrentClients: map[string]config.TorrentClientConfig{"qbit": {
					Type:         "qbit",
					URL:          server.URL,
					Linking:      "hardlink",
					LinkedFolder: config.StringList{linkRoot},
				}},
			}, nil)
			err = service.Inject(t.Context(), api.ClientSubject{SourcePath: source, FileList: []string{source}},
				api.TorrentResult{
					Path:            torrentPath,
					Tracker:         "ALPHA",
					VerifyInjection: true,
				})
			if outcome == "unconfirmed add" {
				if !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
					t.Fatalf("unconfirmed injection = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			linked := filepath.Join(linkRoot, "alpha", filepath.Base(source))
			_, statErr := os.Stat(linked)
			if outcome == "already installed" {
				if adds.Load() != 0 || !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("installed reuse created staging or sent an add: adds=%d staging=%v", adds.Load(), statErr)
				}
			} else if adds.Load() != 1 || statErr != nil {
				t.Fatalf("new add lost staging: adds=%d staging=%v", adds.Load(), statErr)
			}
			if payload, err := os.ReadFile(retained); err != nil || string(payload) != "existing data" {
				t.Fatalf("existing client data changed: %v", err)
			}
		})
	}
}
