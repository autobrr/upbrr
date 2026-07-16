// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func testSessionManager() *sessionManager {
	return &sessionManager{
		ttl:      time.Hour,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		sessions: map[string]session{},
	}
}

func testServerWithBackend(t *testing.T, repo *db.SQLiteRepository, cfg config.Config) *Server {
	t.Helper()
	manager := testSessionManager()
	manager.sessions["test-session"] = session{
		ID:        "test-session",
		Username:  "tester",
		CSRFToken: "test-csrf",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	return &Server{
		backend:        &Backend{repo: repo, cfg: cfg},
		sessions:       manager,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
	}
}

func openBrowseTestRepo(t *testing.T) (*db.SQLiteRepository, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	repo, err := db.OpenWithLogger(dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}
	return repo, dbPath
}

func setTestBrowsePolicy(t *testing.T, server *Server, dbPath string, root string, allowUnrestricted bool) {
	t.Helper()
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("new auth store: %v", err)
	}
	if err := store.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatalf("load auth: %v", err)
	}
	record.BrowseRoot = root
	record.AllowUnrestrictedBrowse = allowUnrestricted
	if err := store.UpdateRecord(record); err != nil {
		t.Fatalf("update auth: %v", err)
	}
	server.auth = store
}

func canonicalBrowseTestPath(t *testing.T, path string) string {
	t.Helper()
	if strings.TrimSpace(path) == "" {
		return path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize %q: %v", path, err)
	}
	return resolved
}

func newBrowseRequest() *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/BrowseDirectory", strings.NewReader(`{}`))
	req.Host = "example.com:8080"
	req.RemoteAddr = "192.168.1.25:5050"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com:8080")
	req.Header.Set("X-Csrf-Token", "test-csrf")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	return req
}

func TestIsLoopbackHostname(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "sub.localhost", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "192.168.1.20", want: false},
		{host: "example.com", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isLoopbackHostname(tc.host); got != tc.want {
				t.Fatalf("isLoopbackHostname(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestBrowseDirectoryRouteAllowsRemoteSessionsAndSortsEntries(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	root := canonicalBrowseTestPath(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(root, "b-folder"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c-video.mkv"), []byte("video"), 0o600); err != nil {
		t.Fatalf("write video: %v", err)
	}
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(root) + `,"mode":"file"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse directory returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browse directory: %v", err)
	}
	if len(payload.Entries) < 2 {
		t.Fatalf("expected at least two entries, got %#v", payload.Entries)
	}
	if payload.Entries[0].Name != "b-folder" || !payload.Entries[0].IsDir {
		t.Fatalf("expected folder first, got %#v", payload.Entries)
	}
	if payload.Entries[1].Name != "c-video.mkv" || payload.Entries[1].IsDir {
		t.Fatalf("expected file second, got %#v", payload.Entries)
	}
	for _, entry := range payload.Entries {
		if entry.Name == "a-file.txt" {
			t.Fatalf("expected non-video file to be hidden, got %#v", payload.Entries)
		}
	}
}

func TestBrowseDirectoryRouteRequiresWebBrowsePolicy(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	root := t.TempDir()
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("new auth store: %v", err)
	}
	if err := store.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	server.auth = store
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(root) + `,"mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected missing browse policy to return 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "web browse root is not configured") {
		t.Fatalf("expected browse policy error, got %s", recorder.Body.String())
	}
}

func TestMenuImportPathsWithinBrowsePolicyRejectsOutsideRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "menu.png")
	if err := os.WriteFile(outside, []byte("png"), 0o600); err != nil {
		t.Fatalf("write outside image: %v", err)
	}

	_, err := menuImportPathsWithinBrowsePolicy([]string{outside}, webBrowsePolicy{Roots: []string{root}})
	if err == nil {
		t.Fatal("expected outside browse root error")
	}
	if !strings.Contains(err.Error(), "outside configured web browse roots") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMenuImportPathsWithinBrowsePolicyAllowsInsideRoot(t *testing.T) {
	t.Parallel()

	root := canonicalBrowseTestPath(t, t.TempDir())
	inside := filepath.Join(root, "menu.png")
	if err := os.WriteFile(inside, []byte("png"), 0o600); err != nil {
		t.Fatalf("write inside image: %v", err)
	}

	paths, err := menuImportPathsWithinBrowsePolicy([]string{inside}, webBrowsePolicy{Roots: []string{root}})
	if err != nil {
		t.Fatalf("menuImportPathsWithinBrowsePolicy: %v", err)
	}
	if len(paths) != 1 || filepath.Clean(paths[0]) != filepath.Clean(inside) {
		t.Fatalf("unexpected import paths: %#v", paths)
	}
}

func TestMenuImportPathsWithinBrowsePolicyAdditionalScenarios(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		setup     func(t *testing.T) ([]string, webBrowsePolicy, []string)
		wantErr   string
		exactPath bool
	}

	toWindowsPath := func(t *testing.T, path string) string {
		t.Helper()
		if runtime.GOOS != "windows" {
			t.Skip("Windows-style local paths are only valid on Windows")
		}
		return strings.ReplaceAll(filepath.ToSlash(path), "/", `\`)
	}

	tests := []testCase{
		{
			name: "directory import posix path",
			setup: func(t *testing.T) ([]string, webBrowsePolicy, []string) {
				t.Helper()
				root := canonicalBrowseTestPath(t, t.TempDir())
				dir := filepath.Join(root, "menus")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatalf("mkdir menu dir: %v", err)
				}
				first := filepath.Join(dir, "one.png")
				second := filepath.Join(dir, "two.jpg")
				subdir := filepath.Join(dir, "nested")
				if err := os.WriteFile(first, []byte("png"), 0o600); err != nil {
					t.Fatalf("write first image: %v", err)
				}
				if err := os.WriteFile(second, []byte("jpg"), 0o600); err != nil {
					t.Fatalf("write second image: %v", err)
				}
				if err := os.Mkdir(subdir, 0o755); err != nil {
					t.Fatalf("mkdir nested dir: %v", err)
				}
				return []string{filepath.ToSlash(dir)}, webBrowsePolicy{Roots: []string{root}}, []string{first, second}
			},
		},
		{
			name: "directory import windows path",
			setup: func(t *testing.T) ([]string, webBrowsePolicy, []string) {
				t.Helper()
				root := canonicalBrowseTestPath(t, t.TempDir())
				dir := filepath.Join(root, "menus")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatalf("mkdir menu dir: %v", err)
				}
				image := filepath.Join(dir, "one.png")
				if err := os.WriteFile(image, []byte("png"), 0o600); err != nil {
					t.Fatalf("write image: %v", err)
				}
				return []string{toWindowsPath(t, dir)}, webBrowsePolicy{Roots: []string{root}}, []string{image}
			},
		},
		{
			name: "symlink inside root points outside",
			setup: func(t *testing.T) ([]string, webBrowsePolicy, []string) {
				t.Helper()
				root := canonicalBrowseTestPath(t, t.TempDir())
				outside := filepath.Join(t.TempDir(), "menu.png")
				if err := os.WriteFile(outside, []byte("png"), 0o600); err != nil {
					t.Fatalf("write outside image: %v", err)
				}
				link := filepath.Join(root, "linked.png")
				if err := os.Symlink(outside, link); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				return []string{link}, webBrowsePolicy{Roots: []string{root}}, nil
			},
			wantErr: "outside configured web browse roots",
		},
		{
			name: "allow unrestricted returns original paths",
			setup: func(t *testing.T) ([]string, webBrowsePolicy, []string) {
				t.Helper()
				path := filepath.Join(t.TempDir(), "missing.png")
				return []string{path}, webBrowsePolicy{AllowUnrestricted: true}, []string{path}
			},
			exactPath: true,
		},
		{
			name: "multiple roots accepts second root posix path",
			setup: func(t *testing.T) ([]string, webBrowsePolicy, []string) {
				t.Helper()
				firstRoot := canonicalBrowseTestPath(t, t.TempDir())
				secondRoot := canonicalBrowseTestPath(t, t.TempDir())
				image := filepath.Join(secondRoot, "menu.png")
				if err := os.WriteFile(image, []byte("png"), 0o600); err != nil {
					t.Fatalf("write image: %v", err)
				}
				return []string{filepath.ToSlash(image)}, webBrowsePolicy{Roots: []string{firstRoot, secondRoot}}, []string{image}
			},
		},
		{
			name: "multiple roots accepts second root windows path",
			setup: func(t *testing.T) ([]string, webBrowsePolicy, []string) {
				t.Helper()
				firstRoot := canonicalBrowseTestPath(t, t.TempDir())
				secondRoot := canonicalBrowseTestPath(t, t.TempDir())
				image := filepath.Join(secondRoot, "menu.png")
				if err := os.WriteFile(image, []byte("png"), 0o600); err != nil {
					t.Fatalf("write image: %v", err)
				}
				return []string{toWindowsPath(t, image)}, webBrowsePolicy{Roots: []string{firstRoot, secondRoot}}, []string{image}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, policy, want := tt.setup(t)
			got, err := menuImportPathsWithinBrowsePolicy(paths, policy)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("menuImportPathsWithinBrowsePolicy: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("expected %d paths, got %#v", len(want), got)
			}
			for i := range want {
				if tt.exactPath {
					if got[i] != want[i] {
						t.Fatalf("expected path %q at index %d, got %q", want[i], i, got[i])
					}
					continue
				}
				if filepath.Clean(got[i]) != filepath.Clean(want[i]) {
					t.Fatalf("expected path %q at index %d, got %q", want[i], i, got[i])
				}
			}
		})
	}
}

func TestBrowseDirectoryRouteHonorsWebAuthBrowseRoot(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	root := canonicalBrowseTestPath(t, t.TempDir())
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(allowed, 0o755); err != nil {
		t.Fatalf("mkdir allowed: %v", err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, root, false)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":"","mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse root returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browse root: %v", err)
	}
	if payload.CurrentPath != root {
		t.Fatalf("expected constrained root %q, got %q", root, payload.CurrentPath)
	}
	if payload.ParentPath != "" {
		t.Fatalf("expected no parent above constrained root, got %q", payload.ParentPath)
	}

	req = newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(outside) + `,"mode":"folder"}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected outside browse root to return 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "outside configured web browse root") {
		t.Fatalf("expected browse root error, got %s", recorder.Body.String())
	}
}

func TestBrowseDirectoryRouteHonorsMultipleWebAuthBrowseRoots(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	first := filepath.Join(canonicalBrowseTestPath(t, t.TempDir()), "first")
	second := filepath.Join(canonicalBrowseTestPath(t, t.TempDir()), "second")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, dir := range []string{first, second, outside} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	first = canonicalBrowseTestPath(t, first)
	second = canonicalBrowseTestPath(t, second)

	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, first+", "+second, false)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":"","mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse roots returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browse roots: %v", err)
	}
	if payload.CurrentPath != "" || len(payload.Entries) != 2 {
		t.Fatalf("expected virtual root with two entries, got %#v", payload)
	}
	if payload.Entries[0].Path != first || payload.Entries[1].Path != second {
		t.Fatalf("expected both configured roots, got %#v", payload.Entries)
	}

	req = newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(second) + `,"mode":"folder"}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse second root returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal second root: %v", err)
	}
	if payload.CurrentPath != second || payload.ParentPath != "" {
		t.Fatalf("expected constrained second root, got %#v", payload)
	}

	req = newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(outside) + `,"mode":"folder"}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected outside browse roots to return 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestBrowsePolicyRootsRoundTripCommaPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media, 4k")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	encoded := joinBrowsePolicyRoots([]string{root})
	roots, err := normalizeBrowsePolicyRoots(splitBrowsePolicyRoots(encoded))
	if err != nil {
		t.Fatalf("normalize encoded root: %v", err)
	}
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("expected encoded comma root to round-trip, got %#v", roots)
	}

	roots, err = normalizeBrowsePolicyRoots(splitBrowsePolicyRoots(root))
	if err != nil {
		t.Fatalf("normalize raw comma root: %v", err)
	}
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("expected raw comma root to remain one root, got %#v", roots)
	}
}

func TestBrowseDirectoryRouteRejectsInvalidPath(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest()
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(filepath.Join(t.TempDir(), "missing")) + `,"mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid path to return 400, got %d", recorder.Code)
	}
}
