// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const testXSRFToken = "token=value"

type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	resp, err := http.DefaultTransport.RoundTrip(clone)
	if err != nil {
		return nil, fmt.Errorf("rewrite round trip: %w", err)
	}
	return resp, nil
}

type fakeSite struct {
	uploadStatus int
	approved     atomic.Bool
	uploads      atomic.Int32
	screenshots  atomic.Int32
	throttled    atomic.Bool
}

func (s *fakeSite) handler(t *testing.T, registered []byte) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /torrents/screenshots", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Xsrf-Token") != testXSRFToken {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if s.throttled.CompareAndSwap(false, true) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if _, _, err := r.FormFile("image"); err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		n := s.screenshots.Add(1)
		_, _ = fmt.Fprintf(w, `{"path":"screenshots/%d.webp","url":"/storage/screenshots/%d.webp"}`, n, n)
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("login")) })
	mux.HandleFunc("GET /torrents/upload", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("amigos-share-club-session"); err != nil || cookie.Value != "valid" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:  "XSRF-TOKEN",
			Value: url.QueryEscape(testXSRFToken),
			Path:  "/",
		})
		_, _ = w.Write([]byte("<html></html>"))
	})
	mux.HandleFunc("POST /torrents", func(w http.ResponseWriter, r *http.Request) {
		s.uploads.Add(1)
		if r.Header.Get("X-Xsrf-Token") != testXSRFToken || r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if s.uploadStatus == http.StatusUnprocessableEntity {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Informe o ano de lançamento.","errors":{"year":["Informe o ano de lançamento."]}}`))
			return
		}
		form := r.MultipartForm
		valid := form.Value["category_id"][0] == categoryMovie &&
			slices.Contains(form.Value["attribute_ids[]"], "59") &&
			len(form.File["torrent"]) == 1 && len(form.File["cover"]) == 1 && len(form.File["screenshots[]"]) == 0 &&
			slices.Equal(form.Value["screenshot_paths[]"], []string{"screenshots/1.webp", "screenshots/2.webp"})
		if !valid {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/torrents/321", http.StatusFound)
	})
	mux.HandleFunc("GET /torrents/321", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html></html>")) })
	mux.HandleFunc("GET /torrents/321/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(registered)
	})
	mux.HandleFunc("PATCH /admin/torrents/321/approve", func(w http.ResponseWriter, r *http.Request) {
		s.approved.Store(r.Header.Get("X-Xsrf-Token") == testXSRFToken)
		w.WriteHeader(http.StatusForbidden)
	})
	return mux
}

func testSubmit(t *testing.T, site *fakeSite, sessionValue string) (api.UploadSummary, string, error) {
	t.Helper()

	registered := testTorrentPayload(t)
	server := httptest.NewServer(site.handler(t, registered))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	client, err := newSessionClient(&http.Client{Transport: rewriteTransport{target: target}}, []*http.Cookie{
		{Name: "amigos-share-club-session", Value: sessionValue},
	})
	if err != nil {
		t.Fatalf("session client: %v", err)
	}

	tmp := t.TempDir()
	torrentFile := filepath.Join(tmp, "upload.torrent")
	if err := os.WriteFile(torrentFile, registered, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := warmUploadSession(t.Context(), client); err != nil {
		return api.UploadSummary{}, "", err
	}
	remotePaths, err := uploadScreenshots(t.Context(), client, []commonhttp.FileField{
		{
			FieldName: "image",
			FileName:  "a.png",
			Content:   []byte("a"),
		},
		{
			FieldName: "image",
			FileName:  "b.png",
			Content:   []byte("b"),
		},
	})
	if err != nil {
		t.Fatalf("upload screenshots: %v", err)
	}
	payload := uploadPayload{fields: map[string]string{"category_id": categoryMovie}, attributeIDs: []string{"1", "59"}}
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(
		payload.multipartFields(remotePaths),
		[]commonhttp.FileField{
			{FieldName: "torrent", Path: torrentFile},
			{
				FieldName: "cover",
				FileName:  "cover.jpg",
				Content:   []byte("cover"),
			},
		},
	)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	artifactPath := filepath.Join(tmp, "registered", "ASC.torrent")
	req := trackers.PreparationInput{
		Tracker:       "ASC",
		Meta:          api.UploadSubject{SourcePath: filepath.Join(tmp, "Example.Movie.2026.mkv")},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		TrackerConfig: config.TrackerConfig{UploaderStatus: true},
		Logger:        api.NopLogger{},
	}
	summary, err := submitPreparedUpload(t.Context(), req, client, uploadState{torrentPath: torrentFile}, body, contentType, "", artifactPath)
	return summary, artifactPath, err
}

func TestSubmitPreparedUploadSucceeds(t *testing.T) {
	t.Parallel()

	site := &fakeSite{}
	summary, artifactPath, err := testSubmit(t, site, "valid")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	uploaded := summary.UploadedTorrents[0]
	if uploaded.TorrentID != "321" || uploaded.TorrentURL != baseURL+"/torrents/321" || uploaded.TorrentPath != artifactPath {
		t.Fatalf("uploaded torrent = %+v", uploaded)
	}
	stored, err := os.ReadFile(artifactPath)
	if err != nil || !bytes.Equal(stored, testTorrentPayload(t)) {
		t.Fatalf("registered torrent was not the site download: %v", err)
	}
	if !site.approved.Load() {
		t.Fatal("expected approval attempt with XSRF token")
	}
}

func TestSubmitPreparedUploadReportsValidationErrors(t *testing.T) {
	t.Parallel()

	_, _, err := testSubmit(t, &fakeSite{uploadStatus: http.StatusUnprocessableEntity}, "valid")
	if err == nil || !strings.Contains(err.Error(), "Informe o ano") {
		t.Fatalf("expected validation error detail, got %v", err)
	}
}

func TestSubmitPreparedUploadDetectsExpiredSession(t *testing.T) {
	t.Parallel()

	site := &fakeSite{}
	_, _, err := testSubmit(t, site, "stale")
	if !errors.Is(err, errSessionExpired) {
		t.Fatalf("expected expired session, got %v", err)
	}
	if site.uploads.Load() != 0 {
		t.Fatal("upload must not be posted with an expired session")
	}
}

func TestParseUploadID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://amigos-share.club/torrents/321":        "321",
		"https://amigos-share.club/torrents/321?tab=1":  "321",
		"https://amigos-share.club/torrents/upload":     "",
		"https://amigos-share.club/torrents/321/edit":   "321",
		"https://amigos-share.club/admin/torrents/pend": "",
	}
	for finalURL, want := range tests {
		if got := parseUploadID(finalURL); got != want {
			t.Errorf("parseUploadID(%q) = %q, want %q", finalURL, got, want)
		}
	}
}

func testTorrentPayload(t *testing.T) []byte {
	t.Helper()

	private := true
	infoBytes, err := bencode.Marshal(metainfo.Info{
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Name:        "Example.Movie.2026.mkv",
		Length:      4,
		Private:     &private,
		Source:      sourceFlag,
	})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	var payload bytes.Buffer
	torrentMeta := metainfo.MetaInfo{Announce: "https://tracker.example/announce", InfoBytes: infoBytes}
	if err := torrentMeta.Write(&payload); err != nil {
		t.Fatalf("encode torrent: %v", err)
	}
	return payload.Bytes()
}
