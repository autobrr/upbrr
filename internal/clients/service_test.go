// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type qbitAddCapture struct {
	errCh        chan error
	mu           sync.Mutex
	addCalls     int
	category     string
	savePath     string
	tags         string
	autoTMM      string
	skipChecking string
}

func newQbitAddCaptureServer(t *testing.T) (*httptest.Server, *qbitAddCapture) {
	t.Helper()

	capture := &qbitAddCapture{errCh: make(chan error, 1)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(10 << 20); err != nil {
					capture.errCh <- err
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			} else if err := r.ParseForm(); err != nil {
				capture.errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			capture.mu.Lock()
			capture.addCalls++
			capture.category = r.FormValue("category")
			capture.savePath = r.FormValue("savepath")
			capture.tags = r.FormValue("tags")
			capture.autoTMM = r.FormValue("autoTMM")
			capture.skipChecking = r.FormValue("skip_checking")
			capture.mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func TestInjectWatchFolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	watch := filepath.Join(dir, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	torrentPath := filepath.Join(dir, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	copied := filepath.Join(watch, filepath.Base(torrentPath))
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("expected copied torrent: %v", err)
	}
}

func TestInjectUnsupportedType(t *testing.T) {
	t.Parallel()

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"rtorrent": {Type: "rtorrent", URL: "http://localhost"},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: "/tmp/file.torrent"})
	if !errors.Is(err, internalerrors.ErrNotImplemented) {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestInjectQbitClient(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	loginCalled := false
	addCalled := false
	var addCategory string
	var addTags string
	var addSkipChecking string
	var addAutoTMM string
	var fileCount int
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			mu.Lock()
			loginCalled = true
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addCalled = true
			addCategory = r.FormValue("category")
			addTags = r.FormValue("tags")
			addSkipChecking = r.FormValue("skip_checking")
			addAutoTMM = r.FormValue("autoTMM")
			if files, ok := r.MultipartForm.File["torrents"]; ok {
				fileCount = len(files)
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	root := t.TempDir()
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
				Category: "ua",
				Tags:     []string{"tag1", "tag2"},
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if !loginCalled {
		t.Fatalf("expected qbit login call")
	}
	if !addCalled {
		t.Fatalf("expected qbit add call")
	}
	if addCategory != "ua" {
		t.Fatalf("expected category ua, got %q", addCategory)
	}
	if addTags != "tag1,tag2" {
		t.Fatalf("expected tags tag1,tag2, got %q", addTags)
	}
	if addSkipChecking != "true" {
		t.Fatalf("expected skip_checking true, got %q", addSkipChecking)
	}
	if addAutoTMM != "false" {
		t.Fatalf("expected autoTMM false, got %q", addAutoTMM)
	}
	if fileCount != 1 {
		t.Fatalf("expected 1 torrent file, got %d", fileCount)
	}
}

func TestInjectQbitClientRejectsCookieBearingNonOKLogin(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "transient-session"})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Try again."))
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
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
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "Example.Release.2026.mkv"}, api.TorrentResult{Path: torrentPath})
	if err == nil {
		t.Fatal("expected cookie-bearing non-Ok login rejection")
	}
	if !strings.Contains(err.Error(), "qbit login") {
		t.Fatal("expected qbit login error")
	}
	mu.Lock()
	defer mu.Unlock()
	if addCalls != 0 {
		t.Fatalf("expected no qbit add attempt, got %d", addCalls)
	}
}

func TestQbitInjectClientConfigCapsTimeoutAndRetries(t *testing.T) {
	t.Parallel()

	cfg := qbitInjectClientConfig("http://localhost:8080", "user", "pass", config.TorrentClientConfig{})
	if cfg.Timeout != int(qbitInjectHTTPTimeout/time.Second) {
		t.Fatalf("expected timeout %d, got %d", int(qbitInjectHTTPTimeout/time.Second), cfg.Timeout)
	}
	if cfg.RetryAttempts != qbitInjectHTTPRetryAttempts {
		t.Fatalf("expected retry attempts %d, got %d", qbitInjectHTTPRetryAttempts, cfg.RetryAttempts)
	}
}

func TestQbitAutomaticManagementEnabledUsesPathBoundaries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tests := []struct {
		name       string
		sourcePath string
		configured string
		want       bool
	}{
		{
			name:       "configured root",
			sourcePath: filepath.Join(root, "video.mkv"),
			configured: root,
			want:       true,
		},
		{
			name:       "configured root child",
			sourcePath: filepath.Join(root, "child", "video.mkv"),
			configured: root,
			want:       true,
		},
		{
			name:       "sibling prefix",
			sourcePath: filepath.Join(root+"-old", "video.mkv"),
			configured: root,
			want:       false,
		},
		{
			name:       "case variant directory",
			sourcePath: filepath.Join(root, "media", "video.mkv"),
			configured: filepath.Join(root, "Media"),
			want:       runtime.GOOS == "windows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meta := api.PreparedMetadata{SourcePath: tt.sourcePath}
			if got := qbitAutomaticManagementEnabled(meta, config.StringList{tt.configured}); got != tt.want {
				t.Fatalf("qbitAutomaticManagementEnabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestInjectQbitClientUsesPathMappingSavePath(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)

	root := t.TempDir()
	localRoot := filepath.Join(root, "local")
	releaseDir := filepath.Join(localRoot, "Movies", "Fixture.Title.2024")
	if err := os.MkdirAll(releaseDir, 0o700); err != nil {
		t.Fatalf("mkdir release: %v", err)
	}
	source := filepath.Join(releaseDir, "video.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	remoteRoot := "/remote/media"
	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:                     "qbit",
				URL:                      server.URL,
				Username:                 "user",
				Password:                 "pass",
				LocalPath:                config.StringList{"", localRoot},
				RemotePath:               config.StringList{"/wrong/root", remoteRoot},
				AutomaticManagementPaths: config.StringList{localRoot},
			},
		},
	}, nil)

	meta := api.PreparedMetadata{SourcePath: source, FileList: []string{source}}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	wantSavePath := "/remote/media/Movies/Fixture.Title.2024/"
	if capture.savePath != wantSavePath {
		t.Fatalf("expected mapped savepath %q, got %q", wantSavePath, capture.savePath)
	}
	if capture.autoTMM != "true" {
		t.Fatalf("expected autoTMM true, got %q", capture.autoTMM)
	}
}

func TestInjectQbitClientUsesPathMappingSavePathWithoutSourceStat(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)

	root := t.TempDir()
	localRoot := filepath.Join(root, "local")
	source := filepath.Join(localRoot, "Movies", "Fixture.Title.2024", "video.mkv")
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	remoteRoot := "/remote/media"
	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:       "qbit",
				URL:        server.URL,
				Username:   "user",
				Password:   "pass",
				LocalPath:  config.StringList{localRoot},
				RemotePath: config.StringList{remoteRoot},
			},
		},
	}, nil)

	meta := api.PreparedMetadata{SourcePath: source, FileList: []string{source}}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	wantSavePath := "/remote/media/Movies/Fixture.Title.2024/"
	if capture.savePath != wantSavePath {
		t.Fatalf("expected mapped savepath %q, got %q", wantSavePath, capture.savePath)
	}
}

func TestMappedRemotePathPreservesBlankPathPairAlignment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	localRoot := filepath.Join(root, "local")
	otherRoot := filepath.Join(root, "other")
	source := filepath.Join(localRoot, "Movies", "Fixture.Title.2024", "video.mkv")

	tests := []struct {
		name    string
		locals  config.StringList
		remotes config.StringList
	}{
		{
			name:    "blank local path",
			locals:  config.StringList{"", localRoot},
			remotes: config.StringList{"/wrong/root", "/remote/media"},
		},
		{
			name:    "blank remote path",
			locals:  config.StringList{otherRoot, localRoot},
			remotes: config.StringList{"", "/remote/media"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mappedRemotePath(source, tt.locals, tt.remotes)
			if !ok {
				t.Fatal("expected mapped path")
			}
			want := filepath.Join("/remote/media", "Movies", "Fixture.Title.2024", "video.mkv")
			if got != want {
				t.Fatalf("expected mapped path %q, got %q", want, got)
			}
		})
	}
}

func TestMappedRemotePathUsesMostSpecificRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	linkedRoot := filepath.Join(root, "cross-seed")
	source := filepath.Join(linkedRoot, "EXAMPLE", "Example.Release.2026.mkv")

	got, ok := mappedRemotePath(
		source,
		config.StringList{root, linkedRoot},
		config.StringList{"/remote/general", "/remote/cross-seed"},
	)
	if !ok {
		t.Fatal("expected mapped path")
	}
	want := filepath.Join("/remote/cross-seed", "EXAMPLE", "Example.Release.2026.mkv")
	if got != want {
		t.Fatalf("expected most-specific mapped path %q, got %q", want, got)
	}
}

func TestMappedRemotePathIsCaseInsensitiveOnWindows(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	root := t.TempDir()
	source := filepath.Join(root, "CrossSeed", "EXAMPLE", "Example.Release.2026.mkv")
	localRoot := strings.ToUpper(filepath.Join(root, "crossseed"))

	got, ok := mappedRemotePath(source, config.StringList{localRoot}, config.StringList{"/remote/cross-seed"})
	if !ok {
		t.Fatal("expected case-insensitive mapped path")
	}
	want := filepath.Join("/remote/cross-seed", "EXAMPLE", "Example.Release.2026.mkv")
	if got != want {
		t.Fatalf("expected mapped path %q, got %q", want, got)
	}
}

func TestInjectQbitClientUsesRequestOverrides(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var addCategory string
	var addTags string
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addCategory = r.FormValue("category")
			addTags = r.FormValue("tags")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	root := t.TempDir()
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
				Category: "config-cat",
				Tags:     []string{"config1", "config2"},
			},
		},
	}, nil)

	overrideCategory := "override-cat"
	overrideTag := "override-tag"
	meta := api.PreparedMetadata{
		SourcePath: "video.mkv",
		ClientOverrides: api.ClientOverrides{
			QbitCategory: &overrideCategory,
			QbitTag:      &overrideTag,
		},
	}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if addCategory != "override-cat" {
		t.Fatalf("expected override category, got %q", addCategory)
	}
	if addTags != "override-tag" {
		t.Fatalf("expected override tag, got %q", addTags)
	}
}

func TestInjectQbitClientIgnoresCrossFieldsForNormalTorrent(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:              "qbit",
				URL:               server.URL,
				Username:          "user",
				Password:          "pass",
				Category:          "config-cat",
				Tags:              []string{"config1", "config2"},
				QbitCrossCategory: "cross-cat",
				QbitCrossTag:      "cross-tag",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.category != "config-cat" {
		t.Fatalf("expected config category, got %q", capture.category)
	}
	if capture.tags != "config1,config2" {
		t.Fatalf("expected config tags, got %q", capture.tags)
	}
}

func TestInjectQbitClientUsesCrossFieldsForCrossSeedTorrent(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:              "qbit",
				URL:               server.URL,
				Username:          "user",
				Password:          "pass",
				Category:          "config-cat",
				Tags:              []string{"config1", "config2"},
				QbitCrossCategory: "cross-cat",
				QbitCrossTag:      "cross-tag",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER", CrossSeed: true}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.category != "cross-cat" {
		t.Fatalf("expected cross category, got %q", capture.category)
	}
	if capture.tags != "cross-tag" {
		t.Fatalf("expected cross tag, got %q", capture.tags)
	}
}

func newQbitAddFailureServer(t *testing.T) (*httptest.Server, *qbitAddCapture) {
	t.Helper()

	capture := &qbitAddCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(10 << 20); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			} else {
				if err := r.ParseForm(); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			capture.mu.Lock()
			capture.addCalls++
			capture.mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Fails."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func TestInjectQbitClientCleansStagingAfterLoginFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Fails."))
	}))
	t.Cleanup(server.Close)

	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"EXAMPLE": {LinkDirName: "example-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "EXAMPLE"})
	if err == nil {
		t.Fatal("expected qbit login failure")
	}

	stagingDir := filepath.Join(linkRoot, "example-links")
	if _, statErr := os.Stat(stagingDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected login failure to remove staging, stat err=%v", statErr)
	}
}

func TestInjectQbitClientCleansStagingAfterFileAddFailure(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddFailureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"EXAMPLE": {LinkDirName: "example-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "EXAMPLE"})
	if err == nil {
		t.Fatal("expected qbit add failure")
	}
	capture.mu.Lock()
	addCalls := capture.addCalls
	capture.mu.Unlock()
	if addCalls != 1 {
		t.Fatalf("expected one qbit add attempt, got %d", addCalls)
	}

	stagedPath := filepath.Join(linkRoot, "example-links", filepath.Base(source))
	if _, statErr := os.Stat(stagedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected staged file cleanup, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(linkRoot, "example-links")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected tracker staging dir cleanup, stat err=%v", statErr)
	}
}

func TestInjectQbitClientURLLinkingFallsBackWithoutStaging(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: "aither-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{URL: "https://tracker.example/torrent/1", Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject URL fallback: %v", err)
	}
	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	stagedPath := filepath.Join(linkRoot, "aither-links", filepath.Base(source))
	if _, statErr := os.Stat(stagedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected URL-only injection not to stage files, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(linkRoot, "aither-links")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected URL-only injection not to create tracker staging, stat err=%v", statErr)
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.addCalls != 1 {
		t.Fatalf("expected one qbit add attempt, got %d", capture.addCalls)
	}
	if capture.skipChecking != "true" {
		t.Fatalf("expected URL fallback to skip hash checking, got skip_checking=%q", capture.skipChecking)
	}
}

func TestInjectQbitClientInvalidLinkPlanFallbackSkipsHashCheck(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "invalid.torrent")
	if err := os.WriteFile(torrentPath, []byte("invalid metainfo"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{
		SourcePath: source,
		FileList:   []string{source},
	}, api.TorrentResult{Path: torrentPath, Tracker: "EXAMPLE"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.savePath != "" {
		t.Fatalf("expected original-path fallback, got savepath %q", capture.savePath)
	}
	if capture.skipChecking != "true" {
		t.Fatalf("expected fallback to skip hash checking, got skip_checking=%q", capture.skipChecking)
	}
}

func TestInjectQbitClientHardlinksSourceAndUsesLinkedSavePath(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var addSavePath string
	var addTags string
	var addAutoTMM string
	var addSkipChecking string
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addSavePath = r.FormValue("savepath")
			addTags = r.FormValue("tags")
			addAutoTMM = r.FormValue("autoTMM")
			addSkipChecking = r.FormValue("skip_checking")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	torrentRoot := "Tracker.Name.2026.mkv"
	writeQbitTestTorrent(t, torrentPath, torrentRoot, map[string]string{"source": source}, false)

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: "aither-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:                     "qbit",
				URL:                      server.URL,
				Username:                 "user",
				Password:                 "pass",
				Linking:                  "hardlink",
				LinkedFolder:             config.StringList{linkRoot},
				LocalPath:                config.StringList{root},
				RemotePath:               config.StringList{"/remote/root"},
				AutomaticManagementPaths: config.StringList{root},
				UseTrackerAsTag:          true,
			},
		},
	}, nil)

	meta := api.PreparedMetadata{SourcePath: source, FileList: []string{source}}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	linkedSource := filepath.Join(linkRoot, "aither-links", torrentRoot)
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}
	linkedInfo, err := os.Stat(linkedSource)
	if err != nil {
		t.Fatalf("stat linked source: %v", err)
	}
	if !os.SameFile(sourceInfo, linkedInfo) {
		t.Fatalf("expected linked file to share source file identity")
	}

	mu.Lock()
	defer mu.Unlock()
	wantSavePath := "/remote/root/links/aither-links/"
	if addSavePath != wantSavePath {
		t.Fatalf("expected savepath %q, got %q", wantSavePath, addSavePath)
	}
	if addTags != "AITHER" {
		t.Fatalf("expected tracker tag, got %q", addTags)
	}
	if addAutoTMM != "false" {
		t.Fatalf("expected linked injection autoTMM false, got %q", addAutoTMM)
	}
	if addSkipChecking != "true" {
		t.Fatalf("expected linked injection to skip hash checking, got skip_checking=%q", addSkipChecking)
	}
}

func TestInjectQbitClientRejectsUnmappedLinkedSavePath(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	if err := os.MkdirAll(mediaRoot, 0o700); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	source := filepath.Join(mediaRoot, "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "cross-seed")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"EXAMPLE": {LinkDirName: "example-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
				LocalPath:    config.StringList{mediaRoot},
				RemotePath:   config.StringList{"/remote/media"},
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "EXAMPLE"})
	if err == nil || !strings.Contains(err.Error(), "outside configured local_path roots") {
		t.Fatal("expected linked staging path mapping error")
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.addCalls != 0 {
		t.Fatal("expected mapping failure to prevent qBittorrent add")
	}
	if _, statErr := os.Stat(filepath.Join(linkRoot, "example-links")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("expected mapping failure not to create tracker staging")
	}
}

func TestInjectQbitClientRejectsUnsafeTrackerLinkDirName(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: filepath.Join("..", "escape")},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"})
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input for unsafe link dir name, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "escape")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no escaped tracker directory, stat err=%v", statErr)
	}
	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}
}

func TestInjectQbitClientRejectsStaleExistingHardlinkDest(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("fresh"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	trackerDir := filepath.Join(linkRoot, "aither-links")
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	dest := filepath.Join(trackerDir, filepath.Base(source))
	if err := os.WriteFile(dest, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale dest: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)
	allowFallback := false
	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: "aither-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:          "qbit",
				URL:           server.URL,
				Username:      "user",
				Password:      "pass",
				Linking:       "hardlink",
				LinkedFolder:  config.StringList{linkRoot},
				AllowFallback: &allowFallback,
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"})
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected stale link dest to fail with invalid input, got %v", err)
	}
	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}
}

func TestInjectQbitClientFailedLinkPlanFallbackSkipsHashCheck(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("fresh"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	trackerDir := filepath.Join(linkRoot, "example-links")
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	dest := filepath.Join(trackerDir, filepath.Base(source))
	if err := os.WriteFile(dest, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale dest: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)
	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"EXAMPLE": {LinkDirName: "example-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "EXAMPLE"}); err != nil {
		t.Fatalf("inject failed-link fallback: %v", err)
	}
	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.addCalls != 1 {
		t.Fatalf("expected one qbit add attempt, got %d", capture.addCalls)
	}
	if capture.savePath != "" {
		t.Fatalf("expected original-path fallback, got savepath %q", capture.savePath)
	}
	if capture.skipChecking != "true" {
		t.Fatalf("expected failed-link fallback to skip hash checking, got skip_checking=%q", capture.skipChecking)
	}
}

func TestInjectQbitClientAllowsMatchingExistingHardlinkDest(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	trackerDir := filepath.Join(linkRoot, "aither-links")
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	dest := filepath.Join(trackerDir, filepath.Base(source))
	if err := os.Link(source, dest); err != nil {
		t.Fatalf("precreate hardlink dest: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: "aither-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "hardlink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	wantSavePath := filepath.ToSlash(filepath.Join(linkRoot, "aither-links")) + "/"
	if capture.savePath != wantSavePath {
		t.Fatalf("expected savepath %q, got %q", wantSavePath, capture.savePath)
	}
}

func TestInjectQbitClientTriesLaterLinkedFolderAfterFirstFails(t *testing.T) {
	t.Parallel()

	server, capture := newQbitAddCaptureServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	firstLinkRoot := filepath.Join(root, "links-first")
	secondLinkRoot := filepath.Join(root, "links-second")
	firstTrackerDir := filepath.Join(firstLinkRoot, "aither-links")
	if err := os.MkdirAll(firstTrackerDir, 0o700); err != nil {
		t.Fatalf("mkdir first links: %v", err)
	}
	if err := os.MkdirAll(secondLinkRoot, 0o700); err != nil {
		t.Fatalf("mkdir second links: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstTrackerDir, filepath.Base(source)), []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale first dest: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)
	allowFallback := false

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: "aither-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:          "qbit",
				URL:           server.URL,
				Username:      "user",
				Password:      "pass",
				Linking:       "hardlink",
				LinkedFolder:  config.StringList{firstLinkRoot, secondLinkRoot},
				AllowFallback: &allowFallback,
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	select {
	case err := <-capture.errCh:
		t.Fatalf("handler: %v", err)
	default:
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	wantSavePath := filepath.ToSlash(filepath.Join(secondLinkRoot, "aither-links")) + "/"
	if capture.savePath != wantSavePath {
		t.Fatalf("expected second linked folder savepath %q, got %q", wantSavePath, capture.savePath)
	}
}

func TestLinkedFolderCandidatesWindowsSymlinkDoesNotRepeatSameVolumeFolders(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "windows" {
		t.Skip("windows-specific symlink ordering")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	firstLinkRoot := filepath.Join(root, "links-first")
	secondLinkRoot := filepath.Join(root, "links-second")
	if err := os.MkdirAll(firstLinkRoot, 0o700); err != nil {
		t.Fatalf("mkdir first links: %v", err)
	}
	if err := os.MkdirAll(secondLinkRoot, 0o700); err != nil {
		t.Fatalf("mkdir second links: %v", err)
	}

	got, err := linkedFolderCandidates(source, []string{firstLinkRoot, secondLinkRoot}, "symlink")
	if err != nil {
		t.Fatalf("linked folder candidates: %v", err)
	}

	wantFirst, err := absLocalPath("first linked folder", firstLinkRoot)
	if err != nil {
		t.Fatalf("first linked folder abs path: %v", err)
	}
	wantSecond, err := absLocalPath("second linked folder", secondLinkRoot)
	if err != nil {
		t.Fatalf("second linked folder abs path: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 unique candidates, got %d (%v)", len(got), got)
	}
	if got[0] != wantFirst || got[1] != wantSecond {
		t.Fatalf("expected ordered unique candidates [%q %q], got %v", wantFirst, wantSecond, got)
	}
}

func TestInjectQbitClientReflinksSourceAndUsesLinkedSavePath(t *testing.T) {
	var mu sync.Mutex
	var addSavePath string
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addSavePath = r.FormValue("savepath")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	root := t.TempDir()
	source := filepath.Join(root, "release")
	nested := filepath.Join(source, "disc")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	sourceFile := filepath.Join(nested, "movie.mkv")
	if err := os.WriteFile(sourceFile, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "reflinks")
	if err := os.MkdirAll(linkRoot, 0o700); err != nil {
		t.Fatalf("mkdir links: %v", err)
	}
	torrentPath := filepath.Join(root, "sample.torrent")
	writeQbitTestTorrentForSource(t, torrentPath, source)

	originalCreateReflink := createReflink
	var cloneCalls []string
	createReflink = func(src, dst string) error {
		cloneCalls = append(cloneCalls, src+"->"+dst)
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read reflink source: %w", err)
		}
		return os.WriteFile(dst, data, 0o600)
	}
	t.Cleanup(func() { createReflink = originalCreateReflink })

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AITHER": {LinkDirName: "aither-reflinks"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:         "qbit",
				URL:          server.URL,
				Username:     "user",
				Password:     "pass",
				Linking:      "reflink",
				LinkedFolder: config.StringList{linkRoot},
			},
		},
	}, nil)

	meta := api.PreparedMetadata{SourcePath: source}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	linkedFile := filepath.Join(linkRoot, "aither-reflinks", filepath.Base(source), "disc", filepath.Base(sourceFile))
	if _, err := os.Stat(linkedFile); err != nil {
		t.Fatalf("expected reflinked source file: %v", err)
	}
	if len(cloneCalls) != 1 {
		t.Fatalf("expected 1 reflink call, got %d (%v)", len(cloneCalls), cloneCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	wantSavePath := filepath.ToSlash(filepath.Join(linkRoot, "aither-reflinks")) + "/"
	if addSavePath != wantSavePath {
		t.Fatalf("expected savepath %q, got %q", wantSavePath, addSavePath)
	}
}

func TestInjectUsesSelectedClientOverride(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	firstWatch := filepath.Join(watchRoot, "first")
	secondWatch := filepath.Join(watchRoot, "second")
	if err := os.MkdirAll(firstWatch, 0o700); err != nil {
		t.Fatalf("mkdir first: %v", err)
	}
	if err := os.MkdirAll(secondWatch, 0o700); err != nil {
		t.Fatalf("mkdir second: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"first":  {Type: "watch", WatchFolder: firstWatch},
			"second": {Type: "watch", WatchFolder: secondWatch},
		},
	}, nil)

	selectedClient := "second"
	meta := api.PreparedMetadata{
		SourcePath: "video.mkv",
		ClientOverrides: api.ClientOverrides{
			Client: &selectedClient,
		},
	}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(firstWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected first client untouched, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected second client copy, got %v", err)
	}
}

func TestInjectUsesDefaultClientWhenNoInjectionListOrTrackerClient(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	defaultWatch := filepath.Join(watchRoot, "default")
	otherWatch := filepath.Join(watchRoot, "other")
	if err := os.MkdirAll(defaultWatch, 0o700); err != nil {
		t.Fatalf("mkdir default: %v", err)
	}
	if err := os.MkdirAll(otherWatch, 0o700); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "default",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"default": {Type: "watch", WatchFolder: defaultWatch},
			"other":   {Type: "watch", WatchFolder: otherWatch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(defaultWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected default client copy, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected other client untouched, got %v", err)
	}
}

func TestInjectUsesMultipleInjectionClients(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	firstWatch := filepath.Join(watchRoot, "first")
	secondWatch := filepath.Join(watchRoot, "second")
	thirdWatch := filepath.Join(watchRoot, "third")
	for _, dir := range []string{firstWatch, secondWatch, thirdWatch} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "first",
			InjectClients: config.CSVList{"second", "third"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"first":  {Type: "watch", WatchFolder: firstWatch},
			"second": {Type: "watch", WatchFolder: secondWatch},
			"third":  {Type: "watch", WatchFolder: thirdWatch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(firstWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected default client untouched, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected second client copy, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(thirdWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected third client copy, got %v", err)
	}
}

func TestInjectUsesSingleInjectionClientBeforeDefaultClient(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	defaultWatch := filepath.Join(watchRoot, "default")
	otherWatch := filepath.Join(watchRoot, "other")
	if err := os.MkdirAll(defaultWatch, 0o700); err != nil {
		t.Fatalf("mkdir default: %v", err)
	}
	if err := os.MkdirAll(otherWatch, 0o700); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "default",
			InjectClients: config.CSVList{"other"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"default": {Type: "watch", WatchFolder: defaultWatch},
			"other":   {Type: "watch", WatchFolder: otherWatch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(defaultWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected default client untouched, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected single injection client copy, got %v", err)
	}
}

func TestInjectSkipsDefaultClientWhenInjectionClientUnknown(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	defaultWatch := filepath.Join(watchRoot, "default")
	if err := os.MkdirAll(defaultWatch, 0o700); err != nil {
		t.Fatalf("mkdir default: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "default",
			InjectClients: config.CSVList{"missing"},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"default": {Type: "watch", WatchFolder: defaultWatch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(defaultWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected unknown injection selector to skip default fallback, got %v", err)
	}
}

func TestInjectSkipsImplicitSingleClientWhenDefaultClientUnknown(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir watch: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "missing",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(watch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected unknown default selector to skip implicit fallback, got %v", err)
	}
}

func TestInjectUnknownExplicitClientOverrideSkipsImplicitFallback(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir watch: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	missing := "missing"
	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
		},
	}, nil)

	meta := api.PreparedMetadata{
		SourcePath: "video.mkv",
		ClientOverrides: api.ClientOverrides{
			Client: &missing,
		},
	}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(watch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected explicit unknown override to skip fallback, got %v", err)
	}
}

func TestInjectUsesTrackerTorrentClient(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	firstWatch := filepath.Join(watchRoot, "first")
	secondWatch := filepath.Join(watchRoot, "second")
	if err := os.MkdirAll(firstWatch, 0o700); err != nil {
		t.Fatalf("mkdir first: %v", err)
	}
	if err := os.MkdirAll(secondWatch, 0o700); err != nil {
		t.Fatalf("mkdir second: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {TorrentClient: "second"},
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"first":  {Type: "watch", WatchFolder: firstWatch},
			"second": {Type: "watch", WatchFolder: secondWatch},
		},
	}, nil)

	meta := api.PreparedMetadata{SourcePath: "video.mkv"}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath, Tracker: "aither"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(firstWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected first client untouched, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected second client copy, got %v", err)
	}
}

func TestInjectClientOverrideWinsOverTrackerTorrentClient(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	firstWatch := filepath.Join(watchRoot, "first")
	secondWatch := filepath.Join(watchRoot, "second")
	if err := os.MkdirAll(firstWatch, 0o700); err != nil {
		t.Fatalf("mkdir first: %v", err)
	}
	if err := os.MkdirAll(secondWatch, 0o700); err != nil {
		t.Fatalf("mkdir second: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {TorrentClient: "second"},
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"first":  {Type: "watch", WatchFolder: firstWatch},
			"second": {Type: "watch", WatchFolder: secondWatch},
		},
	}, nil)

	selectedClient := "first"
	meta := api.PreparedMetadata{
		SourcePath: "video.mkv",
		ClientOverrides: api.ClientOverrides{
			Client: &selectedClient,
		},
	}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(firstWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected first client copy, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected second client untouched, got %v", err)
	}
}

func TestInjectTrackerTorrentClientURLOnlyWatchReturnsErrorWithoutFallback(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {TorrentClient: "watch"},
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{
		URL:     "https://aither.cc/torrent/download/374352.382",
		Tracker: "aither",
	})
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got %v", err)
	}

	entries, err := os.ReadDir(watch)
	if err != nil {
		t.Fatalf("read watch folder: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected watch folder untouched for URL injection, got %d entries", len(entries))
	}
	mu.Lock()
	defer mu.Unlock()
	if addCalled {
		t.Fatalf("did not expect tracker-selected watch client to fall back to qbit")
	}
}

func TestInjectTrackerTorrentClientCanSelectClientNamedNone(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	noneWatch := filepath.Join(watchRoot, "none")
	otherWatch := filepath.Join(watchRoot, "other")
	if err := os.MkdirAll(noneWatch, 0o700); err != nil {
		t.Fatalf("mkdir none: %v", err)
	}
	if err := os.MkdirAll(otherWatch, 0o700); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {TorrentClient: "none"},
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"none":  {Type: "watch", WatchFolder: noneWatch},
			"other": {Type: "watch", WatchFolder: otherWatch},
		},
	}, nil)

	meta := api.PreparedMetadata{SourcePath: "video.mkv"}
	if err := svc.Inject(context.Background(), meta, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(noneWatch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected none client copy, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherWatch, filepath.Base(torrentPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected other client untouched, got %v", err)
	}
}

func TestSelectTorrentClientsRejectsAmbiguousCaseInsensitiveNames(t *testing.T) {
	t.Parallel()

	clients := map[string]config.TorrentClientConfig{
		"Qbit": {Type: "watch"},
		"qbit": {Type: "qbit"},
	}

	matches := selectTorrentClients(clients, []string{"QBIT"})
	if len(matches) != 0 {
		t.Fatalf("expected ambiguous case-insensitive selector to be ignored, got %v", matches)
	}

	matches = selectTorrentClients(clients, []string{"qbit"})
	if len(matches) != 1 {
		t.Fatalf("expected exact selector match, got %v", matches)
	}
	if _, ok := matches["qbit"]; !ok {
		t.Fatalf("expected exact lower-case client match, got %v", matches)
	}

	matches = selectTorrentClients(clients, []string{"QBIT", "qbit"})
	if len(matches) != 1 {
		t.Fatalf("expected later exact selector after ambiguous selector, got %v", matches)
	}
	if _, ok := matches["qbit"]; !ok {
		t.Fatalf("expected exact lower-case client match after ambiguous selector, got %v", matches)
	}
}

func TestSelectTorrentClientsDeduplicatesNormalizedSelectors(t *testing.T) {
	t.Parallel()

	clients := map[string]config.TorrentClientConfig{
		"qbit": {Type: "qbit"},
	}

	matches := selectTorrentClients(clients, []string{"qbit", " QBIT ", "qbit"})
	if len(matches) != 1 {
		t.Fatalf("expected duplicate selector variants to select one client, got %v", matches)
	}
	if _, ok := matches["qbit"]; !ok {
		t.Fatalf("expected qbit match, got %v", matches)
	}
}

func TestSelectTorrentClientsKeepsExactCaseVariantClients(t *testing.T) {
	t.Parallel()

	clients := map[string]config.TorrentClientConfig{
		"Qbit": {Type: "watch"},
		"qbit": {Type: "qbit"},
	}

	matches := selectTorrentClients(clients, []string{"Qbit", "qbit"})
	if len(matches) != 2 {
		t.Fatalf("expected both exact case-variant clients, got %v", matches)
	}
	if _, ok := matches["Qbit"]; !ok {
		t.Fatalf("expected exact upper-case client match, got %v", matches)
	}
	if _, ok := matches["qbit"]; !ok {
		t.Fatalf("expected exact lower-case client match, got %v", matches)
	}
}

func TestInjectDisabledClient(t *testing.T) {
	t.Parallel()

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"disabled": {Type: "none"},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: "/tmp/file.torrent"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestInjectQuiProxyClient(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	loginCalled := false
	addCalled := false
	var fileCount int
	var addSkipChecking string
	proxyPrefix := "/proxy/abc123"
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case proxyPrefix + "/api/v2/auth/login":
			mu.Lock()
			loginCalled = true
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case proxyPrefix + "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addCalled = true
			addSkipChecking = r.FormValue("skip_checking")
			if files, ok := r.MultipartForm.File["torrents"]; ok {
				fileCount = len(files)
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	root := t.TempDir()
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:        "qbit",
				QuiProxyURL: server.URL + proxyPrefix,
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if loginCalled {
		t.Fatalf("did not expect qbit login call")
	}
	if !addCalled {
		t.Fatalf("expected qbit add call")
	}
	if addSkipChecking != "true" {
		t.Fatalf("expected skip_checking true, got %q", addSkipChecking)
	}
	if fileCount != 1 {
		t.Fatalf("expected 1 torrent file, got %d", fileCount)
	}
}

func TestInjectWatchFolderTempTorrent(t *testing.T) {
	t.Parallel()

	patterns := []string{
		filepath.Join(os.TempDir(), "*.torrent"),
		filepath.Join(os.TempDir(), "**", "*.torrent"),
	}
	var torrentPath string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		if len(matches) > 0 {
			torrentPath = matches[0]
			break
		}
	}
	if torrentPath == "" {
		t.Skip("no .torrent files found in temp directory")
	}

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	copied := filepath.Join(watch, filepath.Base(torrentPath))
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("expected copied torrent: %v", err)
	}
}

func TestInjectQbitClientFromURL(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	loginCalled := false
	addCalled := false
	var addedURLs string
	var addSkipChecking string
	var fileCount int
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			mu.Lock()
			loginCalled = true
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseForm(); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addCalled = true
			addedURLs = r.FormValue("urls")
			addSkipChecking = r.FormValue("skip_checking")
			fileCount = 0
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	injectURL := "https://aither.cc/torrent/download/374352.382"
	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: injectURL, Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if !loginCalled {
		t.Fatalf("expected qbit login call")
	}
	if !addCalled {
		t.Fatalf("expected qbit add call")
	}
	if addedURLs != injectURL {
		t.Fatalf("expected injected URL %q, got %q", injectURL, addedURLs)
	}
	if addSkipChecking != "true" {
		t.Fatalf("expected skip_checking true, got %q", addSkipChecking)
	}
	if fileCount != 0 {
		t.Fatalf("expected 0 torrent files for URL add, got %d", fileCount)
	}
}

func TestInjectQbitClientPrefersTorrentFileOverURL(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalled := false
	var addedURLs string
	var fileCount int
	errCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addCalled = true
			addedURLs = r.FormValue("urls")
			if r.MultipartForm != nil {
				if files, ok := r.MultipartForm.File["torrents"]; ok {
					fileCount = len(files)
				}
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	root := t.TempDir()
	torrentPath := filepath.Join(root, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{Path: torrentPath, URL: "https://aither.cc/torrent/download/374352.382", Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit add call")
	}
	if fileCount != 1 {
		t.Fatalf("expected 1 torrent file, got %d", fileCount)
	}
	if addedURLs != "" {
		t.Fatalf("expected empty URL payload when path is provided, got %q", addedURLs)
	}
}

func TestInjectURLSkipsWatchFolderClient(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	entries, err := os.ReadDir(watch)
	if err != nil {
		t.Fatalf("read watch folder: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected watch folder untouched for URL injection, got %d entries", len(entries))
	}
	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
	}
}

func TestInjectURLFallsBackToQbitWhenDefaultWatchCannotHandleURL(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "watch",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	entries, err := os.ReadDir(watch)
	if err != nil {
		t.Fatalf("read watch folder: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected watch folder untouched for URL injection, got %d entries", len(entries))
	}
	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
	}
}

func TestInjectURLFallbackIgnoresUnsupportedSelectedClient(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "rtorrent",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"rtorrent": {Type: "rtorrent"},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
	}
}

func TestInjectURLDefaultDisabledDoesNotFallbackToQbit(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "disabled",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"disabled": {Type: "disabled"},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if addCalled {
		t.Fatalf("did not expect disabled default client to fall back to qbit")
	}
}

func TestInjectURLUnknownDefaultFallsBackToQbit(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "missing",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
	}
}

func TestInjectURLUnknownInjectionListFallsBackToQbit(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "missing-default",
			InjectClients: config.CSVList{
				"missing-inject",
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
	}
}

func TestInjectURLInjectionListDisabledDoesNotFallbackToQbit(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "qbit",
			InjectClients: config.CSVList{
				"disabled",
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"disabled": {Type: "none"},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if addCalled {
		t.Fatalf("did not expect disabled injection client to fall back to qbit")
	}
}

func TestInjectURLFallbackSkipsQbitWhenFallbackDisallowed(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var mu sync.Mutex
	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	allowFallback := false
	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "watch",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
			"qbit": {
				Type:          "qbit",
				URL:           server.URL,
				Username:      "user",
				Password:      "pass",
				AllowFallback: &allowFallback,
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if addCalled {
		t.Fatalf("did not expect qbit client with fallback disabled to receive URL fallback")
	}
}

func TestInjectWatchFolderCopiesFileWhenURLAlsoPresent(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	torrentPath := filepath.Join(watchRoot, "sample.torrent")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "watch",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{
		Path: torrentPath,
		URL:  "https://aither.cc/torrent/download/374352.382",
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(watch, filepath.Base(torrentPath))); err != nil {
		t.Fatalf("expected watch client copy with file and URL, got %v", err)
	}
}

func TestInjectURLFallbackSkipsWatchBeforeDelay(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var mu sync.Mutex
	addCalled := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			mu.Lock()
			addCalled = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		PostUpload: config.PostUploadConfig{InjectDelay: 1},
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "watch",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	err := svc.Inject(ctx, api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://aither.cc/torrent/download/374352.382"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
	}
}

func TestInjectCrossSeedURLFallbackPreservesQbitMetadata(t *testing.T) {
	t.Parallel()

	watchRoot := t.TempDir()
	watch := filepath.Join(watchRoot, "watch")
	if err := os.MkdirAll(watch, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var mu sync.Mutex
	addCategory := ""
	addTags := ""
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			if err := r.ParseForm(); err != nil {
				errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			addCategory = r.FormValue("category")
			addTags = r.FormValue("tags")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		ClientSetup: config.ClientSetupConfig{
			DefaultClient: "watch",
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"watch": {Type: "watch", WatchFolder: watch},
			"qbit": {
				Type:              "qbit",
				URL:               server.URL,
				Username:          "user",
				Password:          "pass",
				QbitCrossCategory: "cross-cat",
				QbitCrossTag:      "cross-tag",
			},
		},
	}, nil)

	if err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{
		URL:       "https://aither.cc/torrent/download/374352.382",
		Tracker:   "AITHER",
		CrossSeed: true,
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handler: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if addCategory != "cross-cat" {
		t.Fatalf("expected cross category, got %q", addCategory)
	}
	if addTags != "cross-tag" {
		t.Fatalf("expected cross tag, got %q", addTags)
	}
}

func TestInjectHonorsGlobalDelayCancellation(t *testing.T) {
	t.Parallel()

	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			addCalled = true
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	svc := NewService(config.Config{
		PostUpload: config.PostUploadConfig{InjectDelay: 1},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := svc.Inject(ctx, api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://tracker.example/download/1", Tracker: "AITHER"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if addCalled {
		t.Fatalf("did not expect qbit add call before delay elapsed")
	}
}

func TestInjectTrackerDelayOverrideBeatsGlobalDelay(t *testing.T) {
	t.Parallel()

	addCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		case "/api/v2/torrents/add":
			addCalled = true
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer server.Close()

	overrideDelay := 0
	svc := NewService(config.Config{
		PostUpload: config.PostUploadConfig{InjectDelay: 1},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {InjectDelay: &overrideDelay},
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				URL:      server.URL,
				Username: "user",
				Password: "pass",
			},
		},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := svc.Inject(ctx, api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://tracker.example/download/1", Tracker: "aither"}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if !addCalled {
		t.Fatalf("expected qbit add call without global delay because tracker override is zero")
	}
}
