// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadSuccessPersistsReturnedTorrentAndUsesProvidedAssets(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := newCZTUploadTestServer(t, returnedTorrent, uploadPath)
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	server.assertNoHandlerError(t)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent, got %+v", result)
	}
	uploaded := result.UploadedTorrents[0]
	if uploaded.TorrentURL != server.URL+"/details.php?id=123" {
		t.Fatalf("unexpected torrent URL: %q", uploaded.TorrentURL)
	}
	if uploaded.DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("unexpected download URL: %q", uploaded.DownloadURL)
	}
	if strings.TrimSpace(uploaded.TorrentPath) == "" {
		t.Fatal("expected persisted returned torrent path")
	}
	payload, err := os.ReadFile(uploaded.TorrentPath)
	if err != nil {
		t.Fatalf("read persisted torrent: %v", err)
	}
	if string(payload) != string(returnedTorrent) {
		t.Fatalf("persisted torrent did not match returned bytes")
	}
}

func TestUploadUsesProfileEndpointAndIgnoresAPIKey(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := newCZTUploadTestServer(t, returnedTorrent, uploadPath)
	defer server.Close()

	req := cztUploadRequest(t, server.URL)
	req.TrackerConfig.APIKey = "stale-api-key"

	result, err := upload(context.Background(), req)
	server.assertNoHandlerError(t)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected upload success, got %+v", result)
	}
}

func TestUploadRequiresPasskey(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	req := cztUploadRequest(t, server.URL)
	req.TrackerConfig.Passkey = ""

	result, err := upload(context.Background(), req)
	if err == nil {
		t.Fatal("expected missing passkey error")
	}
	if !strings.Contains(err.Error(), "missing passkey") {
		t.Fatalf("expected missing passkey error, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("expected no remote request without passkey, got %d", requests.Load())
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result, got %+v", result)
	}
}

func TestUploadTestServerReportsHandlerAssertionOnTestGoroutine(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := newCZTUploadTestServer(t, returnedTorrent, "/wrong-upload-path")
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	server.assertHandlerErrorContains(t, `expected upload path "/wrong-upload-path"`)
	if err == nil {
		t.Fatal("expected upload error")
	}
	if !strings.Contains(err.Error(), "status=400") {
		t.Fatalf("expected HTTP 400 upload error, got %v", err)
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result on handler assertion failure, got %+v", result)
	}
}

func TestUploadReportsRemoteSuccessWhenReturnedTorrentB64IsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"name":"Release","download_url":"/download.php?id=123","torrent_b64":"not-base64"}`))
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success without artifact failure, got %+v", result)
	}
	if result.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("expected no persisted torrent path, got %q", result.UploadedTorrents[0].TorrentPath)
	}
	if result.UploadedTorrents[0].DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("expected download URL fallback, got %q", result.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadReportsRemoteSuccessWhenReturnedTorrentB64IsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"name":"Release","download_url":"/download.php?id=123","torrent_b64":" "}`))
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success without artifact failure, got %+v", result)
	}
	if result.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("expected no persisted torrent path, got %q", result.UploadedTorrents[0].TorrentPath)
	}
	if result.UploadedTorrents[0].DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("expected download URL fallback, got %q", result.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadReportsRemoteSuccessWhenReturnedTorrentIsCorrupt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"/download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString([]byte("registered-torrent")),
		)
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success without artifact failure, got %+v", result)
	}
	if result.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("expected no persisted torrent path, got %q", result.UploadedTorrents[0].TorrentPath)
	}
	if result.UploadedTorrents[0].DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("expected download URL fallback, got %q", result.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadReportsRemoteSuccessWhenDownloadURLIsOffsite(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"https://evil.example/download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString(returnedTorrent),
		)
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success with artifact, got %+v", result)
	}
	if result.UploadedTorrents[0].DownloadURL != "" {
		t.Fatalf("expected rejected download URL to be omitted, got %q", result.UploadedTorrents[0].DownloadURL)
	}
	if strings.TrimSpace(result.UploadedTorrents[0].TorrentPath) == "" {
		t.Fatal("expected persisted returned torrent path")
	}
}

func TestUploadKeepsRemoteSuccessWhenNoInjectableArtifactExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"name":"Release","download_url":"https://evil.example/download.php?id=123","torrent_b64":"not-base64"}`))
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected remote success count without injectable artifacts, got %+v", result)
	}
	if len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no injectable artifact entry, got %+v", result.UploadedTorrents)
	}
}

func TestUploadResponseParseBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "malformed 201",
			statusCode: http.StatusCreated,
			body:       `{`,
			want:       "parse upload response",
		},
		{
			name:       "malformed non-201 stays http failure",
			statusCode: http.StatusInternalServerError,
			body:       `{`,
			want:       "status=500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := upload(context.Background(), cztUploadRequest(t, server.URL))
			if err == nil {
				t.Fatal("expected upload error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCZTUploadDecodeErrorsAreRedacted(t *testing.T) {
	err := redactCZTUploadDecodeError("decode CZT upload response fields", errors.New("api_key=secret-passkey Cookie=session-token"))
	if err == nil {
		t.Fatal("expected redacted error")
	}
	got := err.Error()
	if strings.Contains(got, "secret-passkey") || strings.Contains(got, "session-token") {
		t.Fatalf("expected redacted decode error")
	}
	if !strings.Contains(got, "decode CZT upload response fields") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected context and redaction marker, got %q", got)
	}

	if _, err := parseCZTUploadID([]byte(`{`)); err == nil || !strings.Contains(err.Error(), "decode CZT upload response fields") {
		t.Fatalf("expected redacted fields decode path, got %v", err)
	}
	if _, err := parseCZTUploadID([]byte(`{"id":"bad"}`)); err == nil || !strings.Contains(err.Error(), "decode CZT upload response id") {
		t.Fatalf("expected redacted id decode path, got %v", err)
	}
	if _, err := parseCZTUploadResponse([]byte(`{`)); err == nil || !strings.Contains(err.Error(), "decode CZT upload response fields") {
		t.Fatalf("expected redacted response fields decode path, got %v", err)
	}
	if _, err := parseCZTUploadResponse([]byte(`{"id":"bad"}`)); err == nil || !strings.Contains(err.Error(), "decode CZT upload response id") {
		t.Fatalf("expected redacted response id decode path, got %v", err)
	}
	if _, err := parseCZTUploadResponse([]byte(`{"id":123,"error":{"api_key":"secret-passkey"}}`)); err == nil {
		t.Fatal("expected redacted optional field decode path")
	} else if got := err.Error(); !strings.Contains(got, "error") || !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "secret-passkey") {
		t.Fatalf("expected redacted optional field decode error, got %q", got)
	}
}

func TestUploadSuccessParsesLargeResponse(t *testing.T) {
	padding := strings.Repeat("a", 70*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"name":%q,"id":123,"download_url":"/download.php?id=123","torrent_b64":" "}`,
			padding,
		)
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success without artifact failure, got %+v", result)
	}
	if result.UploadedTorrents[0].TorrentID != "123" {
		t.Fatalf("expected torrent ID 123, got %q", result.UploadedTorrents[0].TorrentID)
	}
	if result.UploadedTorrents[0].DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("expected download URL from large response, got %q", result.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadResponseBadLocalFieldPreservesRemoteSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"download_url":"/download.php?id=123","torrent_b64":123}`))
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success without artifact, got %+v", result)
	}
	if result.UploadedTorrents[0].TorrentID != "123" || result.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("unexpected uploaded torrent: %+v", result.UploadedTorrents[0])
	}
	if result.UploadedTorrents[0].DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("expected usable download URL despite bad local field, got %q", result.UploadedTorrents[0].DownloadURL)
	}
}

func TestUploadPreCanceledContextStopsBeforeRemoteRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := upload(ctx, cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("expected no remote request after pre-send cancellation, got %d", requests.Load())
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result after pre-send cancellation, got %+v", result)
	}
}

func TestUploadCancellationAfterRequestBuildStopsBeforeRemoteRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	originalFactory := newCZTUploadHTTPClient
	newCZTUploadHTTPClient = func() *http.Client {
		cancel()
		return server.Client()
	}
	t.Cleanup(func() {
		newCZTUploadHTTPClient = originalFactory
	})

	result, err := upload(ctx, cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("expected no remote request after post-build cancellation, got %d", requests.Load())
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result after post-build cancellation, got %+v", result)
	}
}

func TestUploadCancellationAfterResponsePreservesRemoteSuccessWithoutPersistingArtifact(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString(returnedTorrent),
		)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		cancel()
	}))
	defer server.Close()

	req := cztUploadRequest(t, server.URL)
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, trackerName)
	if err != nil {
		t.Fatalf("resolve artifact path: %v", err)
	}
	result, err := upload(ctx, req)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success after cancellation, got %+v", result)
	}
	if result.UploadedTorrents[0].DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("expected download URL preserved, got %q", result.UploadedTorrents[0].DownloadURL)
	}
	if result.UploadedTorrents[0].TorrentPath != "" {
		t.Fatalf("expected no persisted artifact path, got %q", result.UploadedTorrents[0].TorrentPath)
	}
	if _, statErr := os.Stat(artifactPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no persisted artifact, stat err=%v", statErr)
	}
}

func TestUploadCancellationBeforeNonCreatedResponseReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed"}`))
	}))
	defer server.Close()

	result, err := upload(ctx, cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
	if strings.Contains(err.Error(), "status=500") {
		t.Fatalf("expected cancellation before HTTP error, got %v", err)
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result after non-201 cancellation, got %+v", result)
	}
}

func TestUploadCancellationDuringPostCancelsBeforeUploadTimeout(t *testing.T) {
	oldGrace := cztPostCancelGrace.Swap(int64(25 * time.Millisecond))
	t.Cleanup(func() {
		cztPostCancelGrace.Store(oldGrace)
	})

	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
		case <-releaseHandler:
		}
	}))
	defer server.Close()
	defer close(releaseHandler)

	ctx, cancel := context.WithCancel(context.Background())
	req := cztUploadRequest(t, server.URL)
	done := make(chan struct {
		summary api.UploadSummary
		err     error
	}, 1)
	go func() {
		summary, err := upload(ctx, req)
		done <- struct {
			summary api.UploadSummary
			err     error
		}{summary: summary, err: err}
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("expected remote request to start")
	}

	started := time.Now()
	cancel()
	select {
	case result := <-done:
		if result.err == nil {
			t.Fatal("expected cancellation error")
		}
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("expected context cancellation error, got %v", result.err)
		}
		if result.summary.Uploaded != 0 || len(result.summary.UploadedTorrents) != 0 {
			t.Fatalf("expected no uploaded result after canceled POST, got %+v", result.summary)
		}
	case <-time.After(time.Second):
		t.Fatal("expected canceled POST to return before upload timeout")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("expected canceled POST below upload timeout, elapsed %s", elapsed)
	}
}

func TestUploadCancellationAfterPersistPreservesArtifactSummary(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	ctx, cancel := context.WithCancel(context.Background())
	server := newCZTUploadTestServer(t, returnedTorrent, uploadPath)
	defer server.Close()

	req := cztUploadRequest(t, server.URL)
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, trackerName)
	if err != nil {
		t.Fatalf("resolve artifact path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("existing artifact"), 0o600); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}

	oldRemove := removeReturnedTorrentBackup
	removeReturnedTorrentBackup = func(path string) error {
		err := oldRemove(path)
		if strings.Contains(filepath.Base(path), ".backup-") {
			cancel()
		}
		return err
	}
	defer func() {
		removeReturnedTorrentBackup = oldRemove
	}()

	result, err := upload(ctx, req)
	server.assertNoHandlerError(t)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected remote success with artifact after cancellation, got %+v", result)
	}
	if result.UploadedTorrents[0].TorrentPath != artifactPath {
		t.Fatalf("expected artifact path %q, got %q", artifactPath, result.UploadedTorrents[0].TorrentPath)
	}
}

func TestReplaceStagedReturnedTorrentRestoresExistingOnFailure(t *testing.T) {
	tmp := t.TempDir()
	outputPath := filepath.Join(tmp, "registered.torrent")
	original := []byte("existing artifact")
	if err := os.WriteFile(outputPath, original, 0o600); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}

	err := replaceStagedReturnedTorrent(filepath.Join(tmp, "missing.tmp"), outputPath)
	if err == nil {
		t.Fatal("expected replacement error")
	}
	got, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read restored artifact: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("expected existing artifact preserved, got %q", got)
	}
}

func TestUploadOmitsArtifactPathWhenBackupCleanupFails(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString(returnedTorrent),
		)
	}))
	defer server.Close()

	req := cztUploadRequest(t, server.URL)
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, trackerName)
	if err != nil {
		t.Fatalf("resolve artifact path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("existing artifact"), 0o600); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}

	oldRemove := removeReturnedTorrentBackup
	removeReturnedTorrentBackup = func(path string) error {
		if strings.Contains(filepath.Base(path), ".backup-") {
			return os.ErrPermission
		}
		return oldRemove(path)
	}
	defer func() {
		removeReturnedTorrentBackup = oldRemove
	}()

	result, err := upload(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected cleanup-error artifact omitted while preserving remote success, got %+v", result)
	}
}

func TestPersistReturnedTorrentReturnsPathWhenBackupCleanupFails(t *testing.T) {
	req := cztUploadRequest(t, defaultBaseURL)
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, trackerName)
	if err != nil {
		t.Fatalf("resolve artifact path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	original := []byte("existing artifact")
	if err := os.WriteFile(artifactPath, original, 0o600); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}

	oldRemove := removeReturnedTorrentBackup
	removeReturnedTorrentBackup = func(path string) error {
		if strings.Contains(filepath.Base(path), ".backup-") {
			return os.ErrPermission
		}
		return oldRemove(path)
	}
	defer func() {
		removeReturnedTorrentBackup = oldRemove
	}()

	gotPath, err := persistReturnedTorrent(req, base64.StdEncoding.EncodeToString(validTorrentBytes(t)))
	if err == nil {
		t.Fatal("expected cleanup error")
	}
	var cleanupErr returnedTorrentCleanupError
	if !errors.As(err, &cleanupErr) {
		t.Fatalf("expected cleanup error, got %v", err)
	}
	if gotPath != artifactPath {
		t.Fatalf("expected artifact path %q, got %q", artifactPath, gotPath)
	}
	payload, readErr := os.ReadFile(artifactPath)
	if readErr != nil {
		t.Fatalf("read artifact: %v", readErr)
	}
	if string(payload) == string(original) {
		t.Fatal("expected replacement artifact to remain after cleanup failure")
	}
}

func TestJoinCZTURL(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "leading slash",
			base: "https://czteam.me",
			raw:  "/download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name: "no leading slash",
			base: "https://czteam.me",
			raw:  "download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name: "same host absolute",
			base: "https://czteam.me",
			raw:  "https://czteam.me/download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name: "base userinfo stripped",
			base: "https://user:pass@czteam.me",
			raw:  "download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name: "same host absolute userinfo stripped",
			base: "https://czteam.me",
			raw:  "https://user:pass@czteam.me/download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name: "same host scheme relative",
			base: "https://czteam.me",
			raw:  "//czteam.me/download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name: "base path ignored",
			base: "https://czteam.me/nested/path?x=1",
			raw:  "download.php?id=1",
			want: "https://czteam.me/download.php?id=1",
		},
		{
			name:    "absolute offsite",
			base:    "https://czteam.me",
			raw:     "https://cdn.example/download/1",
			wantErr: true,
		},
		{
			name:    "scheme-relative offsite",
			base:    "https://czteam.me",
			raw:     "//cdn.example/download/1",
			wantErr: true,
		},
		{
			name:    "same host wrong scheme",
			base:    "https://czteam.me",
			raw:     "http://czteam.me/download.php?id=1",
			wantErr: true,
		},
		{
			name:    "root path",
			base:    "https://czteam.me",
			raw:     "/",
			wantErr: true,
		},
		{
			name:    "root query",
			base:    "https://czteam.me",
			raw:     "/?id=1",
			wantErr: true,
		},
		{
			name:    "query only",
			base:    "https://czteam.me",
			raw:     "?id=1",
			wantErr: true,
		},
		{
			name:    "pathless",
			base:    "https://czteam.me",
			raw:     "https://czteam.me",
			wantErr: true,
		},
		{
			name:    "empty",
			base:    "https://czteam.me",
			raw:     " ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := joinCZTURL(tt.base, tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestResolveCategoryMatrix(t *testing.T) {
	tests := []struct {
		name    string
		meta    api.UploadSubject
		want    string
		wantErr bool
	}{
		{
			name: "display name answer",
			meta: api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{trackerName: {"category": "Software"}}},
			want: "22",
		},
		{
			name: "numeric answer",
			meta: api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{trackerName: {"category": "6"}}},
			want: "6",
		},
		{
			name: "movie hd",
			meta: api.UploadSubject{Identity: api.ExternalIdentity{Category: "MOVIE"}, Release: api.ReleaseInfo{Resolution: "1080p"}},
			want: "29",
		},
		{
			name: "tv hd ro",
			meta: api.UploadSubject{
				Identity:          api.ExternalIdentity{Category: "TV"},
				Release:           api.ReleaseInfo{Resolution: "1080p"},
				SeasonInt:         1,
				SubtitleLanguages: []string{"ro"},
			},
			want: "34",
		},
		{
			name: "anime",
			meta: api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}, Anime: true},
			want: "23",
		},
		{
			name:    "parsed anime hint is not canonical identity",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Anime-Video"}},
			wantErr: true,
		},
		{
			name:    "video game hint",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "video-game"}},
			wantErr: true,
		},
		{
			name:    "game video hint",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "game-video"}},
			wantErr: true,
		},
		{
			name:    "game movie hint",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "game movie"}},
			wantErr: true,
		},
		{
			name:    "videogame compound",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "videogame"}},
			wantErr: true,
		},
		{
			name:    "gameplay compound",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "gameplay"}},
			wantErr: true,
		},
		{
			name:    "console hint",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Games/Consoles"}},
			wantErr: true,
		},
		{
			name: "release source dvd",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:  api.ReleaseInfo{Source: "DVD"},
			},
			want: "20",
		},
		{
			name: "documentary hint",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:  api.ReleaseInfo{Category: "Documentary"},
			},
			want: "29",
		},
		{
			name: "movie documentary hint",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:  api.ReleaseInfo{Category: "Movie Documentary"},
			},
			want: "29",
		},
		{
			name:    "docs hint",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Docs"}},
			wantErr: true,
		},
		{
			name:    "ebook hint",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "eBook"}},
			wantErr: true,
		},
		{
			name:    "software",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Software"}},
			wantErr: true,
		},
		{
			name:    "music",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Music/Audio"}},
			wantErr: true,
		},
		{
			name:    "music video phrase",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Music Video"}},
			wantErr: true,
		},
		{
			name:    "music video separator",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "music-video"}},
			wantErr: true,
		},
		{
			name:    "music video dotted uppercase",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "MUSIC.VIDEO"}},
			wantErr: true,
		},
		{
			name:    "mvid",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "MVID"}},
			wantErr: true,
		},
		{
			name: "generic video hint",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:  api.ReleaseInfo{Category: "Video", Resolution: "1080p"},
			},
			want: "29",
		},
		{
			name:    "no hints unknown metadata",
			meta:    api.UploadSubject{},
			wantErr: true,
		},
		{
			name:    "unknown non-video",
			meta:    api.UploadSubject{Release: api.ReleaseInfo{Category: "Other Data"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCategory(tt.meta)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected category error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected category %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCategoryQuestionnaireRequiresUnknownNonVideo(t *testing.T) {
	questionnaire := categoryQuestionnaire(api.UploadSubject{Release: api.ReleaseInfo{Category: "Other Data"}})
	if questionnaire == nil || len(questionnaire.Fields) != 1 {
		t.Fatalf("expected one category questionnaire field, got %+v", questionnaire)
	}
	field := questionnaire.Fields[0]
	if !field.Required {
		t.Fatal("expected unknown non-video category to require an explicit answer")
	}
	if field.Value != "" {
		t.Fatalf("expected no default category value, got %q", field.Value)
	}
}

func TestBuildUploadDryRunBlocksMissingRequiredCategory(t *testing.T) {
	req := cztUploadRequest(t, defaultBaseURL)
	req.Meta.Identity.Category = api.CanonicalCategoryUnknown
	req.Meta.Release.Category = "Other Data"

	req.Intent = trackers.PreparationIntentDryRun
	plan, failure := New().Prepare(context.Background(), req)
	if failure != nil {
		t.Fatalf("unexpected dry-run error: %v", failure)
	}
	entry := plan.DryRun()
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked dry-run status, got %q", entry.Status)
	}
	if !strings.Contains(entry.Message, "category questionnaire") {
		t.Fatalf("expected category questionnaire message, got %q", entry.Message)
	}
	if _, ok := entry.Payload["category"]; ok {
		t.Fatalf("expected unresolved category omitted from payload, got %q", entry.Payload["category"])
	}
	if entry.Questionnaire == nil || len(entry.Questionnaire.Fields) != 1 || !entry.Questionnaire.Fields[0].Required {
		t.Fatalf("expected required category questionnaire, got %+v", entry.Questionnaire)
	}

	if _, err := prepareUploadState(context.Background(), req, true); err == nil {
		t.Fatal("expected actual upload state to still require category")
	}
}

func TestBuildDescriptionAndDryRunUseProvidedAssets(t *testing.T) {
	req := cztUploadRequest(t, defaultBaseURL)

	req.Intent = trackers.PreparationIntentDryRun
	plan, failure := New().Prepare(context.Background(), req)
	if failure != nil {
		t.Fatalf("unexpected dry-run error: %v", failure)
	}
	entry := plan.DryRun()
	if !strings.Contains(entry.Description, "[img]https://img.example/raw.jpg[/img]") {
		t.Fatalf("expected provided screenshot in dry-run description, got %q", entry.Description)
	}
	if !strings.Contains(entry.Payload["user_descr"], "[img]https://img.example/raw.jpg[/img]") {
		t.Fatalf("expected provided screenshot in dry-run payload, got %q", entry.Payload["user_descr"])
	}
	if strings.Contains(entry.Payload["user_descr"], "[url=") || strings.Contains(entry.Payload["user_descr"], "[center]") {
		t.Fatalf("expected CZT screenshot BBCode to use only img tags, got %q", entry.Payload["user_descr"])
	}

	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "CZT",
		Meta:    req.Meta,
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "[center]final rewritten body[/center]",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://img.example/should-not-append.jpg",
			}},
			Final: true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected description error: %v", err)
	}
	if result.Description != "[center]final rewritten body[/center]" {
		t.Fatalf("expected final description verbatim, got %q", result.Description)
	}
}

func TestBBCODEScreenshotBlockUsesRawURLsAndCapsAtTwo(t *testing.T) {
	got := bbcodeScreenshotBlock([]api.ScreenshotImage{
		{ImgURL: "https://img.example/rehosted-only.jpg", WebURL: "https://img.example/page-only"},
		{
			ImgURL: "https://img.example/rehosted-1.jpg",
			WebURL: "https://img.example/page-1",
			RawURL: "https://img.example/raw-1.jpg",
		},
		{
			ImgURL: "https://img.example/rehosted-2.jpg",
			WebURL: "https://img.example/page-2",
			RawURL: "https://img.example/raw-2.jpg",
		},
		{
			ImgURL: "https://img.example/rehosted-3.jpg",
			WebURL: "https://img.example/page-3",
			RawURL: "https://img.example/raw-3.jpg",
		},
	})
	want := "[img]https://img.example/raw-1.jpg[/img]\n[img]https://img.example/raw-2.jpg[/img]"
	if got != want {
		t.Fatalf("unexpected screenshot block:\nwant %q\ngot  %q", want, got)
	}
	if strings.Contains(got, "[url=") || strings.Contains(got, "[center]") {
		t.Fatalf("expected only img BBCode, got %q", got)
	}
	if strings.Contains(got, "rehosted") || strings.Contains(got, "page-") || strings.Contains(got, "raw-3") {
		t.Fatalf("expected raw-only first two images, got %q", got)
	}
}

func validTorrentBytes(t *testing.T) []byte {
	t.Helper()
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "source.bin")
	torrentPath := filepath.Join(tmp, "source.torrent")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       sourcePath,
		OutputPath: torrentPath,
		IsPrivate:  true,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
	payload, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read torrent: %v", err)
	}
	return payload
}

type cztUploadTestServer struct {
	*httptest.Server
	handlerErr chan error
}

func newCZTUploadTestServer(t *testing.T, returnedTorrent []byte, expectedPath string) *cztUploadTestServer {
	t.Helper()
	handlerErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := assertCZTUploadRequest(r, expectedPath); err != nil {
			recordCZTUploadHandlerError(handlerErr, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString(returnedTorrent),
		)
	}))
	return &cztUploadTestServer{Server: server, handlerErr: handlerErr}
}

func assertCZTUploadRequest(r *http.Request, expectedPath string) error {
	if r.URL.Path != expectedPath {
		return fmt.Errorf("expected upload path %q, got %q", expectedPath, r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "" {
		return errors.New("expected no authorization header")
	}
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		return fmt.Errorf("parse multipart: %w", err)
	}
	if got := r.FormValue("passkey"); got != "pass" {
		return errors.New("expected passkey form field")
	}
	if got := r.FormValue("user_descr"); !strings.Contains(got, "[img]https://img.example/raw.jpg[/img]") {
		return fmt.Errorf("expected raw screenshot img tag in payload, got %q", got)
	} else if strings.Contains(got, "[url=") || strings.Contains(got, "[center]") || strings.Contains(got, "rehosted.jpg") || strings.Contains(got, "page") {
		return fmt.Errorf("expected only raw screenshot img tags in payload, got %q", got)
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		return fmt.Errorf("expected one torrent file, got %d", len(files))
	}
	file, err := files[0].Open()
	if err != nil {
		return fmt.Errorf("open multipart file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if _, err := io.ReadAll(file); err != nil {
		return fmt.Errorf("read multipart file: %w", err)
	}
	return nil
}

func recordCZTUploadHandlerError(handlerErr chan<- error, err error) {
	select {
	case handlerErr <- err:
	default:
	}
}

func (s *cztUploadTestServer) assertNoHandlerError(t *testing.T) {
	t.Helper()
	if err := s.handlerError(); err != nil {
		t.Fatalf("upload handler assertion: %v", err)
	}
}

func (s *cztUploadTestServer) assertHandlerErrorContains(t *testing.T, want string) {
	t.Helper()
	err := s.handlerError()
	if err == nil {
		t.Fatal("expected upload handler assertion error")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected upload handler assertion containing %q, got %v", want, err)
	}
}

func (s *cztUploadTestServer) handlerError() error {
	select {
	case err := <-s.handlerErr:
		return err
	default:
		return nil
	}
}

func cztUploadRequest(t *testing.T, trackerURL string) trackers.PreparationInput {
	t.Helper()
	if strings.TrimSpace(trackerURL) != "" {
		withCZTBaseURL(t, trackerURL)
	}
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Release.torrent")
	if err := os.WriteFile(torrentPath, []byte("source-torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return trackers.PreparationInput{
		Tracker: "CZT",
		Meta: api.UploadSubject{
			SourcePath:  sourcePath,
			TorrentPath: torrentPath,
			ReleaseName: "Release.2026.1080p.WEB-DL",
			Identity:    api.ExternalIdentity{Category: "MOVIE"},
			Release:     api.ReleaseInfo{Resolution: "1080p"},
		},
		TrackerConfig: config.TrackerConfig{
			Passkey: "pass",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "state", "upbrr.db")}}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://img.example/rehosted.jpg",
				WebURL: "https://img.example/page",
				RawURL: "https://img.example/raw.jpg",
			}, {
				ImgURL: "https://img.example/rehosted-2.jpg",
				WebURL: "https://img.example/page-2",
				RawURL: "https://img.example/raw-2.jpg",
			}, {
				ImgURL: "https://img.example/rehosted-3.jpg",
				WebURL: "https://img.example/page-3",
				RawURL: "https://img.example/raw-3.jpg",
			}},
		},
	}
}

func withCZTBaseURL(t *testing.T, baseURL string) {
	t.Helper()
	old := cztBaseURL
	cztBaseURL = baseURL
	t.Cleanup(func() {
		cztBaseURL = old
	})
}
