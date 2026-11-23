// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/internal/trackers/auth/totp"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	testLoginForm = `<form name="login-form" method="post" action="/user/account/login/">
	<input type="hidden" name="csrf" value="fixture-csrf"><input name="username"><input type="password" name="password"></form>`
	testOTPForm = `<form name="otp-form" method="post" action="/user/account/otpcheck/">
	<input type="hidden" name="username" value="fixture-user">
	<input type="hidden" name="password" value="fixture&amp;opaque">
	<input type="hidden" name="otpkey" value=""></form>`
	testLoggedIn = `<nav class="navbar navbar-default tl loggedin"></nav>`
	testOTPURI   = "otpauth://totp/fixture?secret=JBSWY3DPEHPK3PXP"
)

type tlLoginFixture struct {
	t                       *testing.T
	needsOTP                bool
	acceptCode              func(string) bool
	passwordPosts, otpPosts int
}

func (f *tlLoginFixture) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Helper()
	if req.URL.Host != "www.torrentleech.org" || req.URL.Scheme != "https" {
		f.t.Fatal("auth request left the tracker origin")
	}
	session, _ := req.Cookie("tl_session")
	signedIn := session != nil && session.Value == "active"
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/":
		if signedIn {
			return testAuthResponse(http.StatusOK, testLoggedIn), nil
		}
		return testAuthResponse(http.StatusOK, testLoginForm, "form_session=fixture; Path=/"), nil
	case req.Method == http.MethodPost && req.URL.Path == loginPath:
		defer req.Body.Close()
		f.passwordPosts++
		if cookie, _ := req.Cookie("form_session"); cookie == nil {
			f.t.Fatal("login did not retain the first page session")
		}
		if err := req.ParseForm(); err != nil {
			f.t.Fatal("invalid form encoding")
		}
		if req.PostForm.Get("username") != "fixture-user" || req.PostForm.Get("password") != "fixture-password" ||
			req.PostForm.Get("csrf") != "fixture-csrf" {
			f.t.Fatal("login did not send the configured credentials and hidden field")
		}
		if f.needsOTP {
			response := testAuthResponse(http.StatusFound, "", "otp_session=fixture; Path=/")
			response.Header.Set("Location", otpPath)
			return response, nil
		}
		return testAuthSuccess(), nil
	case req.Method == http.MethodGet && req.URL.Path == otpPath:
		response := testAuthResponse(http.StatusOK, strings.Replace(testOTPForm, otpPath, "./", 1))
		response.Request = req
		return response, nil
	case req.Method == http.MethodPost && req.URL.Path == otpPath:
		defer req.Body.Close()
		f.otpPosts++
		if cookie, _ := req.Cookie("otp_session"); cookie == nil {
			f.t.Fatal("2FA did not retain the login session")
		}
		if err := req.ParseForm(); err != nil {
			f.t.Fatal("invalid 2FA form encoding")
		}
		if req.PostForm.Get("username") != "fixture-user" || req.PostForm.Get("password") != "fixture&opaque" {
			f.t.Fatal("2FA did not send the decoded hidden form values")
		}
		if !f.acceptCode(req.PostForm.Get("otpkey")) {
			return testAuthResponse(http.StatusOK, testOTPForm), nil
		}
		return testAuthSuccess(), nil
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/torrents/browse/list/"):
		if !signedIn {
			response := testAuthResponse(http.StatusFound, "")
			response.Header.Set("Location", loginPath)
			return response, nil
		}
		return testAuthResponse(http.StatusOK, `{"numFound":0,"page":1,"perPage":100,"torrentList":[]}`), nil
	default:
		f.t.Fatalf("unexpected synthetic TL request method=%s path=%s", req.Method, req.URL.Path)
		return nil, errors.New("unexpected synthetic request")
	}
}

func testAuthResponse(status int, content string, cookieHeaders ...string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Set-Cookie": cookieHeaders},
		Body:       io.NopCloser(strings.NewReader(content)),
	}
}

func testAuthSuccess() *http.Response {
	response := testAuthResponse(http.StatusFound, "", "tl_session=active; Path=/; HttpOnly; Secure")
	response.Header.Set("Location", "/")
	return response
}

func tlAuthTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	authfixture.Write(t, dbPath)
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	err = repo.MigrateContext(t.Context())
	if closeErr := repo.Close(); err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	return dbPath
}

func TestDuplicateSearchLogsIntoTL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name               string
		expired, withOTP   bool
		wantSearchRequests int
	}{
		{
			name:               "missing session with credentials",
			wantSearchRequests: 1,
		},
		{
			name:               "expired session with TOTP",
			expired:            true,
			withOTP:            true,
			wantSearchRequests: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dbPath := tlAuthTestDB(t)
			if tc.expired {
				if err := cookies.SaveTrackerCookieMap(t.Context(), dbPath, "TL", map[string]string{"tl_session": "expired"}); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.TrackerConfig{
				Passkey:   "fixture-passkey",
				Username:  "fixture-user",
				Password:  "fixture-password",
				APIUpload: true,
			}
			if tc.withOTP {
				cfg.OTPURI = testOTPURI
			}
			transport := &tlLoginFixture{
				t:        t,
				needsOTP: tc.withOTP,
				acceptCode: func(code string) bool {
					now := time.Now()
					current, _ := totp.FromURIAt(testOTPURI, now)
					previous, _ := totp.FromURIAt(testOTPURI, now.Add(-30*time.Second))
					return code == current || code == previous
				},
			}
			adapter := New().NewDuplicateAdapter(dupe.NewDependencies("TL", cfg, dbPath, &http.Client{Transport: transport}, api.NopLogger{}))
			subject := api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}}
			result := adapter.Search(t.Context(), subject)
			if result.Disposition() != dupe.DispositionResolved || !result.SearchEvidence().Complete || result.SearchEvidence().Pages != tc.wantSearchRequests {
				t.Fatalf("search result=%v code=%s evidence=%+v", result.Disposition(), result.Code(), result.SearchEvidence())
			}
			if transport.passwordPosts != 1 || (transport.otpPosts == 1) != tc.withOTP {
				t.Fatalf("login posts=%d 2FA posts=%d", transport.passwordPosts, transport.otpPosts)
			}
			result = adapter.Search(t.Context(), subject)
			if result.Disposition() != dupe.DispositionResolved || transport.passwordPosts != 1 {
				t.Fatal("saved session was not reused for the next duplicate search")
			}
		})
	}
}

func TestTLTrackerAuthManual2FARetriesAndSavesSession(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	cfg := config.TrackerConfig{
		Passkey:  "fixture-passkey",
		Username: "fixture-user",
		Password: "fixture-password",
	}
	transport := &tlLoginFixture{
		t:          t,
		needsOTP:   true,
		acceptCode: func(value string) bool { return value == "528430" },
	}
	service := newTLAuthTestService(t, cfg, dbPath, transport)

	statuses, err := service.ValidateMany(t.Context(), []string{"TL"})
	if err != nil || len(statuses) != 1 || !statuses[0].Needs2FA || statuses[0].ChallengeID == "" {
		t.Fatal("form upload preflight did not produce the manual-2FA recovery")
	}
	first, err := service.Login(t.Context(), "TL", api.TrackerAuthLoginRequest{})
	if err != nil || !first.Needs2FA || first.ChallengeID == "" || first.CookieCount != 0 {
		t.Fatal("tracker auth login did not start a manual challenge")
	}
	rejected, err := service.Submit2FA(t.Context(), first.ChallengeID, "111111")
	if err != nil || !rejected.Needs2FA || rejected.ChallengeID != first.ChallengeID || rejected.CookieCount != 0 {
		t.Fatal("rejected code did not leave the challenge available for retry")
	}
	ready, err := service.Submit2FA(t.Context(), first.ChallengeID, "528430")
	if err != nil || !trackerauth.IsReadyStatus(ready) || ready.CookieCount == 0 || ready.ChallengeID != "" {
		t.Fatal("accepted code did not establish a reusable session")
	}
}

func TestTLLoginRejectsCredentialRedirects(t *testing.T) {
	t.Parallel()
	for _, location := range []string{"/unrelated", "https://unrelated.invalid/login"} {
		t.Run(location, func(t *testing.T) {
			t.Parallel()
			dbPath := tlAuthTestDB(t)
			requests := 0
			transport := tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				switch requests {
				case 1:
					return testAuthResponse(http.StatusOK, testLoginForm), nil
				case 2:
					defer req.Body.Close()
					response := testAuthResponse(http.StatusTemporaryRedirect, "")
					response.Header.Set("Location", location)
					return response, nil
				default:
					t.Fatal("login followed a redirect that could resend credentials")
					return nil, errors.New("unexpected synthetic request")
				}
			})
			cfg := config.TrackerConfig{Username: "fixture-user", Password: "fixture-password"}
			if err := ensureCookieSession(t.Context(), cfg, dbPath, api.TrackerAuthLoginRequest{}, transport); err == nil {
				t.Fatal("login accepted a redirect without an authenticated session")
			}
			if _, err := loadCookieSession(t.Context(), dbPath, cfg); !errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
				t.Fatal("failed login persisted cookies")
			}
		})
	}
}

func newTLAuthTestService(t *testing.T, cfg config.TrackerConfig, dbPath string, transport http.RoundTripper) *trackerauth.Service {
	t.Helper()
	profile := Profile()
	profile.AuthResolver = func(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
		return ensureCookieSession(ctx, cfg, dbPath, login, transport)
	}
	registry := trackers.NewRegistry()
	if err := registry.Register(standalone.MustNew(profile)); err != nil {
		t.Fatal(err)
	}
	return trackerauth.NewServiceWithRegistryAndLogger(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers:     config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": cfg}},
	}, registry, api.NopLogger{})
}

func TestTLExplicitCodeTakesPrecedence(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	cfg := config.TrackerConfig{
		Passkey:  "fixture-passkey",
		Username: "fixture-user",
		Password: "fixture-password",
		OTPURI:   "unusable-otp-uri",
	}
	transport := &tlLoginFixture{
		t:          t,
		needsOTP:   true,
		acceptCode: func(code string) bool { return code == "528430" },
	}
	service := newTLAuthTestService(t, cfg, dbPath, transport)
	status, err := service.Login(t.Context(), "TL", api.TrackerAuthLoginRequest{Code: "528430"})
	if err != nil || !trackerauth.IsReadyStatus(status) || status.CookieCount == 0 || transport.otpPosts != 1 {
		t.Fatal("caller-supplied code did not establish the session")
	}

	// Form upload must never consume an automatically created cookie from a
	// previous account. A user can still explicitly import browser cookies.
	changed := cfg
	changed.Username = "fixture-other-user"
	changed.Passkey = "fixture-other-passkey"
	if _, err := cookieClient(t.Context(), dbPath, changed); !errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		t.Fatal("the form upload client accepted a previous account's managed session")
	}
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	snapshot, err := cookies.SnapshotTrackerCookieMap(t.Context(), repo.RawDB(), dbPath, "TL", "torrentleech.org", cookieOwner(cfg))
	if err != nil || snapshot["tl_session"] != "active" || len(snapshot) != status.CookieCount {
		t.Fatal("connection test could not read the session without its owner marker")
	}
	if _, err := cookies.SnapshotTrackerCookieMap(t.Context(), repo.RawDB(), dbPath, "TL", "torrentleech.org", cookieOwner(changed)); !errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		t.Fatal("connection test accepted a previous account's managed session")
	}
	changedService := newTLAuthTestService(t, changed, dbPath, transport)
	changedStatus, err := changedService.Status(t.Context(), "TL")
	if err != nil || trackerauth.IsReadyStatus(changedStatus) || changedStatus.CookieCount != 0 {
		t.Fatal("local status accepted a previous account's managed session")
	}
	writeTLSearchCookie(t, dbPath)
	imported, err := changedService.ImportCookies(t.Context(), "TL", "fixture.json", `{"tl_session":"imported"}`)
	if err != nil || !trackerauth.IsReadyStatus(imported) || imported.CookieCount != 1 {
		t.Fatal("explicit import did not replace the previous account's managed session")
	}
	legacyPath, err := db.CookiePath(dbPath, "TL.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := cookies.EnsureCookieMigration(t.Context(), repo.RawDB(), dbPath, filepath.Dir(legacyPath), api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	session, err := loadCookieSession(t.Context(), dbPath, changed)
	if err != nil || session.AccountBound || len(session.Cookies) != 1 || session.Cookies[0].Value != "imported" {
		t.Fatal("explicit import lost authority over the retained legacy file")
	}
}

func TestTLCheckAuthPersistsConfirmedSignOut(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	writeTLSearchCookie(t, dbPath)
	cfg := config.TrackerConfig{Passkey: "fixture-passkey"}
	service := newTLAuthTestService(t, cfg, dbPath, &tlLoginFixture{t: t})
	status, err := service.Validate(t.Context(), "TL")
	if err != nil || status.State != trackerauth.StateLoginRequired || status.CookieCount != 0 {
		t.Fatal("Check Auth did not invalidate the signed-out session")
	}
	reloaded, err := service.Status(t.Context(), "TL")
	if err != nil || trackerauth.IsReadyStatus(reloaded) || reloaded.CookieCount != 0 {
		t.Fatal("reloading status resurrected the signed-out legacy session")
	}
}

func TestTLCheckAuthAllowsFreshFolderImportAfterSignOut(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	cfg := config.TrackerConfig{Passkey: "fixture-passkey"}
	if err := cookies.SaveTrackerCookieMap(t.Context(), dbPath, "TL", map[string]string{"tl_session": "expired"}); err != nil {
		t.Fatal(err)
	}
	service := newTLAuthTestService(t, cfg, dbPath, &tlLoginFixture{t: t})
	status, err := service.Validate(t.Context(), "TL")
	if err != nil || status.State != trackerauth.StateLoginRequired || status.CookieCount != 0 {
		t.Fatal("Check Auth did not invalidate the signed-out session")
	}
	path, err := db.CookiePath(dbPath, "TL.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tl_session":"fresh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := cookies.EnsureCookieMigration(t.Context(), repo.RawDB(), dbPath, filepath.Dir(path), api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fresh export was not migrated")
	}
	session, err := loadCookieSession(t.Context(), dbPath, cfg)
	if err != nil || session.AccountBound || len(session.Cookies) != 1 || session.Cookies[0].Value != "fresh" {
		t.Fatal("fresh export did not restore the cookie-only session")
	}
}

func TestTLCheckAuthPreservesSessionMigratedDuringValidation(t *testing.T) {
	// The fixture runs an actual SQLite migration inside the bounded auth
	// request; do not queue it behind the other tests using TL's session gate.
	for _, scenario := range []struct {
		name, page string
		setCookie  []string
	}{
		{name: "signed-out", page: testLoginForm},
		{name: "unchanged", page: testLoggedIn},
		{
			name:      "rotated",
			page:      testLoggedIn,
			setCookie: []string{"tl_session=rotated; Path=/"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := t.Context()
			dbPath := tlAuthTestDB(t)
			cfg := config.TrackerConfig{Passkey: "fixture-passkey"}
			if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "TL", map[string]string{"tl_session": "observed"}); err != nil {
				t.Fatal(err)
			}
			repo, err := db.OpenContext(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			path, err := db.CookiePath(dbPath, "TL.json")
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(path)
			service := newTLAuthTestService(t, cfg, dbPath, tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				cookie, err := req.Cookie("tl_session")
				if err != nil || cookie.Value != "observed" {
					t.Fatal("validation did not send the observed session")
				}
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"tl_session":"replacement"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := cookies.EnsureCookieMigration(ctx, repo.RawDB(), dbPath, dir, api.NopLogger{}); err != nil {
					t.Fatal(err)
				}
				return testAuthResponse(http.StatusOK, scenario.page, scenario.setCookie...), nil
			}))
			status, err := service.Validate(ctx, "TL")
			if err != nil || !strings.Contains(status.LastError, "session changed") {
				t.Fatalf("validation did not report that its session was replaced: status=%s error=%s call=%v", status.State, status.LastError, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("replacement export was not migrated")
			}
			session, err := loadCookieSession(ctx, dbPath, cfg)
			if err != nil || session.AccountBound || len(session.Cookies) != 1 || session.Cookies[0].Value != "replacement" {
				t.Fatal("the stale validation changed the replacement session")
			}
		})
	}
}

func TestTLLoginPreservesSessionImportedDuringSignIn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dbPath := tlAuthTestDB(t)
	cfg := config.TrackerConfig{
		Username: "fixture-user",
		Password: "fixture-password",
		Passkey:  "fixture-passkey",
	}
	fixture := &tlLoginFixture{t: t}
	service := newTLAuthTestService(t, cfg, dbPath, tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if cookie, err := req.Cookie("tl_session"); err == nil && cookie.Value == "active" {
			if err := cookies.SaveImportedTrackerCookieMap(ctx, dbPath, "TL", map[string]string{"tl_session": "replacement"}); err != nil {
				t.Fatal(err)
			}
		}
		return fixture.RoundTrip(req)
	}))
	status, err := service.Validate(ctx, "TL")
	if err != nil || !strings.Contains(status.LastError, "session changed") {
		t.Fatal("sign-in did not report the intervening import")
	}
	session, err := loadCookieSession(ctx, dbPath, cfg)
	if err != nil || session.AccountBound || len(session.Cookies) != 1 || session.Cookies[0].Value != "replacement" {
		t.Fatal("sign-in replaced the intervening import")
	}
}

func TestTLCheckAuthReportsMissingPasskeyAfterSignIn(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	cfg := config.TrackerConfig{Username: "fixture-user", Password: "fixture-password"}
	service := newTLAuthTestService(t, cfg, dbPath, &tlLoginFixture{t: t})
	status, err := service.Validate(t.Context(), "TL")
	if err != nil || status.CookieCount == 0 || trackerauth.IsReadyStatus(status) || !strings.Contains(status.Message, "passkey") {
		t.Fatal("successful website sign-in did not identify the missing upload passkey")
	}
}

func TestTLCheckAuthPersistsRotatedCookie(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	cfg := config.TrackerConfig{Passkey: "fixture-passkey"}
	stored := []*http.Cookie{{Name: "tl_session", Value: "before"}}
	if err := cookies.SaveOwnedTrackerHTTPCookies(t.Context(), dbPath, "TL", cookieOwner(cfg), stored); err != nil {
		t.Fatal(err)
	}
	service := newTLAuthTestService(t, cfg, dbPath, tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		cookie, err := req.Cookie("tl_session")
		if err != nil || cookie.Value != "before" {
			t.Fatal("validation did not use the prior managed session")
		}
		return testAuthResponse(http.StatusOK, testLoggedIn, "tl_session=after; Path=/"), nil
	}))
	status, err := service.Validate(t.Context(), "TL")
	if err != nil || !trackerauth.IsReadyStatus(status) {
		t.Fatal("rotated session was rejected")
	}
	client, err := cookieClient(t.Context(), dbPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse(baseURL)
	cookies := client.Jar.Cookies(origin)
	if len(cookies) != 1 || cookies[0].Value != "after" {
		t.Fatal("form upload could not reload the rotated session")
	}
}

func TestTLCanceledSignInStopsDuplicateSearch(t *testing.T) {
	t.Parallel()
	for _, refresh := range []bool{false, true} {
		name := "initial sign-in"
		if refresh {
			name = "mid-search refresh"
		}
		t.Run(name, func(t *testing.T) {
			dbPath := tlAuthTestDB(t)
			if refresh {
				writeTLSearchCookie(t, dbPath)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			transport := tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.URL.Path == "/":
					cancel()
					return nil, ctx.Err()
				case strings.HasSuffix(req.URL.Path, "/page/2"):
					response := testAuthResponse(http.StatusFound, "")
					response.Header.Set("Location", loginPath)
					return response, nil
				default:
					return testAuthResponse(http.StatusOK, `{"numFound":2,"page":1,"perPage":1,"torrentList":[{"fid":1,"imdbID":"tt1234567","name":"Example.Release.2026"}]}`), nil
				}
			})
			cfg := config.TrackerConfig{
				Passkey:  "fixture-passkey",
				Username: "fixture-user",
				Password: "fixture-password",
			}
			adapter := New().NewDuplicateAdapter(dupe.NewDependencies("TL", cfg, dbPath, &http.Client{Transport: transport}, api.NopLogger{}))
			result := adapter.Search(ctx, api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
			if result.Disposition() != dupe.DispositionFailed || result.Code() != dupe.FailureRequest || !errors.Is(result.Cause(), context.Canceled) {
				t.Fatalf("canceling authentication returned disposition=%v code=%s", result.Disposition(), result.Code())
			}
		})
	}
}

func TestTLRefreshAfterAcceptedPageKeepsOnlyPartialEvidence(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	writeTLSearchCookie(t, dbPath)
	fixture := &tlLoginFixture{t: t}
	transport := tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/torrents/browse/list/") {
			return fixture.RoundTrip(req)
		}
		if !strings.HasSuffix(req.URL.Path, "/page/2") {
			return testAuthResponse(http.StatusOK, `{"numFound":2,"page":1,"perPage":1,"torrentList":[{"fid":1,"imdbID":"tt1234567","name":"Example.Release.2026.First"}]}`), nil
		}
		if session, _ := req.Cookie("tl_session"); session != nil && session.Value == "active" {
			return testAuthResponse(http.StatusOK, `{"numFound":2,"page":2,"perPage":1,"torrentList":[{"fid":2,"imdbID":"tt1234567","name":"Example.Release.2026.Second"}]}`), nil
		}
		response := testAuthResponse(http.StatusFound, "")
		response.Header.Set("Location", loginPath)
		return response, nil
	})
	cfg := config.TrackerConfig{
		Passkey:  "fixture-passkey",
		Username: "fixture-user",
		Password: "fixture-password",
	}
	adapter := New().NewDuplicateAdapter(dupe.NewDependencies("TL", cfg, dbPath, &http.Client{Transport: transport}, api.NopLogger{}))
	result := adapter.Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if result.Disposition() != dupe.DispositionResolved || result.SearchEvidence().EffectiveComplete() || result.SearchEvidence().Pages != 3 ||
		len(result.Entries()) != 2 || fixture.passwordPosts != 1 {
		t.Fatalf("expected two retained candidates without complete coverage: disposition=%v evidence=%+v", result.Disposition(), result.SearchEvidence())
	}
}

func TestTLLoginConfirmsSessionAfterDirectPOSTResponse(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	transport := tlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			defer req.Body.Close()
			return testAuthResponse(http.StatusOK, testLoggedIn), nil
		}
		return testAuthResponse(http.StatusOK, testLoginForm, "form_session=fixture; Path=/"), nil
	})
	cfg := config.TrackerConfig{Username: "fixture-user", Password: "fixture-password"}
	if err := ensureCookieSession(t.Context(), cfg, dbPath, api.TrackerAuthLoginRequest{}, transport); err == nil {
		t.Fatal("accepted an authenticated-looking POST without a usable session")
	}
	if _, err := loadCookieSession(t.Context(), dbPath, cfg); !errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		t.Fatal("persisted an anonymous form session")
	}
}

func TestTLLegacySessionCannotReplaceUnavailableManagedSession(t *testing.T) {
	t.Parallel()
	dbPath := tlAuthTestDB(t)
	writeTLSearchCookie(t, dbPath)
	cfg := config.TrackerConfig{Username: "fixture-user", Passkey: "fixture-passkey"}
	if err := cookies.SaveOwnedTrackerHTTPCookies(t.Context(), dbPath, "TL", cookieOwner(cfg), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(authmaterial.AuthFilePath(dbPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCookieSession(t.Context(), dbPath, cfg); !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
		t.Fatalf("HTTP loader accepted superseded cookies: %v", err)
	}
	if _, err := cookies.LoadTrackerCookieMap(t.Context(), dbPath, "TL", cookieOwner(cfg)); !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
		t.Fatalf("status loader accepted superseded cookies: %v", err)
	}
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if _, err := cookies.SnapshotTrackerCookieMap(t.Context(), repo.RawDB(), dbPath, "TL", "torrentleech.org", cookieOwner(cfg)); !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
		t.Fatalf("connection snapshot accepted superseded cookies: %v", err)
	}
	service := newTLAuthTestService(t, cfg, dbPath, nil)
	if _, err := service.Delete(t.Context(), "TL"); !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
		t.Fatalf("Delete Auth accepted unavailable managed storage: %v", err)
	}
}
