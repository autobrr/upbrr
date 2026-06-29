// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// httpHandlerErrorRecorder lets httptest handlers capture assertion failures
// and report them from the owning test goroutine.
type httpHandlerErrorRecorder struct {
	t        *testing.T
	mu       sync.Mutex
	messages []string
}

// newHTTPHandlerErrorRecorder binds handler-side assertion collection to the
// owning test.
func newHTTPHandlerErrorRecorder(t *testing.T) *httpHandlerErrorRecorder {
	t.Helper()
	return &httpHandlerErrorRecorder{t: t}
}

// Errorf records a handler assertion failure without calling testing.T from the
// server goroutine.
func (r *httpHandlerErrorRecorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// Check fails the owning test if any handler assertions were recorded.
func (r *httpHandlerErrorRecorder) Check() {
	r.t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.messages) > 0 {
		r.t.Fatalf("handler assertion failed: %s", strings.Join(r.messages, "; "))
	}
}

func TestBTNUploadEndToEndSuccess(t *testing.T) {
	t.Parallel()

	var autofillCalls atomic.Int32
	var uploadCalls atomic.Int32
	var loginCalls atomic.Int32
	var downloadCalls atomic.Int32
	var uploadFormMu sync.Mutex
	uploadFormValues := map[string]string{}
	uploadFileCount := 0
	handlerErrs := newHTTPHandlerErrorRecorder(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login.php" && r.Method == http.MethodPost:
			loginCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodGet:
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=new") {
				handlerErrs.Errorf("expected refreshed cookie on upload validation, got %q", got)
				http.Error(w, "handler assertion failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodPost:
			contentType := r.Header.Get("Content-Type")
			if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
				autofillCalls.Add(1)
				_, _ = w.Write([]byte(`
					<input name="artist" value="Example Show" />
					<input name="title" value="Episode One" />
					<input name="seriesid" value="999" />
					<input name="year" value="2025" />
					<input name="tags" value="action" />
					<input name="image" value="https://img.example/poster.jpg" />
					<textarea name="album_desc">Episode overview: TBA</textarea>
					<select name="format"><option selected value="MKV">MKV</option></select>
					<select name="bitrate"><option selected value="H.265">H.265</option></select>
					<select name="media"><option selected value="WEB-DL">WEB-DL</option></select>
					<select name="resolution"><option selected value="1080p">1080p</option></select>
				`))
				return
			}
			uploadCalls.Add(1)
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				handlerErrs.Errorf("parse multipart form: %v", err)
				http.Error(w, "handler assertion failed", http.StatusInternalServerError)
				return
			}
			uploadFormMu.Lock()
			for key, values := range r.MultipartForm.Value {
				if len(values) == 0 {
					continue
				}
				uploadFormValues[key] = values[0]
			}
			uploadFileCount = len(r.MultipartForm.File["file_input"])
			uploadFormMu.Unlock()
			w.Header().Set("Location", "/torrents.php?id=123&torrentid=456")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/torrents.php" && r.URL.Query().Get("action") == "download":
			downloadCalls.Add(1)
			_, _ = w.Write([]byte("d8:announce13:https://x.ee"))
		case r.URL.Path == "/torrents.php":
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	dbPath := newBTNAuthDB(t)
	sourcePath := filepath.Join(tempDir, "Example.Show.S01E01.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "input.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	req := trackers.UploadRequest{
		Tracker: "BTN",
		Meta: api.PreparedMetadata{
			SourcePath:      sourcePath,
			TorrentPath:     torrentPath,
			ReleaseName:     "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			Type:            "WEBDL",
			Source:          "WEB-DL",
			Container:       "MKV",
			VideoEncode:     "x265",
			VideoCodec:      "HEVC",
			SeasonInt:       1,
			EpisodeInt:      1,
			EpisodeTitle:    "Episode One",
			EpisodeOverview: "Overview",
			TVDBAiredDate:   "2025-01-01",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
			},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			DescriptionOverride: "[b]Test[/b] description",
			Tag:                 "GRP",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			Username: "user",
			Password: "pass",
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: dbPath},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			}},
		},
	}

	summary, err := upload(context.Background(), req)
	handlerErrs.Check()
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent, got %d", len(summary.UploadedTorrents))
	}
	if got := summary.UploadedTorrents[0].TorrentID; got != "456" {
		t.Fatalf("expected torrent id 456, got %q", got)
	}
	if strings.TrimSpace(summary.UploadedTorrents[0].TorrentPath) == "" {
		t.Fatalf("expected tracker torrent path")
	}
	payload, err := os.ReadFile(summary.UploadedTorrents[0].TorrentPath)
	if err != nil {
		t.Fatalf("expected tracker torrent file: %v", err)
	}
	if len(payload) == 0 || payload[0] != 'd' {
		t.Fatalf("expected bencode torrent payload")
	}

	if loginCalls.Load() != 1 {
		t.Fatalf("expected one login call, got %d", loginCalls.Load())
	}
	if autofillCalls.Load() != 1 {
		t.Fatalf("expected one autofill call, got %d", autofillCalls.Load())
	}
	if uploadCalls.Load() != 1 {
		t.Fatalf("expected one upload call, got %d", uploadCalls.Load())
	}
	if downloadCalls.Load() != 1 {
		t.Fatalf("expected one torrent download call, got %d", downloadCalls.Load())
	}

	uploadFormMu.Lock()
	defer uploadFormMu.Unlock()
	if uploadFileCount != 1 {
		t.Fatalf("expected one file_input upload, got %d", uploadFileCount)
	}
	if got := uploadFormValues["type"]; got != "Episode" {
		t.Fatalf("expected type Episode, got %q", got)
	}
	if got := uploadFormValues["format"]; got != "MKV" {
		t.Fatalf("expected format MKV, got %q", got)
	}
	if got := uploadFormValues["bitrate"]; got != "H.265" {
		t.Fatalf("expected bitrate H.265, got %q", got)
	}
	if got := uploadFormValues["media"]; got != "WEB-DL" {
		t.Fatalf("expected media WEB-DL, got %q", got)
	}
	if got := uploadFormValues["resolution"]; got != "1080p" {
		t.Fatalf("expected resolution 1080p, got %q", got)
	}
	if got := uploadFormValues["origin"]; got != "P2P" {
		t.Fatalf("expected origin P2P, got %q", got)
	}
	if got := uploadFormValues["scenename"]; !strings.Contains(got, "H.265") || strings.Contains(got, "x265") {
		t.Fatalf("expected scenename codec remap to H.265, got %q", got)
	}
}

func TestBTNUploadUsesValidImportedCookiesWithoutCredentials(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	var autofillCalls atomic.Int32
	var uploadCalls atomic.Int32
	handlerErrs := newHTTPHandlerErrorRecorder(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login.php":
			loginCalls.Add(1)
			http.NotFound(w, r)
		case r.URL.Path == "/upload.php" && r.Method == http.MethodGet:
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=imported") {
				handlerErrs.Errorf("expected imported cookie on upload validation, got %q", got)
				http.Error(w, "handler assertion failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodPost:
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=imported") {
				handlerErrs.Errorf("expected imported cookie on upload request, got %q", got)
				http.Error(w, "handler assertion failed", http.StatusInternalServerError)
				return
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				autofillCalls.Add(1)
				_, _ = w.Write([]byte(`
					<input name="artist" value="Example Show" />
					<input name="title" value="Episode One" />
					<input name="seriesid" value="999" />
					<textarea name="album_desc">Episode overview: TBA</textarea>
					<select name="format"><option selected value="MKV">MKV</option></select>
					<select name="bitrate"><option selected value="H.265">H.265</option></select>
					<select name="media"><option selected value="WEB-DL">WEB-DL</option></select>
					<select name="resolution"><option selected value="1080p">1080p</option></select>
				`))
				return
			}
			uploadCalls.Add(1)
			w.Header().Set("Location", "/torrents.php?id=123&torrentid=456")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/torrents.php" && r.URL.Query().Get("action") == "download":
			_, _ = w.Write([]byte("d8:announce13:https://x.ee"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	dbPath := newBTNAuthDB(t)
	if err := cookies.SaveTrackerCookieMap(context.Background(), dbPath, "BTN", map[string]string{"session": "imported"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	sourcePath := filepath.Join(tempDir, "Example.Show.S01E01.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "input.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	req := trackers.UploadRequest{
		Tracker: "BTN",
		Meta: api.PreparedMetadata{
			SourcePath:  sourcePath,
			TorrentPath: torrentPath,
			ReleaseName: "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			Type:        "WEBDL",
			Source:      "WEB-DL",
			Container:   "MKV",
			VideoEncode: "x265",
			VideoCodec:  "HEVC",
			SeasonInt:   1,
			EpisodeInt:  1,
			ExternalIDs: api.ExternalIDs{Category: "TV"},
			Release:     api.ReleaseInfo{Resolution: "1080p", Season: 1, Episode: 1},
		},
		TrackerConfig: config.TrackerConfig{URL: server.URL},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: dbPath},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			}},
		},
	}

	summary, err := upload(context.Background(), req)
	handlerErrs.Check()
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected upload success, got %#v", summary)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("expected no login with valid imported cookies, got %d", loginCalls.Load())
	}
	if autofillCalls.Load() != 1 || uploadCalls.Load() != 1 {
		t.Fatalf("expected autofill/upload calls, got autofill=%d upload=%d", autofillCalls.Load(), uploadCalls.Load())
	}
}

func TestBTNUploadStoredCookieLoadErrorPreventsLogin(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			loginCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/upload.php":
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	_, err := ensureBTNUploadSession(context.Background(), config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, uploadContext{baseURL: server.URL})
	if err == nil {
		t.Fatal("expected stored cookie load error")
	}
	if errors.Is(err, errBTNCookiesMissing) || strings.Contains(err.Error(), "cookie invalid/missing") {
		t.Fatalf("expected storage error, got %v", err)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("expected storage error to prevent login, got %d login calls", loginCalls.Load())
	}
}

func TestBTNDryRunRequiresUploadAuthPrerequisites(t *testing.T) {
	t.Parallel()

	baseReq := newBTNDryRunTestRequest(t, newBTNAuthDB(t))
	_, err := buildUploadDryRun(context.Background(), baseReq)
	if err == nil || !strings.Contains(err.Error(), "cookie invalid/missing and username/password not configured") {
		t.Fatalf("expected BTN dry-run to block API-key-only upload auth, got %v", err)
	}

	credentialsReq := newBTNDryRunTestRequest(t, newBTNAuthDB(t))
	credentialsReq.TrackerConfig.Username = "user"
	credentialsReq.TrackerConfig.Password = "pass"
	entry, err := buildUploadDryRun(context.Background(), credentialsReq)
	if err != nil {
		t.Fatalf("BuildUploadDryRun with credentials: %v", err)
	}
	if entry.Status != "ready" {
		t.Fatalf("expected credentials to satisfy dry-run upload auth prerequisites, got %#v", entry)
	}

	cookieDBPath := newBTNAuthDB(t)
	if err := cookies.SaveTrackerCookieMap(context.Background(), cookieDBPath, "BTN", map[string]string{"session": "imported"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	cookieReq := newBTNDryRunTestRequest(t, cookieDBPath)
	entry, err = buildUploadDryRun(context.Background(), cookieReq)
	if err != nil {
		t.Fatalf("BuildUploadDryRun with stored cookies: %v", err)
	}
	if entry.Status != "ready" {
		t.Fatalf("expected stored cookies to satisfy dry-run upload auth prerequisites, got %#v", entry)
	}
}

func TestResolveSessionForTrackerAuthLoginStorageErrorPreventsLogin(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			loginCalls.Add(1)
			_, _ = w.Write([]byte(`<form><input name="codenumber" /></form><p>2FA required</p>`))
		case "/upload.php":
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	err := ResolveSessionForTrackerAuthLogin(context.Background(), config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	if err == nil {
		t.Fatal("expected stored cookie load error")
	}
	if errors.Is(err, errBTNCookiesMissing) || strings.Contains(err.Error(), "2FA required") {
		t.Fatalf("expected storage error, got %v", err)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("expected storage error to prevent login or 2FA, got %d login calls", loginCalls.Load())
	}
}

func TestResolveSessionForTrackerAuthLoginDecryptErrorPreventsPersistence(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			loginCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/upload.php":
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ctx := context.Background()
	dbPath := newBTNAuthDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "old"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	corruptBTNCookieAuthTag(t, dbPath)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	if err == nil {
		t.Fatal("expected stored cookie decrypt error")
	}
	if errors.Is(err, errBTNCookiesMissing) || strings.Contains(err.Error(), "cookie invalid/missing") {
		t.Fatalf("expected decrypt storage error, got %v", err)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("expected decrypt error to prevent login, got %d login calls", loginCalls.Load())
	}
	if _, loadErr := loadBTNCookies(ctx, dbPath); loadErr == nil || !strings.Contains(loadErr.Error(), "decryption failed") {
		t.Fatalf("expected original corrupt cookie to remain unreadable, got %v", loadErr)
	}
}

func TestBTNPrepareUploadDataFailsOnAutofillFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			_, _ = io.WriteString(w, `<input name="artist" value="Autofill Fail"><input name="title" value="Autofill Fail">`)
		case "/login.php":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	uploadCtx := uploadContext{
		baseURL:   server.URL,
		uploadURL: server.URL + "/upload.php",
		client:    server.Client(),
	}

	req := trackers.UploadRequest{Meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}, ReleaseName: "Show.S01E01", Type: "WEBDL", Source: "WEB-DL", Container: "MKV", VideoEncode: "x265", VideoCodec: "HEVC", SeasonInt: 1, EpisodeInt: 1, Release: api.ReleaseInfo{Resolution: "1080p"}}}
	_, err := prepareUploadData(context.Background(), req, uploadCtx)
	if err == nil {
		t.Fatalf("expected autofill validation error")
	}
	if !strings.Contains(err.Error(), "autofill validation failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBTNUploadCredentialLoginDoesNotPersistInvalidSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newBTNAuthDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/upload.php":
			_, _ = w.Write([]byte(`<form action="/login.php"><input type="password" name="password" /></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, err := ensureBTNUploadSession(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, uploadContext{baseURL: server.URL})
	if !errors.Is(err, errBTNSessionConfirmedInvalid) {
		t.Fatalf("expected invalid session error, got %v", err)
	}
	if values, err := loadBTNCookies(ctx, dbPath); err == nil {
		t.Fatalf("expected invalid login cookies not to persist, got %#v", values)
	}
}

func TestValidateBTNClientSessionRequiresUploadFormStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "help page upload text rejected",
			body:    `<html><h1>Upload help</h1><p>Use the upload page to submit releases.</p></html>`,
			wantErr: true,
		},
		{
			name:    "transient success body rejected",
			body:    `<html><body>ok</body></html>`,
			wantErr: true,
		},
		{
			name:    "real upload form accepted",
			body:    `<form action="/upload.php"><input name="file_input" /></form>`,
			wantErr: false,
		},
		{
			name:    "autofill upload form accepted",
			body:    `<form action="upload.php"><input name="autofill" /></form>`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/upload.php" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			err := validateBTNClientSession(context.Background(), server.Client(), server.URL)
			if tt.wantErr && err == nil {
				t.Fatal("expected upload auth page validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateBTNClientSession: %v", err)
			}
		})
	}
}

func TestResolveSessionForTrackerAuthLoginPersistsCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newBTNAuthDB(t)
	handlerErrs := newHTTPHandlerErrorRecorder(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/upload.php":
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=new") {
				handlerErrs.Errorf("expected refreshed cookie on validation, got %q", got)
				http.Error(w, "handler assertion failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	handlerErrs.Check()
	if err != nil {
		t.Fatalf("ResolveSessionForTrackerAuthLogin: %v", err)
	}
	values, err := loadBTNCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadBTNCookies: %v", err)
	}
	if values["session"] != "new" {
		t.Fatalf("expected persisted BTN cookies, got %#v", values)
	}
}

func TestResolveSessionForTrackerAuthLoginIgnoresIncidentalTwoFactorText(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newBTNAuthDB(t)
	handlerErrs := newHTTPHandlerErrorRecorder(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte(`<html><p>Keep your two-factor recovery codes safe.</p></html>`))
		case "/upload.php":
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=new") {
				handlerErrs.Errorf("expected refreshed cookie on validation, got %q", got)
				http.Error(w, "handler assertion failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	handlerErrs.Check()
	if err != nil {
		t.Fatalf("ResolveSessionForTrackerAuthLogin: %v", err)
	}
	values, err := loadBTNCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadBTNCookies: %v", err)
	}
	if values["session"] != "new" {
		t.Fatalf("expected persisted BTN cookies, got %#v", values)
	}
}

func TestResolveSessionForTrackerAuthLoginRequiresManual2FA(t *testing.T) {
	t.Parallel()

	dbPath := newBTNAuthDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login.php" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`<form><input name="codenumber" /></form><p>2FA required</p>`))
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(context.Background(), config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	if err == nil || !strings.Contains(err.Error(), "2FA required") {
		t.Fatalf("expected 2FA required error, got %v", err)
	}
}

func TestResolveSessionForTrackerAuthLoginMarksSubmitted2FARejected(t *testing.T) {
	t.Parallel()

	dbPath := newBTNAuthDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login.php" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`<p>invalid code</p>`))
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(context.Background(), config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "000000"})
	if !errors.Is(err, ErrSubmitted2FARejected) {
		t.Fatalf("expected submitted 2FA rejection marker, got %v", err)
	}
}

func TestBTNUploadFallsBackToAPIResolution(t *testing.T) {
	t.Parallel()

	var apiSearchCalls atomic.Int32
	var apiDownloadCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login.php" && r.Method == http.MethodPost:
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
		case r.URL.Path == "/upload.php" && r.Method == http.MethodPost:
			contentType := r.Header.Get("Content-Type")
			if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
				_, _ = w.Write([]byte(`
					<input name="artist" value="Example Show" />
					<input name="title" value="Episode One" />
					<input name="seriesid" value="999" />
					<textarea name="album_desc">Episode overview: TBA</textarea>
					<select name="format"><option selected value="MKV">MKV</option></select>
					<select name="bitrate"><option selected value="H.265">H.265</option></select>
					<select name="media"><option selected value="WEB-DL">WEB-DL</option></select>
					<select name="resolution"><option selected value="1080p">1080p</option></select>
				`))
				return
			}
			w.Header().Set("Location", "/torrents.php?id=123&torrentid=456")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/torrents.php" && r.URL.Query().Get("action") == "download":
			// Force fallback: looks like HTML page, not bencoded torrent payload.
			_, _ = w.Write([]byte("<html>not a torrent</html>"))
		case r.URL.Path == "/rpc" && r.Method == http.MethodPost:
			var rpc struct {
				Method string `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&rpc)
			switch rpc.Method {
			case "getTorrentsSearch":
				apiSearchCalls.Add(1)
				_, _ = w.Write([]byte(`{"result":{"torrents":{"777":{"GroupID":"123"}}}}`))
			case "getTorrentById":
				apiDownloadCalls.Add(1)
				_, _ = w.Write([]byte(`{"result":{"DownloadURL":"http://` + r.Host + `/mock-download"}}`))
			default:
				http.NotFound(w, r)
			}
		case r.URL.Path == "/mock-download":
			_, _ = w.Write([]byte("d8:announce13:https://x.ee"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	dbPath := newBTNAuthDB(t)
	sourcePath := filepath.Join(tempDir, "Example.Show.S01E01.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "input.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	req := trackers.UploadRequest{
		Tracker: "BTN",
		Meta: api.PreparedMetadata{
			SourcePath:          sourcePath,
			TorrentPath:         torrentPath,
			ReleaseName:         "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			Type:                "WEBDL",
			Source:              "WEB-DL",
			Container:           "MKV",
			VideoEncode:         "x265",
			VideoCodec:          "HEVC",
			SeasonInt:           1,
			EpisodeInt:          1,
			EpisodeTitle:        "Episode One",
			EpisodeOverview:     "Overview",
			TVDBAiredDate:       "2025-01-01",
			DescriptionOverride: "[b]Test[/b] description",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
			},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			Tag: "GRP",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      server.URL,
			Username: "user",
			Password: "pass",
			Unknown: map[string]any{
				"api_url": server.URL + "/rpc",
			},
		},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: dbPath},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			}},
		},
	}

	summary, err := upload(context.Background(), req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent, got %d", len(summary.UploadedTorrents))
	}
	payload, err := os.ReadFile(summary.UploadedTorrents[0].TorrentPath)
	if err != nil {
		t.Fatalf("expected tracker torrent file: %v", err)
	}
	if len(payload) == 0 || payload[0] != 'd' {
		t.Fatalf("expected bencode torrent payload from API fallback")
	}
	if apiSearchCalls.Load() != 1 {
		t.Fatalf("expected one API search call, got %d", apiSearchCalls.Load())
	}
	if apiDownloadCalls.Load() != 1 {
		t.Fatalf("expected one API download call, got %d", apiDownloadCalls.Load())
	}
}

func newBTNAuthDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		t.Fatalf("MigrateContext: %v", err)
	}
	_ = repo.Close()
	return dbPath
}

// corruptBTNCookieAuthTag keeps the BTN cookie row present while making its
// encrypted value fail authentication, exercising decrypt-error handling without
// falling back to the missing-cookie path.
func corruptBTNCookieAuthTag(t *testing.T, dbPath string) {
	t.Helper()

	ctx := context.Background()
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	defer repo.Close()

	result, err := repo.RawDB().ExecContext(ctx, `UPDATE tracker_cookies SET auth_tag = ? WHERE tracker_id = ? AND cookie_name = ?`, "AAAAAAAAAAAAAAAAAAAAAA==", "BTN", "session")
	if err != nil {
		t.Fatalf("corrupt BTN cookie auth tag: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("corrupt BTN cookie rows affected: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected one corrupt BTN cookie row, got %d", affected)
	}
}

// newBTNDryRunTestRequest returns a minimal TV upload request with a BTN API key.
// Callers add credentials or stored cookies to exercise dry-run auth gating.
func newBTNDryRunTestRequest(t *testing.T, dbPath string) trackers.UploadRequest {
	t.Helper()

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "Example.Show.S01E01.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "input.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	return trackers.UploadRequest{
		Tracker: "BTN",
		Meta: api.PreparedMetadata{
			SourcePath:          sourcePath,
			TorrentPath:         torrentPath,
			ReleaseName:         "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			Type:                "WEBDL",
			Source:              "WEB-DL",
			Container:           "MKV",
			VideoEncode:         "x265",
			VideoCodec:          "HEVC",
			SeasonInt:           1,
			EpisodeInt:          1,
			EpisodeTitle:        "Episode One",
			EpisodeOverview:     "Overview",
			TVDBAiredDate:       "2025-01-01",
			DescriptionOverride: "[b]Test[/b] description",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
			},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
		},
		TrackerConfig: config.TrackerConfig{},
		AppConfig: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: dbPath},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			}},
		},
	}
}
