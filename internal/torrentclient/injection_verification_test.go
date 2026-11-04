// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
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
