// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
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
	"github.com/autobrr/upbrr/pkg/api"
)

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

func TestStatusConfiguredOTPURIAvoidsManualChallenge(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {Username: "user", Password: "pass", OTPURI: "otpauth://totp/PTP:user?secret=ABC"},
			},
		},
	})
	status, err := service.statusForSpec(context.Background(), trackerSpec{
		id:               "PTP",
		login:            true,
		totp:             true,
		manual2FA:        true,
		needsCredentials: true,
	})
	if err != nil {
		t.Fatalf("statusForSpec: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("expected configured status, got %#v", status)
	}
	if status.Needs2FA || strings.TrimSpace(status.ChallengeID) != "" {
		t.Fatalf("configured OTPURI must avoid manual challenge path: %#v", status)
	}
}

func TestLoginWithoutAdapterDoesNotReportRemoteSuccess(t *testing.T) {
	for _, trackerID := range []string{"AR", "FL", "THR", "RTF"} {
		t.Run(trackerID, func(t *testing.T) {
			cfg := config.Config{
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{
						trackerID: {Username: "user", Password: "pass"},
					},
				},
			}
			status, err := NewService(cfg).Login(
				context.Background(),
				trackerID,
				api.TrackerAuthLoginRequest{},
			)
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			if strings.Contains(status.Message, "succeeded") {
				t.Fatalf("unexpected remote success message for %s: %#v", trackerID, status)
			}
			if !strings.Contains(status.Message, "not supported") {
				t.Fatalf("expected unsupported remote login message for %s, got %#v", trackerID, status)
			}
		})
	}
}

func TestValidateWithoutAdapterReportsUnsupportedRemoteValidation(t *testing.T) {
	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"AR": {Username: "user", Password: "pass"},
			},
		},
	}
	status, err := NewService(cfg).Validate(context.Background(), "AR")
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
		{"name":"session","value":"latest"},
		{"name":"empty","value":""},
		{"name":"","value":"ignored"}
	]`)
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "latest" {
		t.Fatalf("expected deterministic last array value, got %#v", got)
	}
	if _, ok := got["empty"]; ok {
		t.Fatalf("empty cookie value should be ignored: %#v", got)
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

func TestParseCookieContentJSONRejectsInvalidShapes(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"invalid json":  "{",
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

func TestCapabilitiesDoNotAdvertiseUnsupportedMTVPTPManual2FA(t *testing.T) {
	t.Parallel()

	service := NewService(config.Config{})
	caps, err := service.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	for _, cap := range caps {
		switch cap.TrackerID {
		case "MTV", "PTP":
			if cap.SupportsManual2FA {
				t.Fatalf("%s must not advertise unsupported manual 2FA", cap.TrackerID)
			}
			if !cap.SupportsTOTP {
				t.Fatalf("%s TOTP auto-login capability must be preserved", cap.TrackerID)
			}
		}
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

func TestEnsureSessionDeletesConfirmedInvalidMTVPTPCookies(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"MTV": "session",
		"PTP": "session",
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
				t.Fatal("expected auth required error")
			}
			if _, err := cookies.LoadTrackerCookieMap(ctx, dbPath, trackerID); err == nil {
				t.Fatal("expected confirmed-invalid cookies to be deleted")
			}
		})
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
