// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/btn"
	"github.com/autobrr/upbrr/internal/trackers/impl/mtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

type trackerAuthRecordingLogger struct {
	api.NopLogger
	info []string
	warn []string
}

func (l *trackerAuthRecordingLogger) Infof(format string, args ...any) {
	l.info = append(l.info, fmt.Sprintf(format, args...))
}

func (l *trackerAuthRecordingLogger) Warnf(format string, args ...any) {
	l.warn = append(l.warn, fmt.Sprintf(format, args...))
}

func TestLoginCreatesManual2FAChallengeBeforeReturning(t *testing.T) {
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {Username: "user", Password: "pass"},
			},
		},
	}
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "PTP",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "PTP"}
		},
	}
	loginService := NewService(cfg)
	loginService.adapters = map[string]Adapter{"PTP": adapter}

	status, err := loginService.Login(context.Background(), "PTP", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !status.Needs2FA || strings.TrimSpace(status.ChallengeID) == "" {
		t.Fatalf("expected active 2FA challenge, got %#v", status)
	}

	submitService := NewService(cfg)
	submitService.adapters = map[string]Adapter{"PTP": adapter}
	status, err = submitService.Submit2FA(context.Background(), status.ChallengeID, "123456")
	if err != nil {
		t.Fatalf("Submit2FA with challenge from separate service: %v", err)
	}
	if status.Needs2FA || status.ChallengeID != "" || status.Message != "2FA auth completed" {
		t.Fatalf("unexpected submit status: %#v", status)
	}
}

func TestDefaultAdaptersExposeMTVPTPManual2FAChallenge(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg    func(string) config.TrackerConfig
		server func(*testing.T) *httptest.Server
	}{
		"MTV": {
			cfg: func(serverURL string) config.TrackerConfig {
				return config.TrackerConfig{URL: serverURL, Username: "user", Password: "pass"}
			},
			server: newMTVManual2FAServer,
		},
		"BTN": {
			cfg: func(serverURL string) config.TrackerConfig {
				return config.TrackerConfig{URL: serverURL, APIKey: "api-token", Username: "user", Password: "pass"}
			},
			server: newBTNManual2FAServer,
		},
		"PTP": {
			cfg: func(serverURL string) config.TrackerConfig {
				return config.TrackerConfig{
					URL:         serverURL,
					Username:    "user",
					Password:    "pass",
					AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
				}
			},
			server: newPTPManual2FAServer,
		},
	}
	for trackerID, tt := range tests {
		t.Run(trackerID, func(t *testing.T) {
			t.Parallel()

			server := tt.server(t)
			dbPath := newTrackerAuthTestDB(t)
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: dbPath},
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{
						trackerID: tt.cfg(server.URL),
					},
				},
			}

			for name, action := range map[string]func(*Service) (api.TrackerAuthStatus, error){
				"Login": func(service *Service) (api.TrackerAuthStatus, error) {
					return service.Login(context.Background(), trackerID, api.TrackerAuthLoginRequest{})
				},
				"Validate": func(service *Service) (api.TrackerAuthStatus, error) {
					return service.Validate(context.Background(), trackerID)
				},
			} {
				t.Run(name, func(t *testing.T) {
					service := NewService(cfg)
					service.challenges = NewChallengeManager(defaultChallengeTTL)

					status, err := action(service)
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					if !status.Needs2FA || strings.TrimSpace(status.ChallengeID) == "" {
						t.Fatalf("%s should expose an active manual 2FA challenge, got %#v", name, status)
					}
					if status.State != StateLoginRequired || status.Message != "2FA required" {
						t.Fatalf("%s returned unexpected status: %#v", name, status)
					}
				})
			}
		})
	}
}

func TestBTNCapabilityAndLocalStatusSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Metadata:     config.MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {},
		}},
	}
	service := NewService(cfg)
	caps, err := service.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	var btnCap api.TrackerAuthCapability
	for _, cap := range caps {
		if cap.TrackerID == "BTN" {
			btnCap = cap
			break
		}
	}
	if btnCap.TrackerID == "" {
		t.Fatal("expected BTN capability")
	}
	if !btnCap.RequiresAPIKey || !btnCap.SupportsCookieFile || !btnCap.SupportsLogin || !btnCap.SupportsAutoLogin || !btnCap.SupportsTOTP || !btnCap.SupportsManual2FA {
		t.Fatalf("unexpected BTN capability: %#v", btnCap)
	}

	status, err := service.Status(ctx, "BTN")
	if err != nil {
		t.Fatalf("Status api-key only: %v", err)
	}
	if status.State != StateLoginRequired && status.State != StateNotConfigured {
		t.Fatalf("BTN API-key-only must not be upload-ready, got %#v", status)
	}
	if !strings.Contains(status.Message, "imported cookies or login credentials required") {
		t.Fatalf("expected upload-auth message, got %#v", status)
	}

	cfg.Trackers.Trackers["BTN"] = config.TrackerConfig{Username: "user", Password: "pass"}
	service = NewService(cfg)
	status, err = service.Status(ctx, "BTN")
	if err != nil {
		t.Fatalf("Status credentials: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected BTN API key plus credentials configured, got %#v", status)
	}

	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	cfg.Trackers.Trackers["BTN"] = config.TrackerConfig{}
	service = NewService(cfg)
	status, err = service.Status(ctx, "BTN")
	if err != nil {
		t.Fatalf("Status cookies: %v", err)
	}
	if status.State != StateHasCookies || status.CookieCount != 1 {
		t.Fatalf("expected BTN stored cookies to remain local has_cookies, got %#v", status)
	}

	cfg.Metadata.BTNAPI = ""
	cfg.Trackers.Trackers["BTN"] = config.TrackerConfig{Username: "user", Password: "pass"}
	service = NewService(cfg)
	status, err = service.Status(ctx, "BTN")
	if err != nil {
		t.Fatalf("Status missing API key: %v", err)
	}
	if status.State != StateLoginRequired || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected missing API key to block BTN upload readiness, got %#v", status)
	}

	cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "missing-auth", "upbrr.db")
	service = NewService(cfg)
	status, err = service.Status(ctx, "BTN")
	if err != nil {
		t.Fatalf("Status missing API key without encrypted storage: %v", err)
	}
	if status.State != StateLoginRequired || status.EncryptedStorage || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected missing API key to take precedence over encrypted storage, got %#v", status)
	}
}

func TestValidateBTNStoredCookiesPromotesRemoteSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload.php" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=abc") {
			t.Fatalf("expected BTN session cookie, got %q", got)
		}
		_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Metadata:     config.MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {URL: server.URL},
		}},
	}).Validate(ctx, "BTN")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateConfigured || status.Message != "remote auth check succeeded" {
		t.Fatalf("expected successful BTN auth validation, got %#v", status)
	}
}

func TestValidateBTNRemoteSuccessRequiresAPIKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload.php" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=abc") {
			t.Fatalf("expected BTN session cookie, got %q", got)
		}
		_, _ = w.Write([]byte(`<form action="/upload.php"><input name="file_input" /></form>`))
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {URL: server.URL},
		}},
	}).Validate(ctx, "BTN")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateLoginRequired || status.CookieCount != 1 || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected validated BTN session to remain blocked by missing API key, got %#v", status)
	}
}

func TestValidateBTNInvalidCookiesDeletesOnlyConfirmedInvalid(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "expired"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload.php" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/login.php", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Metadata:     config.MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {URL: server.URL},
		}},
	}).Validate(ctx, "BTN")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateLoginRequired || status.CookieCount != 0 {
		t.Fatalf("expected confirmed-invalid BTN cookies to be deleted, got %#v", status)
	}
	if _, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "BTN"); err == nil {
		t.Fatal("expected BTN cookies to be deleted")
	}
}

func TestClassifyAdapterErrorRecognizesBTNSubmitted2FARejected(t *testing.T) {
	t.Parallel()

	err := classifyAdapterError("BTN", fmt.Errorf("login failed: %w", btn.ErrSubmitted2FARejected))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient || !validationErr.Submitted2FARejected || validationErr.ConfirmedInvalid {
		t.Fatalf("expected retry-visible BTN submitted 2FA rejection, got %v", err)
	}
}

func TestValidateRTFRefreshesExpiredAPIKey(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthTestDB(t)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"RTF": {APIKey: "old-token", Username: "user", Password: "pass"},
			},
		},
	}
	saveTrackerAuthTestConfig(t, dbPath, cfg)

	var testedToken string
	var loginCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/test":
			testedToken = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/login":
			loginCalled = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"new-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	cfg.Trackers.Trackers["RTF"] = config.TrackerConfig{
		URL:      server.URL,
		APIKey:   "old-token",
		Username: "user",
		Password: "pass",
	}

	status, err := NewService(cfg).Validate(context.Background(), "RTF")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateConfigured || status.Message != "remote auth check succeeded" {
		t.Fatalf("expected successful RTF auth validation, got %#v", status)
	}
	if testedToken != "old-token" {
		t.Fatalf("expected old token validation, got %q", testedToken)
	}
	if !loginCalled {
		t.Fatal("expected expired API key to trigger RTF login")
	}
	if got := loadStoredTrackerConfig(t, dbPath).Trackers.Trackers["RTF"].APIKey; got != "new-token" {
		t.Fatalf("expected refreshed token persisted, got %q", got)
	}
}

func TestValidateARStoredCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents.php" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=abc") {
			t.Fatalf("expected AR session cookie, got %q", got)
		}
		_, _ = w.Write([]byte(`<a href="/torrents.php?action=download&id=1&auth=session-key">Download</a>`))
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"AR": {URL: server.URL},
		}},
	}).Validate(ctx, "AR")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateConfigured || status.Message != "remote auth check succeeded" {
		t.Fatalf("expected successful AR auth validation, got %#v", status)
	}
}

func TestValidateHDBInvalidCookiesDeletesSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "HDB", map[string]string{"session": "expired"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload/upload" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/login.php", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"HDB": {URL: server.URL, Username: "user", Passkey: "passkey"},
		}},
	}).Validate(ctx, "HDB")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateLoginRequired || status.CookieCount != 0 || status.Message != "stored session expired or invalid" {
		t.Fatalf("expected HDB login-required expired-session status, got %#v", status)
	}
	if _, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "HDB"); err == nil {
		t.Fatal("expected invalid HDB cookies to be deleted")
	}
}

func TestValidateFFLoginPersistsCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "FF", map[string]string{"session": "expired"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			if cookie, err := r.Cookie("session"); err == nil && cookie.Value == "valid" {
				_, _ = w.Write([]byte(`<a href="friends.php">Friends</a>`))
				return
			}
			_, _ = w.Write([]byte(`<input name="username">`))
		case "/takelogin.php":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.FormValue("username") != "user" || r.FormValue("password") != "pass" {
				t.Fatalf("unexpected FF login form: %v", r.Form)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid", Path: "/"})
			w.Header().Set("Location", "/index.php")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"FF": {URL: server.URL, Username: "user", Password: "pass"},
			},
		},
	}).Validate(ctx, "FF")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected FF configured after login, got %#v", status)
	}
	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "FF")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "valid" {
		t.Fatalf("expected saved FF login cookies, got %#v", values)
	}
}

func TestValidateFLLoginPersistsCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "FL", map[string]string{"session": "expired"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login.php":
			_, _ = w.Write([]byte(`<input name="validator" value="token">`))
		case "/takelogin.php":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.FormValue("validator") != "token" || r.FormValue("username") != "user" || r.FormValue("password") != "pass" {
				t.Fatalf("unexpected FL login form: %v", r.Form)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid", Path: "/"})
			_, _ = w.Write([]byte("Logout"))
		case "/index.php":
			if cookie, err := r.Cookie("session"); err == nil && cookie.Value == "valid" {
				_, _ = w.Write([]byte("Logout"))
				return
			}
			_, _ = w.Write([]byte(`<input name="username">`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"FL": {URL: server.URL, Username: "user", Password: "pass"},
			},
		},
	}).Validate(ctx, "FL")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected FL configured after login, got %#v", status)
	}
	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "FL")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "valid" {
		t.Fatalf("expected saved FL login cookies, got %#v", values)
	}
}

func TestValidateTHRChecksCredentialLogin(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/takelogin.php" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.FormValue("username") != "user" || r.FormValue("password") != "pass" || r.FormValue("ssl") != "yes" {
			t.Fatalf("unexpected THR login form: %v", r.Form)
		}
		_, _ = w.Write([]byte(`<a href="logout.php">Logout</a>`))
	}))
	t.Cleanup(server.Close)

	status, err := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"THR": {URL: server.URL, Username: "user", Password: "pass"},
			},
		},
	}).Validate(context.Background(), "THR")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected THR configured after login check, got %#v", status)
	}
}

func TestSubmit2FARetriesAdapterLoginWithCurrentConfigAndCode(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthTestDB(t)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {
					Username:    "user",
					Password:    "pass",
					AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
				},
			},
		},
	}
	var gotCode, gotDBPath, gotUsername string
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "PTP",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "PTP"}
		},
		submit: func(_ context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error) {
			gotCode = req.Code
			gotDBPath = dbPath
			gotUsername = cfg.Username
			return Session{TrackerID: "PTP", State: SessionStateReady}, nil
		},
	}
	service := NewService(cfg)
	service.adapters = map[string]Adapter{"PTP": adapter}

	status, err := service.Login(context.Background(), "PTP", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if strings.TrimSpace(status.ChallengeID) == "" {
		t.Fatalf("expected challenge: %#v", status)
	}
	if _, err := service.Submit2FA(context.Background(), status.ChallengeID, "654321"); err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if gotCode != "654321" || gotDBPath != dbPath || gotUsername != "user" {
		t.Fatalf("unexpected adapter retry args code=%q db=%q username=%q", gotCode, gotDBPath, gotUsername)
	}
}

func TestSubmit2FABTNSuccessPreservesMissingAPIStatus(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthTestDB(t)
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {Username: "user", Password: "pass"},
			},
		},
	}
	var gotCode string
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "BTN",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "BTN"}
		},
		submit: func(_ context.Context, _ config.TrackerConfig, _ string, req api.TrackerAuthLoginRequest) (Session, error) {
			gotCode = req.Code
			return Session{TrackerID: "BTN", State: SessionStateReady}, nil
		},
	}
	service := NewService(cfg)
	service.adapters = map[string]Adapter{"BTN": adapter}

	status, err := service.Login(context.Background(), "BTN", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if strings.TrimSpace(status.ChallengeID) == "" {
		t.Fatalf("expected challenge: %#v", status)
	}
	status, err = service.Submit2FA(context.Background(), status.ChallengeID, "654321")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if gotCode != "654321" {
		t.Fatalf("unexpected submitted code %q", gotCode)
	}
	if status.Needs2FA || status.ChallengeID != "" {
		t.Fatalf("expected challenge cleared after successful submit, got %#v", status)
	}
	if status.State != StateLoginRequired || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected successful BTN 2FA to preserve missing API status, got %#v", status)
	}
}

func TestSubmit2FAFailureKeepsChallengeRetryVisible(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {
					Username:    "user",
					Password:    "pass",
					AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
				},
			},
		},
	}
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "PTP",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "PTP"}
		},
		submit: func(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) (Session, error) {
			return Session{}, &ValidationError{TrackerID: "PTP", Transient: true, Submitted2FARejected: true, Err: errors.New("login failed")}
		},
	}
	service := NewService(cfg)
	service.adapters = map[string]Adapter{"PTP": adapter}

	status, err := service.Login(context.Background(), "PTP", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	status, err = service.Submit2FA(context.Background(), status.ChallengeID, "000000")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if !status.Needs2FA || strings.TrimSpace(status.ChallengeID) == "" {
		t.Fatalf("expected retry-visible challenge after failed code, got %#v", status)
	}
}

func TestSubmit2FATransientSubmittedCodeFailureKeepsChallengeRetryVisible(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {
					Username:    "user",
					Password:    "pass",
					AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
				},
			},
		},
	}
	service := NewService(cfg)
	service.challenges = NewChallengeManager(defaultChallengeTTL)
	service.adapters = map[string]Adapter{
		"PTP": trackerAdapter{
			capability: api.TrackerAuthCapability{
				TrackerID:         "PTP",
				SupportsLogin:     true,
				SupportsManual2FA: true,
			},
			resolve: func(_ context.Context, _ config.TrackerConfig, _ string, login api.TrackerAuthLoginRequest) error {
				if login.Code != "000000" {
					t.Fatalf("expected submitted code, got %q", login.Code)
				}
				return fmt.Errorf("trackers: PTP login failed: %w", ptp.ErrSubmitted2FARejected)
			},
		},
	}
	ownerKey, err := service.challengeOwnerKey("PTP")
	if err != nil {
		t.Fatalf("challengeOwnerKey: %v", err)
	}
	challengeID := service.challenges.Create(context.Background(), "PTP", ownerKey)

	status, err := service.Submit2FA(context.Background(), challengeID, "000000")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if !status.Needs2FA || status.ChallengeID != challengeID {
		t.Fatalf("expected retry-visible challenge after classified failed code, got %#v", status)
	}
	if _, ok := service.challenges.Get(challengeID); !ok {
		t.Fatal("failed submitted code consumed challenge")
	}
}

func TestSubmit2FAPreCodeLoginFailureDoesNotExposeRetryChallenge(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {
					Username:    "user",
					Password:    "pass",
					AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
				},
			},
		},
	}
	service := NewService(cfg)
	service.challenges = NewChallengeManager(defaultChallengeTTL)
	service.adapters = map[string]Adapter{
		"PTP": trackerAdapter{
			capability: api.TrackerAuthCapability{
				TrackerID:         "PTP",
				SupportsLogin:     true,
				SupportsManual2FA: true,
			},
			resolve: func(_ context.Context, _ config.TrackerConfig, _ string, login api.TrackerAuthLoginRequest) error {
				if login.Code != "000000" {
					t.Fatalf("expected submitted code, got %q", login.Code)
				}
				return errors.New("trackers: PTP login failed")
			},
		},
	}
	ownerKey, err := service.challengeOwnerKey("PTP")
	if err != nil {
		t.Fatalf("challengeOwnerKey: %v", err)
	}
	challengeID := service.challenges.Create(context.Background(), "PTP", ownerKey)

	status, err := service.Submit2FA(context.Background(), challengeID, "000000")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if status.Needs2FA || strings.TrimSpace(status.ChallengeID) != "" {
		t.Fatalf("pre-code login failure must not expose retry challenge: %#v", status)
	}
	if !strings.Contains(status.LastError, "login failed") {
		t.Fatalf("expected login failure in status, got %#v", status)
	}
}

func TestSubmit2FAConfirmedInvalidFailureDoesNotExposeRetryChallenge(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {
					Username:    "user",
					Password:    "pass",
					AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
				},
			},
		},
	}
	service := NewService(cfg)
	service.challenges = NewChallengeManager(defaultChallengeTTL)
	service.adapters = map[string]Adapter{
		"PTP": trackerAdapter{
			capability: api.TrackerAuthCapability{
				TrackerID:         "PTP",
				SupportsLogin:     true,
				SupportsManual2FA: true,
			},
			resolve: func(_ context.Context, _ config.TrackerConfig, _ string, login api.TrackerAuthLoginRequest) error {
				if login.Code != "000000" {
					t.Fatalf("expected submitted code, got %q", login.Code)
				}
				return &ValidationError{TrackerID: "PTP", ConfirmedInvalid: true, Submitted2FARejected: true, Err: errors.New("login failed")}
			},
		},
	}
	ownerKey, err := service.challengeOwnerKey("PTP")
	if err != nil {
		t.Fatalf("challengeOwnerKey: %v", err)
	}
	challengeID := service.challenges.Create(context.Background(), "PTP", ownerKey)

	status, err := service.Submit2FA(context.Background(), challengeID, "000000")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if status.Needs2FA || strings.TrimSpace(status.ChallengeID) != "" {
		t.Fatalf("confirmed-invalid failure must not expose retry challenge: %#v", status)
	}
}

func TestSubmit2FAMTVSubmittedCodeAuthKeyFailureKeepsChallengeRetryVisible(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"MTV": {
					Username: "user",
					Password: "pass",
				},
			},
		},
	}
	service := NewService(cfg)
	service.challenges = NewChallengeManager(defaultChallengeTTL)
	service.adapters = map[string]Adapter{
		"MTV": trackerAdapter{
			capability: api.TrackerAuthCapability{
				TrackerID:         "MTV",
				SupportsLogin:     true,
				SupportsManual2FA: true,
			},
			resolve: func(_ context.Context, _ config.TrackerConfig, _ string, login api.TrackerAuthLoginRequest) error {
				if login.Code != "000000" {
					t.Fatalf("expected submitted code, got %q", login.Code)
				}
				return fmt.Errorf("trackers: MTV auth key not found: %w", mtv.ErrSubmitted2FARejected)
			},
		},
	}
	ownerKey, err := service.challengeOwnerKey("MTV")
	if err != nil {
		t.Fatalf("challengeOwnerKey: %v", err)
	}
	challengeID := service.challenges.Create(context.Background(), "MTV", ownerKey)

	status, err := service.Submit2FA(context.Background(), challengeID, "000000")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if !status.Needs2FA || status.ChallengeID != challengeID {
		t.Fatalf("expected retry-visible MTV challenge after auth-key miss, got %#v", status)
	}
	if _, ok := service.challenges.Get(challengeID); !ok {
		t.Fatal("failed MTV submitted code consumed challenge")
	}
}

func TestSubmit2FATransientParserFailureDoesNotExposeRetryChallenge(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"MTV": {Username: "user", Password: "pass"},
			},
		},
	}
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "MTV",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "MTV"}
		},
		submit: func(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) (Session, error) {
			return Session{}, classifyAdapterError("MTV", errors.New("trackers: MTV 2FA token not found"))
		},
	}
	service := NewService(cfg)
	service.adapters = map[string]Adapter{"MTV": adapter}

	status, err := service.Login(context.Background(), "MTV", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	status, err = service.Submit2FA(context.Background(), status.ChallengeID, "000000")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if status.Needs2FA || strings.TrimSpace(status.ChallengeID) != "" {
		t.Fatalf("transient parser failure must not expose retry challenge: %#v", status)
	}
	if !strings.Contains(status.LastError, "2FA token not found") {
		t.Fatalf("expected parser failure in status, got %#v", status)
	}
}

func TestSubmit2FARejectsChallengeAfterTrackerConfigReplacement(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {Username: "user", Password: "pass"},
			},
		},
	}
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "PTP",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "PTP"}
		},
	}
	loginService := NewService(cfg)
	loginService.adapters = map[string]Adapter{"PTP": adapter}

	status, err := loginService.Login(context.Background(), "PTP", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	replacedCfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {Username: "other", Password: "pass"},
			},
		},
	}
	submitService := NewService(replacedCfg)
	submitService.adapters = map[string]Adapter{"PTP": adapter}
	_, err = submitService.Submit2FA(context.Background(), status.ChallengeID, "123456")
	if err == nil {
		t.Fatal("expected stale challenge error")
	}
	if !strings.Contains(err.Error(), "stale manual 2FA challenge") {
		t.Fatalf("expected stale challenge error, got %v", err)
	}
}

func TestSubmit2FAUsesCanceledContextWithoutConsumingChallenge(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {Username: "user", Password: "pass"},
			},
		},
	}
	challenges := NewChallengeManager(defaultChallengeTTL)
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "PTP",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		submit: func(ctx context.Context, _ config.TrackerConfig, _ string, _ api.TrackerAuthLoginRequest) (Session, error) {
			return Session{}, ctx.Err()
		},
	}
	service := NewService(cfg)
	service.adapters = map[string]Adapter{"PTP": adapter}
	service.challenges = challenges
	ownerKey, err := service.challengeOwnerKey("PTP")
	if err != nil {
		t.Fatalf("challengeOwnerKey: %v", err)
	}
	challengeID := challenges.Create(context.Background(), "PTP", ownerKey)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, err := service.Submit2FA(ctx, challengeID, "123456")
	if err != nil {
		t.Fatalf("Submit2FA: %v", err)
	}
	if !strings.Contains(status.LastError, "context canceled") {
		t.Fatalf("expected context canceled status, got %#v", status)
	}
	if _, ok := challenges.Get(challengeID); !ok {
		t.Fatal("canceled 2FA submit consumed the challenge")
	}
}

func TestLoginMissingCredentialsReturnsLoginRequiredWithoutChallenge(t *testing.T) {
	t.Parallel()

	status, err := NewService(config.Config{}).Login(
		context.Background(),
		"PTP",
		api.TrackerAuthLoginRequest{},
	)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if status.State != StateLoginRequired {
		t.Fatalf("expected login required status, got %#v", status)
	}
	if status.Needs2FA || strings.TrimSpace(status.ChallengeID) != "" {
		t.Fatalf("missing credentials must not create manual 2FA challenge: %#v", status)
	}
}

func TestLoginBTNCredentialsWithoutAPIAttemptsSessionValidation(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: newTrackerAuthTestDB(t)},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {Username: "user", Password: "pass"},
			},
		},
	}
	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "BTN", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, &AuthRequiredError{TrackerID: "BTN", Reason: "login required"}
		},
		login: func() (Session, error) {
			return Session{TrackerID: "BTN", State: SessionStateReady}, nil
		},
	}
	service := NewService(cfg)
	service.adapters = map[string]Adapter{"BTN": adapter}

	status, err := service.Login(context.Background(), "BTN", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if adapter.validateCalls != 1 || adapter.loginCalls != 1 {
		t.Fatalf("expected BTN session validation and login, got validate=%d login=%d", adapter.validateCalls, adapter.loginCalls)
	}
	if status.State != StateLoginRequired || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected successful BTN login to preserve missing API status, got %#v", status)
	}
}

func TestStatusConfiguredOTPURIAvoidsManualChallenge(t *testing.T) {
	t.Parallel()

	tests := map[string]config.TrackerConfig{
		"configured otp uri": {
			Username:    "user",
			Password:    "pass",
			AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
			OTPURI:      "otpauth://totp/PTP:user?secret=ABC",
		},
		"missing otp uri": {
			Username:    "user",
			Password:    "pass",
			AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
		},
	}
	for name, trackerCfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service := NewService(config.Config{
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{
						"PTP": trackerCfg,
					},
				},
			})
			status := service.statusForSpec(context.Background(), trackerSpec{
				id:               "PTP",
				login:            true,
				totp:             true,
				manual2FA:        true,
				needsCredentials: true,
			})
			if status.State != StateConfigured {
				t.Fatalf("expected configured status, got %#v", status)
			}
			if status.Needs2FA || strings.TrimSpace(status.ChallengeID) != "" {
				t.Fatalf("status-only config must not expose manual challenge path: %#v", status)
			}
		})
	}
}

func TestStatusPTPRequiresAnnounceURLForConfiguredLogin(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg       config.TrackerConfig
		wantState string
	}{
		"missing announce": {
			cfg:       config.TrackerConfig{Username: "user", Password: "pass"},
			wantState: StateLoginRequired,
		},
		"blank announce": {
			cfg:       config.TrackerConfig{Username: "user", Password: "pass", AnnounceURL: " \t\n "},
			wantState: StateLoginRequired,
		},
		"complete login config": {
			cfg:       config.TrackerConfig{Username: "user", Password: "pass", AnnounceURL: "https://please.passthepopcorn.me/passkey/announce"},
			wantState: StateConfigured,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service := NewService(config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: newTrackerAuthTestDB(t)},
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{"PTP": tt.cfg},
				},
			})
			status, err := service.Status(context.Background(), "PTP")
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if status.State != tt.wantState {
				t.Fatalf("expected %s state, got %#v", tt.wantState, status)
			}
		})
	}
}

func TestStatusPTPCookiesRemainConfiguredWithoutAnnounceURL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{"PTP": {Username: "user", Password: "pass"}},
		},
	})

	status, err := service.Status(ctx, "PTP")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateHasCookies || status.CookieCount != 1 {
		t.Fatalf("expected stored PTP cookies to remain usable without announce URL, got %#v", status)
	}
}

func TestStatusConfiguredAuthReportsEncryptedStorageUnavailableWhenPersistenceRequired(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg       config.TrackerConfig
		spec      trackerSpec
		wantState string
	}{
		"MTV api key only": {
			cfg:       config.TrackerConfig{APIKey: "api-key"},
			wantState: StateEncryptedStorageUnavailable,
			spec: trackerSpec{
				id:               "MTV",
				cookies:          true,
				apiKey:           true,
				needsCredentials: true,
			},
		},
		"credentials": {
			cfg:       config.TrackerConfig{Username: "user", Password: "pass"},
			wantState: StateEncryptedStorageUnavailable,
			spec: trackerSpec{
				id:               "AR",
				cookies:          true,
				login:            true,
				needsCredentials: true,
			},
		},
		"passkey": {
			cfg:       config.TrackerConfig{Passkey: "passkey"},
			wantState: StateEncryptedStorageUnavailable,
			spec: trackerSpec{
				id:      "HDB",
				cookies: true,
				passkey: true,
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service := NewService(config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")},
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{tt.spec.id: tt.cfg},
				},
			})
			status := service.statusForSpec(context.Background(), tt.spec)
			if status.State != tt.wantState {
				t.Fatalf("expected %s state, got %#v", tt.wantState, status)
			}
			if status.EncryptedStorage {
				t.Fatalf("expected storage availability to remain visible as false: %#v", status)
			}
		})
	}
}

func TestStatusConfiguredAuthReportsCoexistingCookies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		trackerID string
		cfg       config.TrackerConfig
	}{
		"api key": {trackerID: "MTV", cfg: config.TrackerConfig{APIKey: "api-key"}},
		"passkey": {trackerID: "HDB", cfg: config.TrackerConfig{Username: "user", Passkey: "passkey"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dbPath := newTrackerAuthTestDB(t)
			if err := cookies.SaveTrackerCookieMap(ctx, dbPath, tt.trackerID, map[string]string{"session": "abc"}); err != nil {
				t.Fatalf("SaveTrackerCookieMap: %v", err)
			}
			service := NewService(config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: dbPath},
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{tt.trackerID: tt.cfg},
				},
			})

			status, err := service.Status(ctx, tt.trackerID)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if status.State != StateConfigured || status.CookieCount != 1 {
				t.Fatalf("expected configured state with cookie count, got %#v", status)
			}
			if !strings.Contains(status.Message, "stored cookie") {
				t.Fatalf("expected message to preserve cookie presence, got %#v", status)
			}
		})
	}
}

func TestStatusHDBPasskeyWithoutCookiesIsNotUploadReady(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthTestDB(t)
	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{"HDB": {Username: "user", Passkey: "passkey"}},
		},
	})

	status, err := service.Status(context.Background(), "HDB")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateLoginRequired {
		t.Fatalf("expected HDB cookie import requirement, got %#v", status)
	}
}

func TestStatusMTVAPIKeyOnlyReportsUploadNotReady(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthTestDB(t)
	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{"MTV": {APIKey: "api-key"}},
		},
	})

	status, err := service.Status(context.Background(), "MTV")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateLoginRequired {
		t.Fatalf("expected upload auth to be login-required, got %#v", status)
	}
	if !strings.Contains(status.Message, "API key covers Torznab/search") || !strings.Contains(status.Message, "required for upload auth") {
		t.Fatalf("expected split search/upload message, got %#v", status)
	}
}

func TestStatusMTVCredentialsWithoutAPIKeyRemainUploadReady(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthTestDB(t)
	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{"MTV": {Username: "user", Password: "pass"}},
		},
	})

	status, err := service.Status(context.Background(), "MTV")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected MTV credentials to remain upload-ready, got %#v", status)
	}
}

func TestStatusCookiesOnlyReportsEncryptedStorageUnavailable(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")},
	})
	status := service.statusForSpec(context.Background(), trackerSpec{
		id:      "ASC",
		cookies: true,
	})
	if status.State != StateEncryptedStorageUnavailable {
		t.Fatalf("expected encrypted storage unavailable, got %#v", status)
	}
	if status.EncryptedStorage {
		t.Fatalf("expected storage availability to remain visible as false: %#v", status)
	}
}

func TestRTFStatusTreatsCredentialsAsRefreshAuth(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"RTF": {Username: "user", Password: "pass"},
			},
		},
	})

	status, err := service.Status(context.Background(), "RTF")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("RTF credentials should report refresh auth configured without api_key: %#v", status)
	}
}

func TestTHRDoesNotAdvertiseCookieImport(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{})
	caps, err := service.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	for _, cap := range caps {
		if cap.TrackerID != "THR" {
			continue
		}
		if cap.SupportsCookieFile {
			t.Fatalf("THR upload logs in per request and must not advertise DB cookie import: %#v", cap)
		}
		return
	}
	t.Fatal("THR capability not found")
}

func TestFFAdvertisesCookieImportWithRemoteLoginAction(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{})
	caps, err := service.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	for _, cap := range caps {
		if cap.TrackerID != "FF" {
			continue
		}
		if !cap.SupportsCookieFile {
			t.Fatalf("FF upload can use DB cookies and must advertise cookie import: %#v", cap)
		}
		if !cap.SupportsLogin || !cap.SupportsAutoLogin {
			t.Fatalf("FF tracker-auth adapter must advertise login actions: %#v", cap)
		}
		return
	}
	t.Fatal("FF capability not found")
}

func TestValidateWithoutAdapterReportsUnsupportedRemoteValidation(t *testing.T) {
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ASC": {},
			},
		},
	}
	status, err := NewService(cfg).Validate(context.Background(), "ASC")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if strings.Contains(status.Message, "succeeded") {
		t.Fatalf("unexpected remote success message: %#v", status)
	}
	if !strings.Contains(status.Message, "not supported") {
		t.Fatalf("expected unsupported remote validation message, got %#v", status)
	}
}

func TestParseCookieContentJSONMap(t *testing.T) {
	got, err := ParseCookieContent("MTV.json", `{"session":"abc","nested":{"value":"def"}}`)
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "abc" || got["nested"] != "def" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestParseCookieContentJSONArray(t *testing.T) {
	got, err := ParseCookieContent("PTP.json", `[
		{"domain":".example.test","name":"session","value":"abc"},
		{"name":"token","value":"latest"},
		{"name":"empty","value":""},
		{"name":"","value":"ignored"}
	]`)
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "abc" || got["token"] != "latest" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
	if _, ok := got["empty"]; ok {
		t.Fatalf("empty cookie value should be ignored: %#v", got)
	}
}

func TestParseCookieContentJSONArrayWithoutJSONExtension(t *testing.T) {
	got, err := ParseCookieContent("PTP.txt", `[
		{"domain":".example.test","name":"session","value":"abc"}
	]`)
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "abc" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestParseCookieContentNetscape(t *testing.T) {
	got, err := ParseCookieContent("PTP.txt", ".example.test\tTRUE\t/\tTRUE\t0\tsession\tabc\n")
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "abc" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestParseCookieContentRejectsTrimmedObjectKeyCollision(t *testing.T) {
	t.Parallel()

	_, err := ParseCookieContent("cookies.json", `{"session":"abc"," session ":"def"}`)
	if err == nil {
		t.Fatal("expected trimmed key collision error")
	}
	if !strings.Contains(err.Error(), "duplicate cookie name") {
		t.Fatalf("expected duplicate cookie name error, got %v", err)
	}
}

func TestParseCookieContentRejectsDuplicateJSONObjectNamesBeforeMapCollapse(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"root exact":           `{"session":"abc","session":"def"}`,
		"root escaped":         `{"session":"abc","\u0073ession":"def"}`,
		"nested value exact":   `{"session":{"value":"abc","value":"def"}}`,
		"nested value escaped": `{"session":{"value":"abc","\u0076alue":"def"}}`,
		"array name exact":     `[{"name":"session","name":"token","value":"abc"}]`,
		"array value escaped":  `[{"name":"session","value":"abc","\u0076alue":"def"}]`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseCookieContent("cookies.json", content)
			if err == nil {
				t.Fatal("expected duplicate error")
			}
			if !strings.Contains(err.Error(), "duplicate cookie name") {
				t.Fatalf("expected duplicate cookie name error, got %v", err)
			}
		})
	}
}

func TestParseCookieContentAllowsNestedNonCookieDuplicateJSONKeys(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"object metadata": `{"session":{"value":"abc","metadata":{"same":"first","same":"second"}}}`,
		"array metadata":  `[{"name":"session","value":"abc","metadata":{"same":"first","same":"second"}}]`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCookieContent("cookies.json", content)
			if err != nil {
				t.Fatalf("ParseCookieContent: %v", err)
			}
			if got["session"] != "abc" {
				t.Fatalf("unexpected cookies: %#v", got)
			}
		})
	}
}

func TestParseCookieContentValidJSONModesUnchanged(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		fileName string
		content  string
		want     map[string]string
	}{
		"root map": {
			fileName: "cookies.json",
			content:  `{"session":"abc","nested":{"value":"def"}}`,
			want:     map[string]string{"session": "abc", "nested": "def"},
		},
		"array": {
			fileName: "cookies.json",
			content:  `[{"name":"session","value":"abc"},{"name":"token","value":"def"}]`,
			want:     map[string]string{"session": "abc", "token": "def"},
		},
		"txt array": {
			fileName: "cookies.txt",
			content:  `[{"name":"session","value":"abc"}]`,
			want:     map[string]string{"session": "abc"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCookieContent(tt.fileName, tt.content)
			if err != nil {
				t.Fatalf("ParseCookieContent: %v", err)
			}
			for key, want := range tt.want {
				if got[key] != want {
					t.Fatalf("unexpected cookie %s: got %#v want %q", key, got, want)
				}
			}
		})
	}
}

func TestParseCookieContentPreservesCookieValueWhitespace(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		fileName string
		content  string
	}{
		"JSON object": {
			fileName: "cookies.json",
			content:  `{"session":" abc "}`,
		},
		"JSON array": {
			fileName: "cookies.json",
			content:  `[{"name":"session","value":" abc "}]`,
		},
		"Netscape": {
			fileName: "cookies.txt",
			content:  ".example.test\tTRUE\t/\tTRUE\t0\tsession\t abc ",
		},
		"Netscape HttpOnly": {
			fileName: "cookies.txt",
			content:  "#HttpOnly_.example.test\tTRUE\t/\tTRUE\t0\tsession\t abc ",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCookieContent(tt.fileName, tt.content)
			if err != nil {
				t.Fatalf("ParseCookieContent: %v", err)
			}
			if got["session"] != " abc " {
				t.Fatalf("cookie value was normalized: %#v", got)
			}
		})
	}
}

func TestApplyEnsureErrorToStatusDoesNotExposeEmpty2FAChallenge(t *testing.T) {
	t.Parallel()

	status := api.TrackerAuthStatus{TrackerID: "PTP", State: StateConfigured}
	applyEnsureErrorToStatus(&status, &Needs2FAError{TrackerID: "PTP", Reason: "2FA required"})

	if status.Needs2FA {
		t.Fatalf("empty challenge must not enable 2FA submission: %#v", status)
	}
	if status.ChallengeID != "" {
		t.Fatalf("empty challenge must not set challenge id: %#v", status)
	}
	if status.State != StateLoginRequired {
		t.Fatalf("expected login required state, got %#v", status)
	}
}

func TestApplyEnsureErrorToStatusRedactsURLPath(t *testing.T) {
	t.Parallel()

	status := api.TrackerAuthStatus{TrackerID: "MTV", State: StateConfigured}
	applyEnsureErrorToStatus(&status, errors.New(`Get "https://www.morethantv.me/secret-login-token?passkey=abc": auth key not found`))

	if strings.Contains(status.LastError, "secret-login-token") || strings.Contains(status.LastError, "abc") {
		t.Fatalf("LastError leaked URL secret: %#v", status)
	}
	if !strings.Contains(status.LastError, "https://www.morethantv.me/[REDACTED]") {
		t.Fatalf("LastError lost useful URL host context: %#v", status)
	}
}

func TestCookiesToMapPreservesCookieValueWhitespace(t *testing.T) {
	t.Parallel()

	got := CookiesToMap([]*http.Cookie{{Name: " session ", Value: " abc "}})
	if got["session"] != " abc " {
		t.Fatalf("cookie value was normalized: %#v", got)
	}
}

func TestParseCookieContentInvalidLeadingArrayReportsJSONError(t *testing.T) {
	t.Parallel()

	_, err := ParseCookieContent("PTP.txt", `[{"name":"session","value":"abc"}`)
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
	if !strings.Contains(err.Error(), "parse JSON cookies") {
		t.Fatalf("expected JSON parse error, got %v", err)
	}
}

func TestParseCookieContentRejectsDuplicateNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		fileName string
		content  string
	}{
		"JSON array": {
			fileName: "cookies.json",
			content:  `[{"name":"session","value":"abc"},{"name":"session","value":"def"}]`,
		},
		"Netscape": {
			fileName: "cookies.txt",
			content:  ".example.test\tTRUE\t/\tTRUE\t0\tsession\tabc\n.example.test\tTRUE\t/\tTRUE\t0\tsession\tdef\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseCookieContent(tt.fileName, tt.content)
			if err == nil {
				t.Fatal("expected duplicate error")
			}
			if !strings.Contains(err.Error(), "duplicate cookie name") {
				t.Fatalf("expected duplicate error, got %v", err)
			}
		})
	}
}

func TestParseCookieContentNetscapeOversizedValidLine(t *testing.T) {
	value := strings.Repeat("a", 70*1024)
	got, err := ParseCookieContent("PTP.txt", ".example.test\tTRUE\t/\tTRUE\t0\tsession\t"+value)
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != value {
		t.Fatalf("unexpected oversized cookie value length: got %d want %d", len(got["session"]), len(value))
	}
}

func TestParseCookieContentNetscapeOversizedMalformedLineHasNoEntries(t *testing.T) {
	_, err := ParseCookieContent("PTP.txt", strings.Repeat("x", 70*1024))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no entries") {
		t.Fatalf("expected no entries error, got %v", err)
	}
}

func TestParseCookieContentJSONRejectsInvalidShapes(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"invalid json":  "{",
		"non-object":    `[{"name":"session","value":"abc"},"bad"]`,
		"missing name":  `[{"value":"abc"}]`,
		"missing value": `[{"name":"session"}]`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCookieContent("cookies.json", payload); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestImportCookiesRejectsMalformedArrayEntryWithoutReplacingCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	_, err := service.ImportCookies(ctx, "AR", "cookies.json", `[
		{"name":"session","value":"replacement"},
		{"name":"token"}
	]`)
	if err == nil {
		t.Fatal("expected malformed array entry error")
	}
	if !strings.Contains(err.Error(), "require name and value") {
		t.Fatalf("expected missing value error, got %v", err)
	}

	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("existing cookies changed after failed import: %#v", values)
	}
}

func TestImportCookiesRejectsOverCapWithoutReplacingCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	_, err := service.ImportCookies(ctx, "AR", "cookies.txt", strings.Repeat("x", MaxCookieImportContentBytes+1))
	if err == nil {
		t.Fatal("expected over-cap import error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}

	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("existing cookies changed after failed import: %#v", values)
	}
}

func TestImportCookiesReportsMergedCookieCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	legacyPath, err := servicedb.CookiePath(dbPath, "AR.txt")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(".example.test\tTRUE\t/\tTRUE\t0\tlegacy\tfrom-file\n"), 0o600); err != nil {
		t.Fatalf("write legacy cookies: %v", err)
	}
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	status, err := service.ImportCookies(ctx, "AR", "cookies.json", `{"session":"from-db"}`)
	if err != nil {
		t.Fatalf("ImportCookies: %v", err)
	}
	if status.CookieCount != 2 {
		t.Fatalf("expected merged cookie count, got %#v", status)
	}
}

func TestImportCookiesPreservesBTNMissingAPIStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {},
		}},
	})

	status, err := service.ImportCookies(ctx, "BTN", "cookies.json", `{"session":"abc"}`)
	if err != nil {
		t.Fatalf("ImportCookies: %v", err)
	}
	if status.State != StateLoginRequired || status.CookieCount != 1 || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected missing BTN API to survive cookie import, got %#v", status)
	}
}

func TestCapabilitiesAdvertiseOnlySupportedManual2FA(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{})
	caps, err := service.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	for _, cap := range caps {
		switch cap.TrackerID {
		case "MTV", "PTP":
			if !cap.SupportsManual2FA {
				t.Fatalf("%s must advertise supported manual 2FA", cap.TrackerID)
			}
			if !cap.SupportsTOTP {
				t.Fatalf("%s TOTP auto-login capability must be preserved", cap.TrackerID)
			}
			if !cap.SupportsLogin || !cap.SupportsAutoLogin {
				t.Fatalf("%s adapter-backed login capability must be preserved: %#v", cap.TrackerID, cap)
			}
		case "RTF":
			if !cap.SupportsLogin || !cap.SupportsAutoLogin {
				t.Fatalf("%s API refresh capability must be preserved: %#v", cap.TrackerID, cap)
			}
			if cap.SupportsManual2FA || cap.SupportsTOTP {
				t.Fatalf("%s must not advertise 2FA actions: %#v", cap.TrackerID, cap)
			}
		case "AR":
			if cap.SupportsLogin || cap.SupportsAutoLogin || cap.SupportsManual2FA {
				t.Fatalf("%s auth check is validation-only in tracker-auth UI: %#v", cap.TrackerID, cap)
			}
			if !cap.SupportsCookieFile {
				t.Fatalf("%s must keep cookie import capability: %#v", cap.TrackerID, cap)
			}
		case "HDB":
			if cap.SupportsLogin || cap.SupportsAutoLogin || cap.SupportsManual2FA {
				t.Fatalf("%s auth check is validation-only in tracker-auth UI: %#v", cap.TrackerID, cap)
			}
			if !cap.SupportsCookieFile || !cap.RequiresPasskey {
				t.Fatalf("%s must keep cookie/passkey capability: %#v", cap.TrackerID, cap)
			}
		case "FF", "FL":
			if !cap.SupportsLogin || !cap.SupportsAutoLogin {
				t.Fatalf("%s adapter-backed login capability must be preserved: %#v", cap.TrackerID, cap)
			}
			if !cap.SupportsCookieFile || cap.SupportsManual2FA {
				t.Fatalf("%s must advertise cookie import without manual 2FA: %#v", cap.TrackerID, cap)
			}
		case "THR":
			if !cap.SupportsLogin || !cap.SupportsAutoLogin {
				t.Fatalf("%s adapter-backed login capability must be preserved: %#v", cap.TrackerID, cap)
			}
			if cap.SupportsCookieFile || cap.SupportsManual2FA {
				t.Fatalf("%s must advertise stateless login without cookie import or 2FA: %#v", cap.TrackerID, cap)
			}
		case "ASC":
			if cap.SupportsLogin || cap.SupportsAutoLogin || cap.SupportsManual2FA {
				t.Fatalf("%s must not advertise unsupported login actions: %#v", cap.TrackerID, cap)
			}
		}
	}
}

func TestBuiltInSpecsOnlyReferenceKnownTrackers(t *testing.T) {
	t.Parallel()

	for _, spec := range builtInSpecs() {
		if !trackers.IsKnownTracker(spec.id) {
			t.Fatalf("built-in tracker auth spec references unknown tracker %s", spec.id)
		}
	}
}

func TestTrackerAuthLogsOperationResultsWithoutSecrets(t *testing.T) {
	t.Parallel()

	logger := &trackerAuthRecordingLogger{}
	service := NewServiceWithLogger(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: newTrackerAuthTestDB(t)},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"MTV": {APIKey: "secret-api-key", Username: "secret-user", Password: "secret-password"},
			},
		},
	}, logger)

	status, err := service.Status(context.Background(), "MTV")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.TrackerID != "MTV" {
		t.Fatalf("expected MTV status, got %#v", status)
	}
	if _, err := service.ImportCookies(context.Background(), "AR", "cookies.json", "{bad"); err == nil {
		t.Fatal("expected invalid cookie import to fail")
	}

	infoLog := strings.Join(logger.info, "\n")
	warnLog := strings.Join(logger.warn, "\n")
	allLogs := infoLog + "\n" + warnLog
	if !strings.Contains(infoLog, "tracker auth: status checked tracker=MTV") {
		t.Fatalf("expected status info log, got info=%q warn=%q", infoLog, warnLog)
	}
	if !strings.Contains(warnLog, "tracker auth: cookie import failed tracker=AR bytes=4") {
		t.Fatalf("expected import warning log, got info=%q warn=%q", infoLog, warnLog)
	}
	for _, secret := range []string{"secret-api-key", "secret-user", "secret-password", "{bad"} {
		if strings.Contains(allLogs, secret) {
			t.Fatalf("tracker auth log leaked %q: %s", secret, allLogs)
		}
	}
}

func TestTrackerAuthWarningStatusDoesNotLogSuccess(t *testing.T) {
	t.Parallel()

	logger := &trackerAuthRecordingLogger{}
	service := NewServiceWithLogger(config.Config{}, logger)
	service.logStatus("login completed", api.TrackerAuthStatus{
		TrackerID: "MTV",
		State:     StateConfigured,
		LastError: "tracker auth: MTV: validation failed",
	})

	infoLog := strings.Join(logger.info, "\n")
	warnLog := strings.Join(logger.warn, "\n")
	if strings.Contains(infoLog, "tracker auth: login completed") {
		t.Fatalf("warning status logged success info: info=%q warn=%q", infoLog, warnLog)
	}
	if !strings.Contains(warnLog, "tracker auth: login completed warning tracker=MTV") {
		t.Fatalf("expected warning log, got info=%q warn=%q", infoLog, warnLog)
	}
}

func TestTrackerAuthRejectsCaseCollidingConfigIDs(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ar": {Username: "user", Password: "pass"},
				"AR": {APIKey: "api-key"},
			},
		},
	})

	if _, err := service.Capabilities(context.Background()); err == nil {
		t.Fatal("expected capabilities to reject duplicate tracker ids")
	} else if !strings.Contains(err.Error(), "duplicate tracker config id") {
		t.Fatalf("expected duplicate tracker id error, got %v", err)
	}
	if _, err := service.Status(context.Background(), "AR"); err == nil {
		t.Fatal("expected status to reject duplicate tracker ids")
	} else if !strings.Contains(err.Error(), "duplicate tracker config id") {
		t.Fatalf("expected duplicate tracker id error, got %v", err)
	}
	if _, err := service.Delete(context.Background(), "AR"); err == nil {
		t.Fatal("expected delete to reject duplicate tracker ids")
	} else if !strings.Contains(err.Error(), "duplicate tracker config id") {
		t.Fatalf("expected duplicate tracker id error, got %v", err)
	}
}

func TestTrackerAuthKeepsCaseInsensitiveSingleConfigLookup(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"ar": {Username: "user", Password: "pass"},
			},
		},
	})

	status, err := service.Status(context.Background(), "AR")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateEncryptedStorageUnavailable {
		t.Fatalf("expected single case variant to preserve storage readiness, got %#v", status)
	}
}

func TestTrackerAuthKeepsCustomUnicodeConfigLookupCanonical(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"åar": {Username: "user", Password: "pass"},
			},
		},
	})

	status, err := service.Status(context.Background(), "åar")
	if err != nil {
		t.Fatalf("Status lowercase Unicode tracker: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected custom Unicode tracker to remain configured, got %#v", status)
	}
	if _, err := service.Status(context.Background(), "Åar"); err == nil {
		t.Fatal("expected Unicode-folded tracker id to remain unknown")
	}
}

func TestTrackerAuthRejectsASCIICollidingUnicodeConfigIDs(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"åar": {Username: "user", Password: "pass"},
				"åAR": {APIKey: "api-key"},
			},
		},
	})

	if _, err := service.Capabilities(context.Background()); err == nil {
		t.Fatal("expected capabilities to reject duplicate custom Unicode tracker ids")
	} else if !strings.Contains(err.Error(), "duplicate tracker config id") {
		t.Fatalf("expected duplicate tracker id error, got %v", err)
	}
}

func TestTrackerAuthDoesNotFoldUnicodeLookalikeTrackerIDs(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"K": {Username: "user", Password: "pass"},
			},
		},
	})

	if _, err := service.Status(context.Background(), "K"); err != nil {
		t.Fatalf("Status ASCII tracker: %v", err)
	}
	if _, err := service.Status(context.Background(), "\u212a"); err == nil {
		t.Fatal("expected Unicode lookalike tracker id to be unknown")
	}
}

func TestDeleteARAuthClearsCookiesAuthStateAndLegacyAuth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := SaveAuthState(ctx, dbPath, "AR", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	legacyPath, err := servicedb.CookiePath(dbPath, "AR_auth.txt")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy-auth-key"), 0o600); err != nil {
		t.Fatalf("write legacy auth: %v", err)
	}

	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})
	status, err := service.Delete(ctx, "AR")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if status.CookieCount != 0 {
		t.Fatalf("expected zero cookies after delete, got %#v", status)
	}
	if _, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "AR"); err == nil {
		t.Fatal("expected AR cookies to be deleted")
	}
	if _, err := LoadAuthState(ctx, dbPath, "AR", "auth_key"); !errors.Is(err, ErrAuthStateNotFound) {
		t.Fatalf("expected AR auth state to be deleted, got %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy AR auth to be deleted, got %v", err)
	}
}

func TestDeleteARAuthStatusRefreshIgnoresCancellationAfterMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	deleteCtx, cancel := context.WithCancel(context.Background())
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})
	service.afterDeleteCleanup = cancel

	status, err := service.Delete(deleteCtx, "AR")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleteCtx.Err() == nil {
		t.Fatal("expected delete context to be canceled before status refresh")
	}
	if status.CookieCount != 0 || status.Message != "stored auth deleted" {
		t.Fatalf("expected truthful delete status after cancellation, got %#v", status)
	}
	if _, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "AR"); err == nil {
		t.Fatal("expected AR cookies to be deleted")
	}
}

func TestDeleteARAuthLegacyPathFailureReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := SaveAuthState(ctx, dbPath, "AR", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	cookiesDir := filepath.Join(filepath.Dir(dbPath), "cookies")
	if err := os.WriteFile(cookiesDir, []byte("blocks cookie path"), 0o600); err != nil {
		t.Fatalf("write cookie path blocker: %v", err)
	}
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	_, err := service.Delete(ctx, "AR")
	if err == nil {
		t.Fatal("expected legacy auth path resolution error")
	}
	if !strings.Contains(err.Error(), "resolve AR legacy auth key path") {
		t.Fatalf("expected legacy path resolution error, got %v", err)
	}
	authKey, err := LoadAuthState(ctx, dbPath, "AR", "auth_key")
	if err != nil {
		t.Fatalf("expected AR auth state to be restored: %v", err)
	}
	if authKey != "secret-auth-key" {
		t.Fatalf("unexpected restored auth state")
	}
}

func TestDeleteARAuthWithoutWebAuthMaterialDeletesStaleAuth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		t.Fatalf("MigrateContext: %v", err)
	}
	_ = repo.Close()
	legacyPath, err := servicedb.CookiePath(dbPath, "AR_auth.txt")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy-auth-key"), 0o600); err != nil {
		t.Fatalf("write legacy auth: %v", err)
	}
	cookiePath, err := servicedb.CookiePath(dbPath, "AR.txt")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.WriteFile(cookiePath, []byte(".alpharatio.cc\tTRUE\t/\tTRUE\t0\tsession\tabc\n"), 0o600); err != nil {
		t.Fatalf("write legacy cookies: %v", err)
	}
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	_, err = service.Delete(ctx, "AR")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy AR auth deleted, got %v", err)
	}
	if _, err := os.Stat(cookiePath); !os.IsNotExist(err) {
		t.Fatalf("expected cookies deleted, got %v", err)
	}
}

func TestDeleteARCookieFailureRestoresAuthState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := SaveAuthState(ctx, dbPath, "AR", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	legacyAuthPath, err := servicedb.CookiePath(dbPath, "AR_auth.txt")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyAuthPath), 0o700); err != nil {
		t.Fatalf("mkdir legacy auth dir: %v", err)
	}
	if err := os.WriteFile(legacyAuthPath, []byte("legacy-auth-key"), 0o600); err != nil {
		t.Fatalf("write legacy auth: %v", err)
	}
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	legacyCookiePath, err := servicedb.CookiePath(dbPath, "AR.txt")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.MkdirAll(legacyCookiePath, 0o700); err != nil {
		t.Fatalf("mkdir legacy cookie path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyCookiePath, "child"), []byte("blocks remove"), 0o600); err != nil {
		t.Fatalf("write legacy cookie child: %v", err)
	}
	service := NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	_, err = service.Delete(ctx, "AR")
	if err == nil {
		t.Fatal("expected cookie delete error")
	}
	if !strings.Contains(err.Error(), "delete AR cookies") {
		t.Fatalf("expected cookie delete error, got %v", err)
	}
	authKey, err := LoadAuthState(ctx, dbPath, "AR", "auth_key")
	if err != nil {
		t.Fatalf("expected AR auth state to be restored: %v", err)
	}
	if authKey != "secret-auth-key" {
		t.Fatalf("unexpected restored auth state")
	}
	legacyAuth, err := os.ReadFile(legacyAuthPath)
	if err != nil {
		t.Fatalf("expected legacy AR auth to be restored: %v", err)
	}
	if string(legacyAuth) != "legacy-auth-key" {
		t.Fatalf("unexpected restored legacy AR auth")
	}
	cookieValues, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("expected AR cookies to be restored: %v", err)
	}
	if cookieValues["session"] != "abc" {
		t.Fatalf("unexpected restored AR cookies: %#v", cookieValues)
	}
}

func TestTrackerAuthSnapshotRestoreIgnoresCanceledContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := SaveAuthState(ctx, dbPath, "AR", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	snapshot, err := snapshotTrackerAuthState(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("snapshotTrackerAuthState: %v", err)
	}
	if err := deleteTrackerAuthState(ctx, dbPath, "AR"); err != nil {
		t.Fatalf("deleteTrackerAuthState: %v", err)
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := snapshot.restore(cancelledCtx); err != nil {
		t.Fatalf("restore with canceled context: %v", err)
	}
	authKey, err := LoadAuthState(ctx, dbPath, "AR", "auth_key")
	if err != nil {
		t.Fatalf("expected AR auth state to be restored: %v", err)
	}
	if authKey != "secret-auth-key" {
		t.Fatalf("unexpected restored auth state")
	}
}

func TestEnsureSessionPreservesMTVCookiesOnInvalidLookingAdapterText(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"MTV": "session",
	}
	for trackerID, cookieName := range tests {
		t.Run(trackerID, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dbPath := newTrackerAuthTestDB(t)
			if err := cookies.SaveTrackerCookieMap(ctx, dbPath, trackerID, map[string]string{cookieName: "abc"}); err != nil {
				t.Fatalf("SaveTrackerCookieMap: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("<html>logged out</html>"))
			}))
			t.Cleanup(server.Close)

			service := NewService(config.Config{})
			_, err := service.EnsureSession(ctx, EnsureRequest{
				TrackerID: trackerID,
				Config:    config.TrackerConfig{URL: server.URL},
				DBPath:    dbPath,
				AutoLogin: true,
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, trackerID)
			if err != nil {
				t.Fatalf("LoadTrackerCookieMap: %v", err)
			}
			if values[cookieName] != "abc" {
				t.Fatalf("expected invalid-looking adapter text to preserve cookies, got %#v", values)
			}
		})
	}
}

func TestValidateTransientAdapterFailurePreservesCookieCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "MTV", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>logged out</html>"))
	}))
	t.Cleanup(server.Close)

	service := NewService(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"MTV": {URL: server.URL},
			},
		},
	})
	status, err := service.Validate(ctx, "MTV")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if status.State != StateHasCookies {
		t.Fatalf("expected cookies to remain configured after transient adapter failure, got %#v", status)
	}
	if status.CookieCount != 1 {
		t.Fatalf("expected existing cookies to be preserved, got %#v", status)
	}
	if !strings.Contains(status.LastError, "auth key not found") {
		t.Fatalf("expected adapter failure in status, got %#v", status)
	}
	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "MTV")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "abc" {
		t.Fatalf("expected transient adapter failure to preserve cookies, got %#v", values)
	}
}

func newTrackerAuthTestDB(t *testing.T) string {
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

func saveTrackerAuthTestConfig(t *testing.T, dbPath string, cfg config.Config) {
	t.Helper()

	repo, err := servicedb.OpenWithLoggerContext(context.Background(), dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	defer repo.Close()
	if err := config.SaveToDatabase(context.Background(), &cfg, repo); err != nil {
		t.Fatalf("SaveToDatabase: %v", err)
	}
}

func loadStoredTrackerConfig(t *testing.T, dbPath string) config.Config {
	t.Helper()

	repo, err := servicedb.OpenWithLoggerContext(context.Background(), dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	defer repo.Close()
	cfg, err := config.LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabase: %v", err)
	}
	return *cfg
}

func newMTVManual2FAServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			_, _ = w.Write([]byte("<html>logged out</html>"))
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.Redirect(w, r, "/twofactor/login", http.StatusFound)
		case "/twofactor/login":
			_, _ = w.Write([]byte(`<input name="token" value="ponmlkjihgfedcba">`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newBTNManual2FAServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			_, _ = w.Write([]byte(`<form action="/login.php"><input type="password" name="password"></form>`))
		case "/login.php":
			_, _ = w.Write([]byte(`<form><input name="codenumber" value=""></form><p>2FA required</p>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newPTPManual2FAServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Result":"TfaRequired"}`))
	}))
	t.Cleanup(server.Close)
	return server
}
