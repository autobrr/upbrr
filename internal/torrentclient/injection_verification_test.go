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
	for _, outcome := range []string{"already installed", "fresh add", "unconfirmed add", "expired session", "refresh login failed"} {
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
			var logins atomic.Int32
			var sessionFresh atomic.Bool
			var addSavePath atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					if logins.Add(1) > 1 && outcome == "refresh login failed" {
						_, _ = w.Write([]byte("Fails."))
						return
					}
					sessionFresh.Store(true)
					http.SetCookie(w, &http.Cookie{
						Name:  "SID",
						Value: "fixture-session",
						Path:  "/",
					})
					_, _ = w.Write([]byte("Ok."))
				case "/api/v2/torrents/info":
					switch {
					case adds.Load() > 0 && outcome == "unconfirmed add":
						w.WriteHeader(http.StatusServiceUnavailable)
					case adds.Load() > 0 || outcome == "already installed":
						_ = json.NewEncoder(w).Encode([]map[string]string{{"hash": meta.HashInfoBytes().HexString()}})
					default:
						sessionFresh.Store(false)
						_, _ = w.Write([]byte("[]"))
					}
				case "/api/v2/torrents/add":
					if (outcome == "expired session" || outcome == "refresh login failed") && !sessionFresh.Load() {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Errorf("parse add form: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					addSavePath.Store(r.FormValue("savepath"))
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
					Username:     "fixture",
					Password:     "fixture-password",
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
			switch {
			case outcome == "unconfirmed add":
				if !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
					t.Fatalf("unconfirmed injection = %v", err)
				}
			case outcome == "refresh login failed":
				if err == nil || errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
					t.Fatalf("refresh login failure = %v", err)
				}
			case err != nil:
				t.Fatal(err)
			}
			linked := filepath.Join(linkRoot, "alpha", filepath.Base(source))
			_, statErr := os.Stat(linked)
			switch outcome {
			case "already installed":
				if adds.Load() != 0 || !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("installed reuse created staging or sent an add: adds=%d staging=%v", adds.Load(), statErr)
				}
			case "refresh login failed":
				if adds.Load() != 0 || statErr != nil || logins.Load() != 2 {
					t.Fatalf("failed refresh changed staging or sent an add: logins=%d adds=%d staging=%v", logins.Load(), adds.Load(), statErr)
				}
			default:
				if adds.Load() != 1 || statErr != nil {
					t.Fatalf("new add lost staging: adds=%d staging=%v", adds.Load(), statErr)
				}
			}
			if outcome == "fresh add" || outcome == "expired session" {
				gotSavePath, _ := addSavePath.Load().(string)
				wantSavePath := filepath.ToSlash(filepath.Join(linkRoot, "alpha")) + "/"
				if gotSavePath != wantSavePath {
					t.Fatalf("fresh add savepath = %q, want %q", gotSavePath, wantSavePath)
				}
			}
			if payload, err := os.ReadFile(retained); err != nil || string(payload) != "existing data" {
				t.Fatalf("existing client data changed: %v", err)
			}
		})
	}
}
