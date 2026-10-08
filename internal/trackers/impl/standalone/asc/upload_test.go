// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"
	"time"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	testXSRFToken    = "token=value"
	testAnnounceURL  = "https://tracker.example/announce/passkey"
	testTorrentRoute = "/torrents/321"
)

func init() {
	// Keep Retry-After waits short; the retry logic itself is unchanged.
	retryAfterUnit = time.Millisecond
}

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

// fakeSite imitates the ASC Laravel endpoints the upload uses. Zero values
// give the happy path; each field switches on one failure mode.
type fakeSite struct {
	noXSRF           bool
	throttleOnce     bool
	alwaysThrottle   bool
	screenshotStatus int
	screenshotNoPath bool
	uploadStatus     int
	uploadRedirect   string
	downloadFails    bool

	approveCalls      atomic.Int32
	approveWithToken  atomic.Bool
	uploads           atomic.Int32
	screenshotCalls   atomic.Int32
	screenshotsStored atomic.Int32
	throttled         atomic.Bool
}

func (s *fakeSite) handler(t *testing.T, registered []byte) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("login")) })
	mux.HandleFunc("GET /torrents/upload", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("amigos-share-club-session"); err != nil || cookie.Value != "valid" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if !s.noXSRF {
			http.SetCookie(w, &http.Cookie{
				Name:  "XSRF-TOKEN",
				Value: url.QueryEscape(testXSRFToken),
				Path:  "/",
			})
		}
		_, _ = w.Write([]byte("<html></html>"))
	})
	mux.HandleFunc("POST /torrents/screenshots", func(w http.ResponseWriter, r *http.Request) {
		s.screenshotCalls.Add(1)
		if r.Header.Get("X-Xsrf-Token") != testXSRFToken {
			t.Errorf("screenshot request missing XSRF token")
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if s.alwaysThrottle || (s.throttleOnce && s.throttled.CompareAndSwap(false, true)) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if s.screenshotStatus != 0 {
			w.WriteHeader(s.screenshotStatus)
			_, _ = w.Write([]byte(`{"message":"bad image"}`))
			return
		}
		if _, _, err := r.FormFile("image"); err != nil {
			t.Errorf("screenshot request missing image field: %v", err)
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		if s.screenshotNoPath {
			_, _ = w.Write([]byte(`{"url":"/storage/x.webp"}`))
			return
		}
		n := s.screenshotsStored.Add(1)
		_, _ = fmt.Fprintf(w, `{"path":"screenshots/%d.webp","url":"/storage/screenshots/%d.webp"}`, n, n)
	})
	mux.HandleFunc("POST /torrents", func(w http.ResponseWriter, r *http.Request) {
		s.uploads.Add(1)
		if r.Header.Get("X-Xsrf-Token") != testXSRFToken || r.Header.Get("Accept") != "application/json" {
			t.Errorf("upload request missing XSRF/XHR headers")
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse upload form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch {
		case s.uploadStatus == http.StatusUnprocessableEntity:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Informe o ano de lançamento.","errors":{"year":["Informe o ano de lançamento."]}}`))
			return
		case s.uploadRedirect != "":
			http.Redirect(w, r, s.uploadRedirect, http.StatusFound)
			return
		}
		form := r.MultipartForm
		if got := form.Value["category_id"]; len(got) != 1 || got[0] != categoryMovie {
			t.Errorf("category_id = %v", got)
		}
		if !slices.Contains(form.Value["attribute_ids[]"], "59") {
			t.Errorf("attribute_ids[] = %v", form.Value["attribute_ids[]"])
		}
		if len(form.File["torrent"]) != 1 || len(form.File["cover"]) != 1 {
			t.Errorf("files torrent=%d cover=%d", len(form.File["torrent"]), len(form.File["cover"]))
		}
		if !slices.Equal(form.Value["screenshot_paths[]"], []string{"screenshots/1.webp", "screenshots/2.webp"}) {
			t.Errorf("screenshot_paths[] = %v", form.Value["screenshot_paths[]"])
		}
		http.Redirect(w, r, testTorrentRoute, http.StatusFound)
	})
	// The new torrent page is restricted while pending; success must not depend on it.
	mux.HandleFunc("GET "+testTorrentRoute, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	mux.HandleFunc("GET "+testTorrentRoute+"/download", func(w http.ResponseWriter, _ *http.Request) {
		if s.downloadFails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(registered)
	})
	mux.HandleFunc("PATCH /admin/torrents/321/approve", func(w http.ResponseWriter, r *http.Request) {
		s.approveCalls.Add(1)
		s.approveWithToken.Store(r.Header.Get("X-Xsrf-Token") == testXSRFToken)
		w.WriteHeader(http.StatusForbidden)
	})
	return mux
}

type submitResult struct {
	summary      api.UploadSummary
	artifactPath string
	dbPath       string
	err          error
}

func testSubmit(t *testing.T, site *fakeSite, sessionValue string, trackerCfg config.TrackerConfig) submitResult {
	t.Helper()
	return testSubmitWithLogger(t, site, sessionValue, trackerCfg, api.NopLogger{})
}

func testSubmitWithLogger(t *testing.T, site *fakeSite, sessionValue string, trackerCfg config.TrackerConfig, logger api.Logger) submitResult {
	t.Helper()

	uploaded := testTorrentPayload(t, "https://tracker.example/announce")
	registered := testTorrentPayload(t, testAnnounceURL)
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
	if err := os.WriteFile(torrentFile, uploaded, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	payload := uploadPayload{fields: map[string]string{"category_id": categoryMovie}, attributeIDs: []string{"1", "59"}}
	submission := preparedSubmission{
		fields: payload.multipartFields(nil),
		files: []commonhttp.FileField{
			{
				FieldName: "torrent",
				FileName:  "upload.torrent",
				Content:   uploaded,
			},
			{
				FieldName: "cover",
				FileName:  "cover.jpg",
				Content:   []byte("cover"),
			},
		},
		images: []commonhttp.FileField{
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
		},
		artifactPath: filepath.Join(tmp, "registered", "ASC.torrent"),
	}
	dbPath := filepath.Join(tmp, "ua.db")
	req := trackers.PreparationInput{
		Tracker:       "ASC",
		Meta:          api.UploadSubject{SourcePath: filepath.Join(tmp, "Example.Movie.2026.mkv")},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		TrackerConfig: trackerCfg,
		Logger:        logger,
	}
	summary, err := submitPreparedUpload(t.Context(), req, client, submission)
	return submitResult{
		summary:      summary,
		artifactPath: submission.artifactPath,
		dbPath:       dbPath,
		err:          err,
	}
}

func TestSubmitPreparedUploadSucceeds(t *testing.T) {
	t.Parallel()

	site := &fakeSite{throttleOnce: true}
	result := testSubmit(t, site, "valid", config.TrackerConfig{UploaderStatus: true})
	if result.err != nil {
		t.Fatalf("submit: %v", result.err)
	}
	if len(result.summary.UploadedTorrents) != 1 {
		t.Fatalf("summary = %+v", result.summary)
	}
	uploaded := result.summary.UploadedTorrents[0]
	if uploaded.TorrentID != "321" || uploaded.TorrentURL != baseURL+testTorrentRoute || uploaded.TorrentPath != result.artifactPath {
		t.Fatalf("uploaded torrent = %+v", uploaded)
	}
	stored, err := os.ReadFile(result.artifactPath)
	if err != nil || !bytes.Equal(stored, testTorrentPayload(t, testAnnounceURL)) {
		t.Fatalf("registered torrent was not the site download: %v", err)
	}
	if site.screenshotCalls.Load() != 3 || site.screenshotsStored.Load() != 2 {
		t.Fatalf("screenshot calls=%d stored=%d, want one 429 retry", site.screenshotCalls.Load(), site.screenshotsStored.Load())
	}
	if site.approveCalls.Load() != 1 || !site.approveWithToken.Load() {
		t.Fatal("expected one approval attempt with XSRF token")
	}
}

func TestSubmitPreparedUploadSkipsApprovalWhenUploaderStatusDisabled(t *testing.T) {
	t.Parallel()

	site := &fakeSite{}
	if result := testSubmit(t, site, "valid", config.TrackerConfig{}); result.err != nil {
		t.Fatalf("submit: %v", result.err)
	}
	if site.approveCalls.Load() != 0 {
		t.Fatal("approval must not be attempted without uploader_status")
	}
}

func TestSubmitPreparedUploadWithoutSiteTorrentSkipsInjection(t *testing.T) {
	t.Parallel()

	result := testSubmit(t, &fakeSite{downloadFails: true}, "valid", config.TrackerConfig{})
	if result.err != nil {
		t.Fatalf("a failed torrent download must not fail the upload: %v", result.err)
	}
	if result.summary.Uploaded != 1 || result.summary.UploadedTorrents[0].TorrentID != "321" {
		t.Fatalf("summary = %+v", result.summary)
	}
	if got := result.summary.UploadedTorrents[0].TorrentPath; got != "" {
		t.Fatalf("torrent path = %q, want none (ASC rewrites torrents, no local reconstruction)", got)
	}
	if _, err := os.Stat(result.artifactPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no registered torrent may be written, stat err = %v", err)
	}
}

func TestSubmitPreparedUploadReportsValidationErrors(t *testing.T) {
	t.Parallel()

	result := testSubmit(t, &fakeSite{uploadStatus: http.StatusUnprocessableEntity}, "valid", config.TrackerConfig{})
	if result.err == nil || !strings.Contains(result.err.Error(), "Informe o ano") {
		t.Fatalf("expected validation error detail, got %v", result.err)
	}
}

func TestSubmitPreparedUploadRejectsRedirectWithoutTorrent(t *testing.T) {
	t.Parallel()

	result := testSubmit(t, &fakeSite{uploadRedirect: uploadPagePath}, "valid", config.TrackerConfig{})
	if result.err == nil || !strings.Contains(result.err.Error(), "rejected") || result.summary.Uploaded != 0 {
		t.Fatalf("expected rejected upload, got summary=%+v err=%v", result.summary, result.err)
	}
	if !failureArtifactWritten(t, filepath.Dir(result.dbPath)) {
		t.Fatal("expected an upload failure artifact")
	}
}

func TestSubmitPreparedUploadDetectsExpiredSession(t *testing.T) {
	t.Parallel()

	t.Run("before writes", func(t *testing.T) {
		t.Parallel()
		site := &fakeSite{}
		result := testSubmit(t, site, "stale", config.TrackerConfig{})
		if !errors.Is(result.err, errSessionExpired) {
			t.Fatalf("expected expired session, got %v", result.err)
		}
		if site.uploads.Load() != 0 || site.screenshotCalls.Load() != 0 {
			t.Fatal("nothing may be posted with an expired session")
		}
	})
	t.Run("upload redirected to login", func(t *testing.T) {
		t.Parallel()
		result := testSubmit(t, &fakeSite{uploadRedirect: "/login"}, "valid", config.TrackerConfig{})
		if !errors.Is(result.err, errSessionExpired) {
			t.Fatalf("expected expired session, got %v", result.err)
		}
	})
}

func TestSubmitPreparedUploadRequiresXSRFToken(t *testing.T) {
	t.Parallel()

	site := &fakeSite{noXSRF: true}
	result := testSubmit(t, site, "valid", config.TrackerConfig{})
	if result.err == nil || !strings.Contains(result.err.Error(), "XSRF") {
		t.Fatalf("expected missing XSRF error, got %v", result.err)
	}
	if site.uploads.Load() != 0 || site.screenshotCalls.Load() != 0 {
		t.Fatal("nothing may be posted without an XSRF token")
	}
}

func TestSubmitPreparedUploadScreenshotFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		site      *fakeSite
		wantErr   string
		wantCalls int32
	}{
		{"rate limit exhausted", &fakeSite{alwaysThrottle: true}, "rate limited after 5 attempts", maxScreenshotAttempts},
		{"missing path", &fakeSite{screenshotNoPath: true}, "did not include a path", 1},
		{"rejected image is not retried", &fakeSite{screenshotStatus: http.StatusUnprocessableEntity}, "status=422", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := testSubmit(t, tc.site, "valid", config.TrackerConfig{})
			if result.err == nil || !strings.Contains(result.err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", result.err, tc.wantErr)
			}
			if got := tc.site.screenshotCalls.Load(); got != tc.wantCalls {
				t.Fatalf("screenshot calls = %d, want %d", got, tc.wantCalls)
			}
			if tc.site.uploads.Load() != 0 {
				t.Fatal("the upload must not be posted after a screenshot failure")
			}
		})
	}
}

func TestParseUploadID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://amigos-share.club/torrents/321":         "321",
		"/torrents/321":                                  "321",
		"https://amigos-share.club/torrents/321?tab=1":   "321",
		"https://amigos-share.club/torrents/upload":      "",
		"https://amigos-share.club/torrents/321/edit":    "321",
		"https://amigos-share.club/admin/torrents/5/fix": "",
	}
	for location, want := range tests {
		if got := parseUploadID(location); got != want {
			t.Errorf("parseUploadID(%q) = %q, want %q", location, got, want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()

	tests := map[string]time.Duration{
		"3":   3 * retryAfterUnit,
		"":    defaultRetryAfterSeconds * retryAfterUnit,
		"0":   defaultRetryAfterSeconds * retryAfterUnit,
		"999": maxRetryAfterSeconds * retryAfterUnit,
	}
	for header, want := range tests {
		if got := retryAfter(header); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", header, got, want)
		}
	}
	future := time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat)
	if got := retryAfter(future); got < 15*retryAfterUnit || got > 20*retryAfterUnit {
		t.Errorf("retryAfter(http-date) = %s", got)
	}
}

func TestDownloadCover(t *testing.T) {
	t.Parallel()

	pngHeader := []byte("\x89PNG\r\n\x1a\n0000")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/poster.png":
			_, _ = w.Write(pngHeader)
		case "/error.html":
			_, _ = w.Write([]byte("<html>cdn error</html>"))
		case "/huge.png":
			_, _ = w.Write(append(pngHeader, make([]byte, maxImageBytes)...))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	cover, err := downloadCover(t.Context(), server.Client(), server.URL+"/poster.png")
	if err != nil || cover.FileName != "cover.png" || cover.FieldName != "cover" {
		t.Fatalf("cover = %+v err=%v", cover, err)
	}
	for _, path := range []string{"/error.html", "/huge.png", "/missing.png"} {
		if _, err := downloadCover(t.Context(), server.Client(), server.URL+path); err == nil {
			t.Errorf("downloadCover(%s) succeeded, want error", path)
		}
	}
}

func TestResolveCoverURL(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://image.tmdb.org/t/p/original/poster.jpg": "https://image.tmdb.org/t/p/w780/poster.jpg",
		"https://img.example/poster.jpg":                 "https://img.example/poster.jpg",
		"ftp://img.example/poster.jpg":                   "",
		"/relative/poster.jpg":                           "",
	}
	for poster, want := range tests {
		meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Poster: poster}}}
		if got := resolveCoverURL(meta); got != want {
			t.Errorf("resolveCoverURL(%q) = %q, want %q", poster, got, want)
		}
	}
}

func failureArtifactWritten(t *testing.T, root string) bool {
	t.Helper()

	found := false
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.Contains(entry.Name(), "upload_failure") {
			found = true
		}
		return nil
	})
	return found
}

func testTorrentPayload(t *testing.T, announce string) []byte {
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
	torrentMeta := metainfo.MetaInfo{Announce: announce, InfoBytes: infoBytes}
	if err := torrentMeta.Write(&payload); err != nil {
		t.Fatalf("encode torrent: %v", err)
	}
	return payload.Bytes()
}

func TestValidateSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		session       string
		status        int
		wantNil       bool
		wantAuth      bool
		wantTransient bool
	}{
		{
			name:    "valid session",
			session: "valid",
			wantNil: true,
		},
		{
			name:     "expired session needs cookies",
			session:  "stale",
			wantAuth: true,
		},
		{
			name:          "site error is transient",
			session:       "valid",
			status:        http.StatusBadGateway,
			wantTransient: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/login":
					_, _ = w.Write([]byte("login"))
				case tc.status != 0:
					w.WriteHeader(tc.status)
				default:
					if cookie, err := r.Cookie("amigos-share-club-session"); err != nil || cookie.Value != "valid" {
						http.Redirect(w, r, "/login", http.StatusFound)
						return
					}
					http.SetCookie(w, &http.Cookie{
						Name:  "XSRF-TOKEN",
						Value: "token",
						Path:  "/",
					})
				}
			}))
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			client, err := newSessionClient(&http.Client{Transport: rewriteTransport{target: target}}, []*http.Cookie{
				{Name: "amigos-share-club-session", Value: tc.session},
			})
			if err != nil {
				t.Fatalf("session client: %v", err)
			}
			err = validateSession(t.Context(), client)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("validateSession = %v, want nil", err)
				}
				return
			}
			resolution, ok := errors.AsType[*trackers.AuthResolutionError](err)
			if !ok {
				t.Fatalf("validateSession = %v, want AuthResolutionError", err)
			}
			if resolution.AuthRequired != tc.wantAuth || resolution.Transient != tc.wantTransient || resolution.ConfirmedInvalid {
				t.Fatalf("resolution = %+v", resolution)
			}
		})
	}
}

func TestResolveAuthSessionWithoutCookiesRequiresAuth(t *testing.T) {
	t.Parallel()

	err := resolveAuthSession(t.Context(), config.TrackerConfig{}, filepath.Join(t.TempDir(), "ua.db"), api.TrackerAuthLoginRequest{})
	resolution, ok := errors.AsType[*trackers.AuthResolutionError](err)
	if !ok || !resolution.AuthRequired || resolution.ConfirmedInvalid {
		t.Fatalf("resolveAuthSession without cookies = %v", err)
	}
}

type warnCaptureLogger struct {
	api.NopLogger
	mu       sync.Mutex
	warnings []string
}

func (l *warnCaptureLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func TestSubmitPreparedUploadLogsRejectionDetail(t *testing.T) {
	t.Parallel()

	logger := &warnCaptureLogger{}
	result := testSubmitWithLogger(t, &fakeSite{uploadStatus: http.StatusUnprocessableEntity}, "valid", config.TrackerConfig{}, logger)
	if result.err == nil {
		t.Fatal("expected a rejected upload")
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	joined := strings.Join(logger.warnings, "\n")
	for _, want := range []string{"ASC upload not accepted", "status=422", "Informe o ano"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warning log missing %q: %s", want, joined)
		}
	}
}

func TestResponseSummaryStripsMarkupAndTruncates(t *testing.T) {
	t.Parallel()

	if got := responseSummary([]byte("<html><body>\n  <p>Torrent   already exists</p>\n</body></html>")); got != "Torrent already exists" {
		t.Fatalf("summary = %q", got)
	}
	if got := responseSummary([]byte(strings.Repeat("x", 1000))); len(got) != responseSummaryLimit+3 {
		t.Fatalf("summary length = %d", len(got))
	}
}

func TestResponseSummaryIsRuneSafeAndDropsScripts(t *testing.T) {
	t.Parallel()

	accented := strings.Repeat("é", responseSummaryLimit+10)
	got := responseSummary([]byte(accented))
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != responseSummaryLimit+3 {
		t.Fatalf("summary is not rune-safe: %d runes, valid=%t", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	html := "<html><head><style>body{color:red}</style><script>var x = 1;</script></head><body>Este torrent já foi enviado.</body></html>"
	if got := responseSummary([]byte("Falhou. <script>var secret = 1; window.state = {")); got != "Falhou." {
		t.Fatalf("unclosed script leaked into the summary: %q", got)
	}
	if got := responseSummary([]byte("Falhou. <div cla")); got != "Falhou." {
		t.Fatalf("truncated tag leaked into the summary: %q", got)
	}
	if got := responseSummary([]byte(html)); got != "Este torrent já foi enviado." {
		t.Fatalf("summary = %q", got)
	}
}
