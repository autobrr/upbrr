// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestConnectionUsesDraftAndTrustedSecretStorageWithoutPersistence(t *testing.T) {
	t.Parallel()
	repo, repoPath := openBackendConfigTestRepo(t, "connection.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "admin", "synthetic-test-password-846"); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "synthetic-preserved-key"
		if requests.Add(1) == 2 {
			expected = "synthetic-edited-key"
		}
		if r.URL.Path != "/draft/api/v3/system/status" || r.Header.Get("X-Api-Key") != expected {
			t.Error("draft values or preserved secret not used")
		}
		_, _ = io.WriteString(w, `{"appName":"Radarr","version":"6.0.0"}`)
	}))
	defer server.Close()
	initial := backendConfigTestConfig(repoPath)
	initial.ArrIntegration.RadarrURL = "http://saved.invalid"
	initial.ArrIntegration.RadarrAPIKey = "synthetic-preserved-key"
	if err := config.SaveToDatabase(t.Context(), &initial, repo); err != nil {
		t.Fatal(err)
	}
	var before map[string]json.RawMessage
	if err := repo.LoadFullConfig(t.Context(), &before); err != nil {
		t.Fatal(err)
	}
	authBefore, err := os.ReadFile(authmaterial.AuthFilePath(repoPath))
	if err != nil {
		t.Fatal(err)
	}
	backend := &Backend{repo: repo, cfg: initial}
	draft := initial
	draft.ArrIntegration.RadarrURL = server.URL + "/draft"
	draft.Metadata.KeepImages = !draft.Metadata.KeepImages
	payload, err := config.ExportToJSON(&draft)
	if err != nil {
		t.Fatal(err)
	}
	transported, err := config.ImportFromJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	transported.MainSettings.DBPath = filepath.Join(t.TempDir(), "must-not-create", "draft.db")
	transported.ArrIntegration.SonarrAPIKey1 = "upbrr-enc:v1:synthetic-malformed-envelope"
	payloadBytes, err := json.Marshal(transported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payloadBytes), "synthetic-preserved-key") {
		t.Fatal("fixture did not preserve an encrypted envelope")
	}
	got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "radarr", Instance: "0"}, Payload: string(payloadBytes)})
	if got.Code != "success" {
		t.Fatalf("draft outcome: %s", got.Code)
	}
	// Edited plaintext credentials replace the preserved encrypted value.
	transported.ArrIntegration.RadarrAPIKey = "synthetic-edited-key"
	payloadBytes, err = json.Marshal(transported)
	if err != nil {
		t.Fatal(err)
	}
	got = backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "radarr", Instance: "0"}, Payload: string(payloadBytes)})
	if got.Code != "success" || requests.Load() != 2 {
		t.Fatalf("edited credentials outcome: %s", got.Code)
	}
	var after map[string]json.RawMessage
	if err := repo.LoadFullConfig(t.Context(), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(backend.currentConfig(), initial) || backend.activator != nil || backend.coreOwner != nil || backend.runtimeGeneration != 0 {
		t.Fatal("connection test saved or activated the draft or initialized a runtime")
	}
	authAfter, err := os.ReadFile(authmaterial.AuthFilePath(repoPath))
	if err != nil || string(authAfter) != string(authBefore) {
		t.Fatal("connection test changed authentication storage")
	}
	if _, err := os.Stat(filepath.Dir(transported.MainSettings.DBPath)); !os.IsNotExist(err) {
		t.Fatal("draft database path was accessed or created")
	}
}

func TestConnectionRejectsOverlapAndKeepsInstancesIndependent(t *testing.T) {
	t.Parallel()
	repo, _ := openBackendConfigTestRepo(t, "concurrent.db")
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"appName":"Sonarr","version":"4.0.0"}`)
	}))
	defer server.Close()
	cfg := config.Config{ArrIntegration: config.ArrIntegrationConfig{SonarrURL: server.URL, SonarrAPIKey: "synthetic-key"}}
	payload, err := config.ExportToPlaintextJSON(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	backend := &Backend{repo: repo}
	req := api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "sonarr", Instance: "0"}, Payload: payload}
	completed := make(chan api.ConnectionTestResult, 1)
	go func() { completed <- backend.TestConnection(t.Context(), "session", req) }()
	<-started
	got := backend.TestConnection(t.Context(), "session", req)
	if got.Code != "in_progress" {
		t.Fatalf("overlapping outcome: %s", got.Code)
	}
	other := req
	other.Target.Instance = "1"
	got = backend.TestConnection(t.Context(), "session", other)
	if got.Code != "missing_configuration" {
		t.Fatalf("independent instance outcome: %s", got.Code)
	}
	close(release)
	if got := <-completed; got.Code != "success" {
		t.Fatalf("original instance outcome: %s", got.Code)
	}
	backend.connectionTestMu.Lock()
	defer backend.connectionTestMu.Unlock()
	if len(backend.connectionTests) != 0 {
		t.Fatal("completed test retained in-flight state")
	}
}

func TestConnectionPreservesEffectiveTorrentClientTLSOptions(t *testing.T) {
	t.Parallel()
	repo, repoPath := openBackendConfigTestRepo(t, "tls.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "admin", "synthetic-tls-test-password"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/app/version" {
			t.Error("unexpected torrent-client operation")
		}
		_, _ = io.WriteString(w, "v5.0.0")
	}))
	defer server.Close()
	backend := &Backend{repo: repo}
	for _, tc := range []struct {
		name, code                                     string
		skip, verify, proxy, malformed, encryptedProxy bool
	}{
		{
			name:   "legacy skip wins",
			skip:   true,
			verify: true,
			code:   "success",
		},
		{
			name:   "legacy verification wins",
			skip:   false,
			verify: false,
			code:   "tls_error",
		},
		{
			name:   "proxy-only TLS route",
			skip:   true,
			verify: true,
			proxy:  true,
			code:   "success",
		},
		{
			name:      "proxy ignores obsolete direct credentials",
			skip:      true,
			verify:    true,
			proxy:     true,
			malformed: true,
			code:      "success",
		},
		{
			name:           "encrypted proxy ignores obsolete direct credentials",
			skip:           true,
			verify:         true,
			proxy:          true,
			malformed:      true,
			encryptedProxy: true,
			code:           "success",
		},
		{
			name:      "direct credentials remain validated",
			skip:      true,
			verify:    true,
			malformed: true,
			code:      "invalid_configuration",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := map[string]any{
				"Type":                   "qbit",
				"QbitURL":                server.URL,
				"TLSSkipVerify":          tc.skip,
				"VerifyWebUICertificate": tc.verify,
			}
			if tc.proxy {
				client["QuiProxyURL"] = server.URL
				client["QbitURL"] = "http://unused.invalid"
			}
			if tc.encryptedProxy {
				cfg := &config.Config{TorrentClients: map[string]config.TorrentClientConfig{
					"selected": {Type: "qbit", QuiProxyURL: server.URL},
				}}
				cfg.MainSettings.DBPath = repoPath
				encrypted, err := config.EncryptConfigSecrets(cfg)
				if err != nil {
					t.Fatal(err)
				}
				client["QuiProxyURL"] = encrypted.TorrentClients["selected"].QuiProxyURL
			}
			if tc.malformed {
				for _, field := range []string{"QbitUser", "QbitPass", "Username", "Password"} {
					client[field] = "upbrr-enc:v1:malformed-synthetic-envelope"
				}
			}
			payload, err := json.Marshal(map[string]any{"TorrentClients": map[string]any{"selected": client}})
			if err != nil {
				t.Fatal(err)
			}
			got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "torrent_client", Instance: "selected"}, Payload: string(payload)})
			if got.Code != tc.code {
				t.Fatalf("TLS outcome: %s", got.Code)
			}
		})
	}
}

func TestConnectionCancellationPrecedesSecretRestoration(t *testing.T) {
	t.Parallel()
	repo, repoPath := openBackendConfigTestRepo(t, "canceled-secret.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "admin", "synthetic-canceled-test-password"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	backend := &Backend{repo: repo}
	got := backend.TestConnection(ctx, "session", api.ConnectionTestRequest{
		Target:  api.ConnectionTestTarget{Kind: "tmdb"},
		Payload: `{"MainSettings":{"TMDBAPI":"upbrr-enc:v1:malformed-synthetic-envelope"}}`,
	})
	if got.Code != "canceled" {
		t.Fatalf("canceled secret restoration=%s", got.Code)
	}
}

func TestConnectionWithoutStoredSecretsBypassesBlockedStorage(t *testing.T) {
	t.Parallel()
	repo, _ := openBackendConfigTestRepo(t, "blocked-storage.db")
	for _, tc := range []struct {
		target api.ConnectionTestTarget
		code   string
	}{
		{target: api.ConnectionTestTarget{Kind: "tmdb"}, code: "missing_configuration"},
		{target: api.ConnectionTestTarget{Kind: "tracker", Instance: "ASC"}, code: "unverified"},
		{target: api.ConnectionTestTarget{Kind: "image_host", Instance: "imgbb"}, code: "unsupported"},
	} {
		synctest.Test(t, func(t *testing.T) {
			slot := make(chan struct{}, 1)
			slot <- struct{}{}
			backend := &Backend{repo: repo, connectionStorageReads: slot}
			got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: tc.target, Payload: `{}`})
			if got.Code != tc.code {
				t.Fatalf("no-storage %s outcome=%s want=%s", tc.target.Kind, got.Code, tc.code)
			}
		})
	}
}

func TestConnectionPlaintextDraftBypassesBlockedStorage(t *testing.T) {
	t.Parallel()
	repo, _ := openBackendConfigTestRepo(t, "plaintext-blocked-storage.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/version" {
			_, _ = io.WriteString(w, "v5.0.0")
			return
		}
		if r.URL.Path != "/api/v3/system/status" || r.Header.Get("X-Api-Key") != "synthetic-plaintext-key" {
			t.Error("unexpected plaintext draft request")
		}
		_, _ = io.WriteString(w, `{"appName":"Sonarr","version":"4.0.0"}`)
	}))
	defer server.Close()
	slot := make(chan struct{}, 1)
	slot <- struct{}{}
	backend := &Backend{repo: repo, connectionStorageReads: slot}
	for _, tc := range []struct {
		target api.ConnectionTestTarget
		draft  any
	}{
		{
			target: api.ConnectionTestTarget{Kind: "sonarr", Instance: "0"},
			draft: config.Config{ArrIntegration: config.ArrIntegrationConfig{
				SonarrURL: server.URL, SonarrAPIKey: "synthetic-plaintext-key",
			}},
		},
		{
			target: api.ConnectionTestTarget{Kind: "torrent_client", Instance: "proxy"},
			draft: map[string]any{"TorrentClients": map[string]any{"proxy": map[string]any{
				"QuiProxyURL": server.URL,
				"Username":    "upbrr-enc:v1:unused",
				"Password":    "upbrr-enc:v1:unused",
				"QbitUser":    "upbrr-enc:v1:unused",
				"QbitPass":    "upbrr-enc:v1:unused",
			}}},
		},
	} {
		payload, err := json.Marshal(tc.draft)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		got := backend.TestConnection(ctx, "session", api.ConnectionTestRequest{Target: tc.target, Payload: string(payload)})
		cancel()
		if got.Code != "success" {
			t.Fatalf("plaintext %s blocked by unrelated storage: %s", tc.target.Kind, got.Code)
		}
	}
}

func TestConnectionStorageCancellationRetainsAdmissionUntilReadFinishes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		slot := make(chan struct{}, 1)
		started, release := make(chan struct{}), make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		completed := make(chan error, 1)
		reads := 0
		go func() {
			_, err := readConnectionStorage(ctx, slot, func() (string, error) {
				reads++
				close(started)
				<-release
				return "late-synthetic-secret", nil
			})
			completed <- err
		}()
		<-started
		cancel()
		if err := <-completed; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled storage wait=%v", err)
		}

		second, cancelSecond := context.WithTimeout(context.Background(), time.Second)
		defer cancelSecond()
		_, err := readConnectionStorage(second, slot, func() (string, error) {
			reads++
			return "must-not-start", nil
		})
		if !errors.Is(err, context.DeadlineExceeded) || reads != 1 {
			t.Fatalf("blocked admission=%v reads=%d", err, reads)
		}
		close(release)
		released = true
		synctest.Wait()
		value, err := readConnectionStorage(context.Background(), slot, func() (string, error) {
			reads++
			return "fresh-synthetic-secret", nil
		})
		if err != nil || value != "fresh-synthetic-secret" || reads != 2 {
			t.Fatalf("released admission=%v reads=%d", err, reads)
		}
	})
}

func TestConnectionRoutePreservesSettingsAuthorization(t *testing.T) {
	t.Parallel()
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))
	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)
	for _, tc := range []struct {
		name, method, origin, csrf, body string
		cookie                           bool
		status                           int
	}{
		{
			name:   "anonymous",
			method: http.MethodPost,
			status: http.StatusUnauthorized,
		},
		{
			name:   "missing CSRF",
			method: http.MethodPost,
			cookie: true,
			status: http.StatusForbidden,
		},
		{
			name:   "foreign origin",
			method: http.MethodPost,
			cookie: true,
			csrf:   current.CSRFToken,
			origin: "https://foreign.invalid",
			status: http.StatusForbidden,
		},
		{
			name:   "GET",
			method: http.MethodGet,
			cookie: true,
			status: http.StatusMethodNotAllowed,
		},
		{
			name:   "malformed",
			method: http.MethodPost,
			cookie: true,
			csrf:   current.CSRFToken,
			origin: "http://localhost:7480",
			body:   `{"payload":"synthetic-secret`,
			status: http.StatusBadRequest,
		},
		{
			name:   "authorized",
			method: http.MethodPost,
			cookie: true,
			csrf:   current.CSRFToken,
			origin: "http://localhost:7480",
			body:   `{"target":{"kind":"tmdb"},"payload":"{}"}`,
			status: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, "http://localhost:7480/api/app/TestConnection", strings.NewReader(tc.body))
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
			}
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Csrf-Token", tc.csrf)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "synthetic-secret") {
				t.Fatalf("route outcome: %d", w.Code)
			}
		})
	}
}

func TestConnectionTrackerSelectionPreservesCanonicalPrecedence(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"BLU", " BLU "} {
		entries := map[string]config.TrackerConfig{name: {}, "blu": {APIKey: "upbrr-enc:v1:must-not-decrypt"}}
		repo, _ := openBackendConfigTestRepo(t, "tracker-selection.db")
		cfg := config.Config{Trackers: config.TrackersConfig{Trackers: entries}}
		payload, err := config.ExportToPlaintextJSON(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		backend := &Backend{repo: repo}
		got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "tracker", Instance: " blu "}, Payload: payload})
		if got.Code != "missing_configuration" {
			t.Fatalf("selected wrong tracker credentials: %#v", got)
		}
	}
}

func TestConnectionRestoresOnlySelectedZiplineSecret(t *testing.T) {
	t.Parallel()
	repo, path := openBackendConfigTestRepo(t, "zipline.db")
	if err := authmaterial.BootstrapAuthFile(path, "admin", "synthetic-password-846"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/paged" || r.Header.Get("Authorization") != "synthetic-preserved-zipline-key" {
			t.Error("draft endpoint or preserved key not used")
		}
		_, _ = io.WriteString(w, `{"count":0}`)
	}))
	defer server.Close()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: path}, ImageHosting: config.ImageHostingConfig{ZiplineURL: server.URL + "/api/upload", ZiplineAPIKey: "synthetic-preserved-zipline-key"}}
	payload, err := config.ExportToJSON(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := config.ImportFromJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	draft.ArrIntegration.SonarrAPIKey = "upbrr-enc:v1:unrelated-corrupt-envelope"
	draft.MainSettings.DBPath = filepath.Join(t.TempDir(), "must-not-create", "draft.db")
	bytes, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	backend := &Backend{repo: repo}
	got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "image_host", Instance: " Zipline "}, Payload: string(bytes)})
	if got.Code != "success" {
		t.Fatalf("draft Zipline result: %#v", got)
	}
	if _, err := os.Stat(filepath.Dir(draft.MainSettings.DBPath)); !os.IsNotExist(err) {
		t.Fatal("draft path was accessed")
	}
}

func TestDraftLoginDoesNotReadUnrelatedStoredSession(t *testing.T) {
	t.Parallel()
	repo, repoPath := openBackendConfigTestRepo(t, "draft-login.db")
	backend := &Backend{repo: repo, cfg: backendConfigTestConfig(repoPath)}
	if err := repo.RawDB().Close(); err != nil {
		t.Fatal(err)
	}
	draft := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"AR": {Username: "synthetic-user"}}}}
	payload, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "tracker", Instance: "AR"}, Payload: string(payload)})
	if got.Code != "missing_configuration" {
		t.Fatalf("partial draft credentials depended on stored-cookie access: %#v", got)
	}
}

func TestTrackerProbeIgnoresSecretsFromUnusedAuthenticationModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, field, want string }{
		{"PTP", "Password", "missing_configuration"},
		{"TL", "Passkey", "missing_configuration"},
		{"AR", "Passkey", "missing_configuration"},
		{"RTF", "Password", "unsupported"},
		{"CZT", "Password", "missing_configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := openBackendConfigTestRepo(t, "unused-tracker-secret.db")
			payload, err := json.Marshal(map[string]any{"Trackers": map[string]any{"Trackers": map[string]any{tc.name: map[string]any{tc.field: "upbrr-enc:v1:synthetic-invalid-unused-envelope"}}}})
			if err != nil {
				t.Fatal(err)
			}
			backend := &Backend{repo: repo}
			got := backend.TestConnection(t.Context(), "session", api.ConnectionTestRequest{Target: api.ConnectionTestTarget{Kind: "tracker", Instance: tc.name}, Payload: string(payload)})
			if got.Code != tc.want {
				t.Fatalf("unused auth secret blocked selected probe: %#v", got)
			}
		})
	}
}
