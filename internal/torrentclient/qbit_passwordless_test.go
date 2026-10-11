// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInjectQbitPasswordless(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		user      string
		password  string
		status    int
		body      string
		addStatus int
		wantErr   bool
	}{
		{"auth bypass", "", "", http.StatusOK, "Ok.", http.StatusOK, false},
		{"empty password", "user", "", http.StatusOK, "Ok.", http.StatusOK, false},
		{"password only", "", "pass", http.StatusOK, "Ok.", http.StatusOK, false},
		{"authenticated", "user", "pass", http.StatusOK, "Ok.", http.StatusOK, false},
		{"login rejected", "user", "", http.StatusOK, "Fails.", http.StatusOK, true},
		{"login forbidden", "user", "", http.StatusForbidden, "Forbidden", http.StatusOK, true},
		{"transient response", "user", "", http.StatusOK, "Try again.", http.StatusOK, true},
		{"auth required", "", "", http.StatusOK, "Ok.", http.StatusForbidden, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			loginCalls, addCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					mu.Lock()
					loginCalls++
					mu.Unlock()
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse login form: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if r.Form.Get("username") != tt.user || r.Form.Get("password") != tt.password {
						t.Error("login did not preserve configured credentials")
					}
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session"})
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
				case "/api/v2/torrents/add":
					mu.Lock()
					addCalls++
					mu.Unlock()
					w.WriteHeader(tt.addStatus)
					_, _ = w.Write([]byte("Ok."))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)

			torrentPath := filepath.Join(t.TempDir(), "sample.torrent")
			if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
				t.Fatalf("write torrent: %v", err)
			}
			svc := NewService(config.Config{
				TorrentClients: map[string]config.TorrentClientConfig{
					"local": {
						QbitURL:  server.URL,
						QbitUser: tt.user,
						QbitPass: tt.password,
					},
				},
			}, nil)
			err := svc.Inject(context.Background(), api.ClientSubject{SourcePath: "Example.Release.2026.mkv"}, api.TorrentResult{Path: torrentPath})
			if (err != nil) != tt.wantErr {
				t.Fatalf("inject error = %v, want error = %t", err, tt.wantErr)
			}
			if tt.wantErr && tt.addStatus == http.StatusOK && !strings.Contains(err.Error(), "qbit login") {
				t.Fatalf("expected server login rejection, got %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			wantLogin := tt.user != "" || tt.password != ""
			if (loginCalls > 0) != wantLogin {
				t.Fatalf("login calls = %d, want login = %t", loginCalls, wantLogin)
			}
			wantAdds := 1
			if tt.wantErr && tt.addStatus == http.StatusOK {
				wantAdds = 0
			}
			if (tt.addStatus == http.StatusOK && addCalls != wantAdds) || (tt.addStatus != http.StatusOK && addCalls == 0) {
				t.Fatalf("add calls = %d, want %d", addCalls, wantAdds)
			}
		})
	}
}

func TestPasswordlessQbitPreservesTLSVerification(t *testing.T) {
	t.Parallel()
	if qbitInjectClientConfig("https://localhost:8080", "", "", config.TorrentClientConfig{}).TLSSkipVerify {
		t.Fatal("empty credentials must retain default TLS verification")
	}
	for _, skipVerify := range []bool{false, true} {
		client := qbitInjectClientConfig("https://localhost:8080", "", "", config.TorrentClientConfig{TLSSkipVerify: &skipVerify})
		if client.TLSSkipVerify != skipVerify {
			t.Fatal("empty credentials changed TLS verification")
		}
	}
}

func TestImportedPasswordlessQbitSelection(t *testing.T) {
	t.Parallel()
	cfg, _, err := importer.ImportFromContent("config.py", []byte(`config = {
    'TORRENT_CLIENTS': {
        'primary': {'torrent_client': 'qbit', 'qbit_url': 'http://localhost:8080', 'qbit_user': 'user', 'qbit_pass': 'pass'},
        'local': {'torrent_client': 'qbit', 'qbit_url': 'http://localhost:8081'},
    },
}`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(cfg.TorrentClients) != 2 {
		t.Fatalf("expected both clients to survive import, got %d", len(cfg.TorrentClients))
	}
	if got := resolveInjectClients(*cfg, api.ClientOverrides{}, false); len(got) != 0 {
		t.Fatal("multiple clients must not trigger implicit single-client injection")
	}
	if names, _ := resolveSearchClients(*cfg, api.ClientOverrides{}); len(names) != 2 {
		t.Fatalf("implicit search should retain both imported clients, got %v", names)
	}
	if got := withURLCapableInjectFallback(nil, cfg.TorrentClients); len(got) != 2 {
		t.Fatal("URL fallback should retain both eligible imported clients")
	}
	cfg.ClientSetup.InjectClients = []string{"local"}
	got := resolveInjectClients(*cfg, api.ClientOverrides{}, false)
	if len(got) != 1 {
		t.Fatalf("explicit selection got %d clients", len(got))
	}
	if _, ok := got["local"]; !ok {
		t.Fatal("explicit selector did not retain the passwordless client")
	}
	if selected := withURLCapableInjectFallback(got, cfg.TorrentClients); len(selected) != 1 {
		t.Fatal("explicit qBittorrent selection must suppress URL fallback fanout")
	}
	cfg.ClientSetup.SearchClients = []string{"local"}
	if names, _ := resolveSearchClients(*cfg, api.ClientOverrides{}); len(names) != 1 || names[0] != "local" {
		t.Fatalf("explicit search should select the passwordless client, got %v", names)
	}
	cfg.ClientSetup.InjectClients = []string{"none"}
	disabled := resolveInjectClients(*cfg, api.ClientOverrides{}, false)
	if selected := withURLCapableInjectFallback(disabled, cfg.TorrentClients); !hasDisabledTorrentClient(selected) {
		t.Fatal("none must still suppress URL fallback")
	}
	delete(cfg.TorrentClients, "primary")
	cfg.ClientSetup = config.ClientSetupConfig{}
	if selected := resolveInjectClients(*cfg, api.ClientOverrides{}, false); len(selected) != 1 {
		t.Fatal("single passwordless client must retain implicit injection selection")
	}
}
