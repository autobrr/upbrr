// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveCookiesReturnsLoginPageReadError(t *testing.T) {
	ctx := context.Background()
	dbPath := newFLAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	requests := 0
	withFLHTTPClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != http.MethodGet || req.URL.String() != loginPageURL {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &failingReadCloser{data: `<input name="validator" value="partial">`},
			Request:    req,
		}, nil
	})})

	_, err := resolveCookies(ctx, api.NopLogger{}, configWithCredentials(), dbPath, false)
	if err == nil {
		t.Fatal("expected login page read error")
	}
	if !strings.Contains(err.Error(), "read login page response") {
		t.Fatalf("expected read error context, got %v", err)
	}
	if strings.Contains(err.Error(), "validator token not found") {
		t.Fatalf("read error must not be replaced by validator parse error: %v", err)
	}
	if strings.Contains(err.Error(), "user") || strings.Contains(err.Error(), "pass") {
		t.Fatalf("read error must not expose credentials: %v", err)
	}
	if requests != 1 {
		t.Fatalf("read failure must stop before login POST, got %d requests", requests)
	}
}

func TestResolveCookiesMissingValidatorStillFailsBeforePost(t *testing.T) {
	ctx := context.Background()
	dbPath := newFLAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	requests := 0
	withFLHTTPClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != http.MethodGet || req.URL.String() != loginPageURL {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		}
		return textResponse(req, "no validator here"), nil
	})})

	_, err := resolveCookies(ctx, api.NopLogger{}, configWithCredentials(), dbPath, false)
	if err == nil || !strings.Contains(err.Error(), "validator token not found") {
		t.Fatalf("expected missing validator error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("missing validator must stop before login POST, got %d requests", requests)
	}
}

func TestResolveCookiesPostsFirstValidatorMatch(t *testing.T) {
	ctx := context.Background()
	dbPath := newFLAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	var postedValidator string
	requests := 0
	withFLHTTPClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		switch {
		case req.Method == http.MethodGet && req.URL.String() == loginPageURL:
			return textResponse(req, `<input name="validator" value="first"><input name="validator" value="second">`), nil
		case req.Method == http.MethodPost && req.URL.String() == loginURL:
			if err := req.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			postedValidator = req.Form.Get("validator")
			if got := req.Form.Get("username"); got != "user" {
				t.Fatalf("username = %q", got)
			}
			if got := req.Form.Get("password"); got != "pass" {
				t.Fatalf("password = %q", got)
			}
			if got := req.Form.Get("unlock"); got != "1" {
				t.Fatalf("unlock = %q", got)
			}
			resp := textResponse(req, "ok")
			resp.Header.Add("Set-Cookie", "session=abc; Path=/")
			return resp, nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})})

	values, err := resolveCookies(ctx, api.NopLogger{}, configWithCredentials(), dbPath, false)
	if err != nil {
		t.Fatalf("resolveCookies: %v", err)
	}
	if postedValidator != "first" {
		t.Fatalf("validator = %q, want first", postedValidator)
	}
	if len(values) != 1 || values[0].Name != "session" || values[0].Value != "abc" {
		t.Fatalf("unexpected cookies: %#v", values)
	}
	if requests != 2 {
		t.Fatalf("successful login should GET then POST, got %d requests", requests)
	}
}

func TestPersistLoginCookiesReturnsSaveFailure(t *testing.T) {
	t.Parallel()

	err := persistLoginCookies(
		context.Background(),
		filepath.Join(t.TempDir(), "upbrr.db"),
		[]*http.Cookie{{Name: "session", Value: "abc"}},
	)
	if !errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		t.Fatalf("expected auth helper save failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "persist login cookies") {
		t.Fatalf("expected persistence context, got %v", err)
	}
}

func TestPersistLoginCookiesRejectsEmptyResponseWithoutReplacingCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newFLAuthTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "FL", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}

	err := persistLoginCookies(ctx, dbPath, nil)
	if err == nil || !strings.Contains(err.Error(), "no usable cookies") {
		t.Fatalf("expected empty login cookie error, got %v", err)
	}
	values, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "FL")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("empty response must preserve previous cookies, got %#v", values)
	}
}

func TestEnsureLoginCookieStorageAvailableRequiresWebAuth(t *testing.T) {
	t.Parallel()

	err := ensureLoginCookieStorageAvailable(filepath.Join(t.TempDir(), "upbrr.db"))
	if !errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		t.Fatalf("expected auth helper unavailable preflight, got %v", err)
	}
}

func newFLAuthTestDB(t *testing.T) string {
	t.Helper()

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
	return dbPath
}

func configWithCredentials() config.TrackerConfig {
	return config.TrackerConfig{Username: "user", Password: "pass"}
}

func withFLHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()

	original := newHTTPClient
	newHTTPClient = func(time.Duration) *http.Client {
		return client
	}
	t.Cleanup(func() {
		newHTTPClient = original
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type failingReadCloser struct {
	data string
	done bool
}

func (body *failingReadCloser) Read(p []byte) (int, error) {
	if body.done {
		return 0, io.EOF
	}
	body.done = true
	return copy(p, body.data), errors.New("simulated read failure")
}

func (body *failingReadCloser) Close() error {
	return nil
}

func textResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
