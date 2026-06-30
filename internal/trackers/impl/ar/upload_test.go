// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackerauth"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveARNameAddsNoGRP(t *testing.T) {
	t.Parallel()

	got := resolveARName(api.PreparedMetadata{
		SourcePath: "C:/data/My Movie (2024).mkv",
		Release:    api.ReleaseInfo{Title: "My Movie", Year: 2024},
	})
	if got != "My.Movie.2024-NoGRP" {
		t.Fatalf("unexpected AR name %q", got)
	}
}

func TestResolveARNameUsesSceneName(t *testing.T) {
	t.Parallel()

	got := resolveARName(api.PreparedMetadata{
		Scene:     true,
		SceneName: "Scene.Release-GRP",
		Tag:       "-GRP",
	})
	if got != "Scene.Release-GRP" {
		t.Fatalf("expected scene name, got %q", got)
	}
}

type captureLogger struct {
	warnings []string
}

func (l *captureLogger) Tracef(string, ...any) {}
func (l *captureLogger) Debugf(string, ...any) {}
func (l *captureLogger) Infof(string, ...any)  {}
func (l *captureLogger) Errorf(string, ...any) {}
func (l *captureLogger) Warnf(format string, _ ...any) {
	l.warnings = append(l.warnings, strings.TrimSpace(format))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func arTestResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestPersistLoginCookiesReturnsAuthHelperUnavailable(t *testing.T) {
	t.Parallel()

	logger := &captureLogger{}
	err := persistLoginCookies(context.Background(), filepath.Join(t.TempDir(), "upbrr.db"), logger, []*http.Cookie{{Name: "session", Value: "abc"}})
	if !errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		t.Fatalf("expected auth helper unavailable error, got %v", err)
	}
	if len(logger.warnings) != 0 {
		t.Fatalf("unexpected warning after hard persistence failure: %v", logger.warnings)
	}
}

func TestPersistLoginCookiesRejectsEmptyJarWithoutReplacingCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newARAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}

	err := persistLoginCookies(ctx, dbPath, api.NopLogger{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no usable cookies") {
		t.Fatalf("expected empty login cookie error, got %v", err)
	}
	values, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatal("empty jar must preserve previous cookies")
	}
}

func TestEnsureLoginPersistenceAvailableRequiresWebAuth(t *testing.T) {
	t.Parallel()

	err := ensureLoginPersistenceAvailable(filepath.Join(t.TempDir(), "upbrr.db"))
	if !errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		t.Fatalf("expected auth helper unavailable preflight, got %v", err)
	}
}

func TestWriteAuthKeyUsesEncryptedStateAndDeletesLegacyFile(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	legacyPath := authPath(dbPath)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy-key"), 0o600); err != nil {
		t.Fatalf("seed legacy auth key: %v", err)
	}

	if err := writeAuthKey(context.Background(), dbPath, "encrypted-key"); err != nil {
		t.Fatalf("writeAuthKey: %v", err)
	}
	if got := readAuthKey(context.Background(), dbPath); got != "encrypted-key" {
		t.Fatal("expected encrypted auth key")
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy auth key removed, stat err=%v", err)
	}
}

func TestWriteAuthKeyRollsBackEncryptedStateOnLegacyDeleteFailure(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	legacyPath := authPath(dbPath)
	if err := os.MkdirAll(filepath.Join(legacyPath, "blocked"), 0o755); err != nil {
		t.Fatalf("seed legacy auth path as non-empty dir: %v", err)
	}

	if err := writeAuthKey(context.Background(), dbPath, "encrypted-key"); err == nil {
		t.Fatal("expected legacy cleanup failure")
	} else if !strings.Contains(err.Error(), "remove legacy auth key") {
		t.Fatalf("expected legacy cleanup error, got %v", err)
	}
	if _, err := trackerauth.LoadAuthState(context.Background(), dbPath, "AR", arAuthKeyKey); !errors.Is(err, trackerauth.ErrAuthStateNotFound) {
		t.Fatalf("expected encrypted auth key rollback, got %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("expected failed legacy cleanup path to remain, stat err=%v", err)
	}
}

func TestWriteAuthKeyFallsBackToExistingLegacyFileWhenWebAuthUnavailable(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	legacyPath := authPath(dbPath)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy-key"), 0o600); err != nil {
		t.Fatalf("seed legacy auth key: %v", err)
	}

	if err := writeAuthKey(context.Background(), dbPath, "updated-legacy-key"); err != nil {
		t.Fatalf("writeAuthKey: %v", err)
	}
	if got := readAuthKey(context.Background(), dbPath); got != "updated-legacy-key" {
		t.Fatal("expected updated legacy auth key")
	}
}

func TestWriteAuthKeySucceedsWhenLegacyFileAbsent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	if err := writeAuthKey(context.Background(), dbPath, "encrypted-key"); err != nil {
		t.Fatalf("writeAuthKey: %v", err)
	}
	if got := readAuthKey(context.Background(), dbPath); got != "encrypted-key" {
		t.Fatal("expected encrypted auth key")
	}
}

func TestWriteAuthKeyWithoutWebAuthReturnsPersistenceFailure(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := writeAuthKey(context.Background(), dbPath, "session-key"); !errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		t.Fatalf("expected auth helper unavailable error, got %v", err)
	}
	if _, err := os.Stat(authPath(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("expected no plaintext auth key, stat err=%v", err)
	}
}

func TestPersistLoginAuthRestoresPreviousCookiesWhenAuthKeyWriteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newARAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	forcedErr := errors.New("forced auth key failure")

	err := persistLoginAuthWithWriter(
		ctx,
		dbPath,
		api.NopLogger{},
		[]*http.Cookie{{Name: "session", Value: "new"}},
		"new-auth-key",
		func(context.Context, string, string) error { return forcedErr },
	)
	if !errors.Is(err, forcedErr) {
		t.Fatalf("expected forced auth key failure, got %v", err)
	}
	values, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatal("expected previous cookies restored")
	}
}

func TestPersistLoginAuthRestoresPreviousCookiesWhenCallerContextCanceledDuringWrite(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	dbPath := newARAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := cookiepkg.SaveTrackerCookieMap(context.Background(), dbPath, "AR", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	forcedErr := errors.New("forced auth key failure")

	err := persistLoginAuthWithWriter(
		ctx,
		dbPath,
		api.NopLogger{},
		[]*http.Cookie{{Name: "session", Value: "new"}},
		"new-auth-key",
		func(context.Context, string, string) error {
			cancel()
			return forcedErr
		},
	)
	if !errors.Is(err, forcedErr) {
		t.Fatalf("expected forced auth key failure, got %v", err)
	}
	values, err := cookiepkg.LoadTrackerCookieMap(context.Background(), dbPath, "AR")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatal("expected previous cookies restored after cancellation")
	}
}

func TestPersistLoginAuthRemovesNewCookiesWhenAuthKeyWriteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newARAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	forcedErr := errors.New("forced auth key failure")

	err := persistLoginAuthWithWriter(
		ctx,
		dbPath,
		api.NopLogger{},
		[]*http.Cookie{{Name: "session", Value: "new"}},
		"new-auth-key",
		func(context.Context, string, string) error { return forcedErr },
	)
	if !errors.Is(err, forcedErr) {
		t.Fatalf("expected forced auth key failure, got %v", err)
	}
	if _, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "AR"); err == nil {
		t.Fatal("expected rollback to remove newly persisted cookies")
	}
}

func TestLoginFallbackDoesNotPersistAuthKeyBeforeCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newARAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case arLoginURL:
				return arTestResponse(req, "<html>logged in without inline auth</html>"), nil
			case arBrowseURL:
				return arTestResponse(req, `<a href="/torrents.php?action=download&id=1&auth=session-key">Download</a>`), nil
			default:
				t.Fatalf("unexpected request URL %s", req.URL.String())
				return nil, nil
			}
		}),
	}

	authKey, err := login(ctx, client, config.TrackerConfig{Username: "user", Password: "pass"}, dbPath)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if authKey != "session-key" {
		t.Fatalf("expected auth key from validation page, got %q", authKey)
	}
	if _, err := trackerauth.LoadAuthState(ctx, dbPath, "AR", arAuthKeyKey); !errors.Is(err, trackerauth.ErrAuthStateNotFound) {
		t.Fatalf("login fallback must not persist auth key before cookie save, got %v", err)
	}

	if err := persistLoginAuth(ctx, dbPath, api.NopLogger{}, nil, authKey); err == nil || !strings.Contains(err.Error(), "no usable cookies") {
		t.Fatalf("expected cookie persistence failure, got %v", err)
	}
	if _, err := trackerauth.LoadAuthState(ctx, dbPath, "AR", arAuthKeyKey); !errors.Is(err, trackerauth.ErrAuthStateNotFound) {
		t.Fatalf("cookie persistence failure must leave no new auth key, got %v", err)
	}
}

func TestLoginFallbackPrefersCurrentResponseKeyAndPreservesPreviousAuthKeyOnCookieFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newARAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := trackerauth.SaveAuthState(ctx, dbPath, "AR", arAuthKeyKey, "previous-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case arLoginURL:
				return arTestResponse(req, "<html>logged in without inline auth</html>"), nil
			case arBrowseURL:
				return arTestResponse(req, `<a href="/torrents.php?action=download&id=1&auth=new-key">Download</a>`), nil
			default:
				t.Fatalf("unexpected request URL %s", req.URL.String())
				return nil, nil
			}
		}),
	}

	authKey, err := login(ctx, client, config.TrackerConfig{Username: "user", Password: "pass"}, dbPath)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if authKey != "new-key" {
		t.Fatalf("expected current response auth key, got %q", authKey)
	}
	if err := persistLoginAuth(ctx, dbPath, api.NopLogger{}, nil, authKey); err == nil || !strings.Contains(err.Error(), "no usable cookies") {
		t.Fatalf("expected cookie persistence failure, got %v", err)
	}
	got, err := trackerauth.LoadAuthState(ctx, dbPath, "AR", arAuthKeyKey)
	if err != nil {
		t.Fatalf("LoadAuthState: %v", err)
	}
	if got != "previous-key" {
		t.Fatal("cookie persistence failure changed previous auth key")
	}
}

func newARAuthTestDB(t *testing.T) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	repo, err := servicedb.OpenWithLogger(dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close repo: %v", err)
	}
	return dbPath
}

func TestBuildDatabaseLinksSkipsTVDBForMovie(t *testing.T) {
	t.Parallel()

	got := buildDatabaseLinks(api.PreparedMetadata{
		MediaInfoCategory: "TV",
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE", TVDBID: 456},
	})
	if strings.Contains(got, "thetvdb.com") {
		t.Fatalf("did not expect tvdb link for movie description, got %q", got)
	}
}

func TestBuildDatabaseLinksIncludesTVDBForMediaInfoTV(t *testing.T) {
	t.Parallel()

	got := buildDatabaseLinks(api.PreparedMetadata{
		MediaInfoCategory: "TV",
		ExternalIDs:       api.ExternalIDs{TVDBID: 456},
	})
	if !strings.Contains(got, "thetvdb.com/?id=456") {
		t.Fatalf("expected tvdb link for MediaInfo TV description, got %q", got)
	}
}

func TestBuildDatabaseLinksIncludesTVDBForTV(t *testing.T) {
	t.Parallel()

	got := buildDatabaseLinks(api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 456},
	})
	if !strings.Contains(got, "thetvdb.com/?id=456") {
		t.Fatalf("expected tvdb link for TV description, got %q", got)
	}
}
