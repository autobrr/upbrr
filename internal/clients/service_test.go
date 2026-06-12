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
	errCh    chan error
	mu       sync.Mutex
	category string
	savePath string
	tags     string
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
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				capture.errCh <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			capture.mu.Lock()
			capture.category = r.FormValue("category")
			capture.savePath = r.FormValue("savepath")
			capture.tags = r.FormValue("tags")
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
	if fileCount != 1 {
		t.Fatalf("expected 1 torrent file, got %d", fileCount)
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

func TestInjectQbitClientUsesCrossFieldsWhenConfigured(t *testing.T) {
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
	if capture.category != "cross-cat" {
		t.Fatalf("expected cross category, got %q", capture.category)
	}
	if capture.tags != "cross-tag" {
		t.Fatalf("expected cross tag, got %q", capture.tags)
	}
}

func newQbitAddFailureServer(t *testing.T) *httptest.Server {
	t.Helper()

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
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Fails."))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestInjectQbitClientCleansStagingAfterFileAddFailure(t *testing.T) {
	t.Parallel()

	server := newQbitAddFailureServer(t)
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

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "AITHER"})
	if err == nil {
		t.Fatal("expected qbit add failure")
	}

	stagedPath := filepath.Join(linkRoot, "aither-links", filepath.Base(source))
	if _, statErr := os.Stat(stagedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected staged file cleanup, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(linkRoot, "aither-links")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected tracker staging dir cleanup, stat err=%v", statErr)
	}
}

func TestInjectQbitClientCleansStagingAfterURLAddFailure(t *testing.T) {
	t.Parallel()

	server := newQbitAddFailureServer(t)
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

	err := svc.Inject(context.Background(), api.PreparedMetadata{SourcePath: source, FileList: []string{source}}, api.TorrentResult{URL: "https://tracker.example/torrent/1", Tracker: "AITHER"})
	if err == nil {
		t.Fatal("expected qbit add failure")
	}

	stagedPath := filepath.Join(linkRoot, "aither-links", filepath.Base(source))
	if _, statErr := os.Stat(stagedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected staged file cleanup, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(linkRoot, "aither-links")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected tracker staging dir cleanup, stat err=%v", statErr)
	}
}

func TestInjectQbitClientHardlinksSourceAndUsesLinkedSavePath(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var addSavePath string
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
			addSavePath = r.FormValue("savepath")
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
			"AITHER": {LinkDirName: "aither-links"},
		}},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:            "qbit",
				URL:             server.URL,
				Username:        "user",
				Password:        "pass",
				Linking:         "hardlink",
				LinkedFolder:    config.StringList{linkRoot},
				UseTrackerAsTag: true,
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

	linkedSource := filepath.Join(linkRoot, "aither-links", filepath.Base(source))
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
	wantSavePath := filepath.ToSlash(filepath.Join(linkRoot, "aither-links")) + "/"
	if addSavePath != wantSavePath {
		t.Fatalf("expected savepath %q, got %q", wantSavePath, addSavePath)
	}
	if addTags != "AITHER" {
		t.Fatalf("expected tracker tag, got %q", addTags)
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
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
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
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
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
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
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
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

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
	if !addCalled {
		t.Fatalf("expected qbit URL add call")
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

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := svc.Inject(ctx, api.PreparedMetadata{SourcePath: "video.mkv"}, api.TorrentResult{URL: "https://tracker.example/download/1", Tracker: "AITHER"}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if !addCalled {
		t.Fatalf("expected qbit add call without global delay because tracker override is zero")
	}
}
