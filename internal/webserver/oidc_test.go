// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIDP is a minimal OpenID provider: discovery, JWKS, and a token endpoint
// that returns a signed ID token. It exists so the callback path is exercised
// against real signature and nonce verification rather than a stubbed verifier.
type fakeIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	// nonce is echoed into the next issued ID token.
	nonce string
	// subject and preferredUsername shape the identity claims.
	subject           string
	preferredUsername string
	// pkceSupported controls the advertised code_challenge_methods_supported.
	pkceSupported bool
	// lastCodeVerifier records what the client sent, so PKCE can be asserted.
	lastCodeVerifier string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	idp := &fakeIDP{
		key:               key,
		subject:           "user-subject-1",
		preferredUsername: "example-user",
		pkceSupported:     true,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		doc := map[string]any{
			"issuer":                 idp.server.URL,
			"authorization_endpoint": idp.server.URL + "/authorize",
			"token_endpoint":         idp.server.URL + "/token",
			"jwks_uri":               idp.server.URL + "/jwks",
		}
		if idp.pkceSupported {
			doc["code_challenge_methods_supported"] = []string{"S256"}
		}
		writeJSON(w, http.StatusOK, doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       key.Public(),
			KeyID:     "test-key",
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}}
		writeJSON(w, http.StatusOK, set)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		idp.lastCodeVerifier = r.Form.Get("code_verifier")
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"id_token":     idp.signIDToken(t),
		})
	})

	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (f *fakeIDP) signIDToken(t *testing.T) string {
	t.Helper()

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), "test-key"),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	now := time.Now()
	claims := map[string]any{
		"iss":                f.server.URL,
		"aud":                "test-client-id",
		"sub":                f.subject,
		"exp":                now.Add(time.Hour).Unix(),
		"iat":                now.Unix(),
		"nonce":              f.nonce,
		"preferred_username": f.preferredUsername,
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	return raw
}

// oidcTestServer wires a server with OIDC pointed at the fake provider.
func oidcTestServer(t *testing.T, idp *fakeIDP, disableBuiltInLogin bool) *Server {
	t.Helper()

	srv := newAuthTestServer(t, filepath.Join(t.TempDir(), "db.sqlite"))
	srv.oidc = newOIDCService(OIDCConfig{
		Enabled:             true,
		Issuer:              idp.server.URL,
		ClientID:            "test-client-id",
		ClientSecret:        "test-client-secret",
		RedirectURL:         "https://upbrr.example.test/api/auth/oidc/callback",
		Scopes:              DefaultOIDCScopes,
		DisableBuiltInLogin: disableBuiltInLogin,
	}, func(string, ...any) {})
	return srv
}

// startFlow drives /api/auth/oidc/login and returns the state the server minted
// plus the state cookie it set.
func startFlow(t *testing.T, srv *Server, idp *fakeIDP) (string, *http.Cookie) {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.handleOIDCLogin(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/auth/oidc/login", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("login: status = %d, want %d", rec.Code, http.StatusFound)
	}

	redirect, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	state := redirect.Query().Get("state")
	if state == "" {
		t.Fatal("login: redirect carried no state")
	}
	if got := redirect.Query().Get("code_challenge_method"); got != "S256" {
		t.Fatalf("login: code_challenge_method = %q, want S256", got)
	}
	// The nonce the server generated must be the one the provider signs, or
	// verification will (correctly) fail.
	idp.nonce = redirect.Query().Get("nonce")
	if idp.nonce == "" {
		t.Fatal("login: redirect carried no nonce")
	}

	var stateCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == oidcStateCookieName {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("login: no state cookie set")
	}
	return state, stateCookie
}

func callback(t *testing.T, srv *Server, state string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/auth/oidc/callback?state="+url.QueryEscape(state)+"&code=test-code", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.handleOIDCCallback(rec, req)
	return rec
}

// TestOIDCCallbackProvisionsAccountAndSession covers the first login on a fresh
// install: no local account exists, so one is provisioned and a session issued.
func TestOIDCCallbackProvisionsAccountAndSession(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	state, cookie := startFlow(t, srv, idp)
	rec := callback(t, srv, state, cookie)

	if rec.Code != http.StatusFound {
		t.Fatalf("callback: status = %d, want %d (body %s)", rec.Code, http.StatusFound, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/" {
		t.Fatalf("callback: redirected to %q, want %q", location, "/")
	}
	if idp.lastCodeVerifier == "" {
		t.Fatal("callback: no PKCE code_verifier sent to the token endpoint")
	}

	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("callback: no session cookie issued")
	}
	if _, ok := srv.sessions.Get(sessionCookie.Value); !ok {
		t.Fatal("callback: session cookie does not resolve to a live session")
	}

	// The account must exist afterwards, and carry the encryption seed that
	// protects tracker credentials.
	record, err := srv.auth.Load()
	if err != nil {
		t.Fatalf("load auth record: %v", err)
	}
	if record.Username != "example-user" {
		t.Fatalf("record username = %q, want %q", record.Username, "example-user")
	}
	if strings.TrimSpace(record.EncryptionKeySeed) == "" {
		t.Fatal("provisioned record has no encryption key seed")
	}
}

// TestOIDCCallbackReusesExistingAccount is the upgrade path: an install that
// already has a local account must keep using it, because its username and seed
// derive the key protecting stored tracker credentials.
func TestOIDCCallbackReusesExistingAccount(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	if err := srv.auth.Bootstrap("local-admin", "correct-horse-battery"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	before, err := srv.auth.Load()
	if err != nil {
		t.Fatalf("load before: %v", err)
	}

	state, cookie := startFlow(t, srv, idp)
	rec := callback(t, srv, state, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback: status = %d, want %d", rec.Code, http.StatusFound)
	}

	after, err := srv.auth.Load()
	if err != nil {
		t.Fatalf("load after: %v", err)
	}
	if after.Username != "local-admin" {
		t.Fatalf("username changed to %q; existing account must be reused", after.Username)
	}
	if after.EncryptionKeySeed != before.EncryptionKeySeed {
		t.Fatal("encryption key seed changed: stored tracker credentials would no longer decrypt")
	}

	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("callback: no session cookie issued")
	}
	current, ok := srv.sessions.Get(sessionCookie.Value)
	if !ok {
		t.Fatal("callback: session not found")
	}
	if current.Username != "local-admin" {
		t.Fatalf("session username = %q, want the local account", current.Username)
	}
}

// TestOIDCCallbackRejectsStateMismatch guards the browser binding: a callback
// whose state does not match the cookie set at authorize time must be refused.
func TestOIDCCallbackRejectsStateMismatch(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	state, _ := startFlow(t, srv, idp)
	rec := callback(t, srv, state, &http.Cookie{Name: oidcStateCookieName, Value: "attacker-state"})

	if rec.Code != http.StatusFound {
		t.Fatalf("callback: status = %d, want redirect", rec.Code)
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "oidc_error=") {
		t.Fatalf("callback: expected an error redirect, got %q", location)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatal("callback: a session was issued despite a state mismatch")
		}
	}
}

// TestOIDCCallbackRejectsMissingStateCookie covers the same binding when the
// cookie is absent entirely, e.g. a callback URL fed to a victim.
func TestOIDCCallbackRejectsMissingStateCookie(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	state, _ := startFlow(t, srv, idp)
	rec := callback(t, srv, state, nil)

	if location := rec.Header().Get("Location"); !strings.Contains(location, "oidc_error=") {
		t.Fatalf("callback: expected an error redirect, got %q", location)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatal("callback: a session was issued with no state cookie")
		}
	}
}

// TestOIDCFlowIsSingleUse ensures an authorization state cannot be replayed.
func TestOIDCFlowIsSingleUse(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	state, cookie := startFlow(t, srv, idp)
	if rec := callback(t, srv, state, cookie); rec.Code != http.StatusFound {
		t.Fatalf("first callback: status = %d", rec.Code)
	}

	replay := callback(t, srv, state, cookie)
	if location := replay.Header().Get("Location"); !strings.Contains(location, "oidc_error=") {
		t.Fatalf("replayed callback was accepted; redirect was %q", location)
	}
}

// TestOIDCNonceMismatchIsRejected proves the ID token nonce is actually checked:
// a token minted for a different flow must not be accepted.
func TestOIDCNonceMismatchIsRejected(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	state, cookie := startFlow(t, srv, idp)
	idp.nonce = "nonce-from-a-different-flow"

	rec := callback(t, srv, state, cookie)
	if location := rec.Header().Get("Location"); !strings.Contains(location, "oidc_error=") {
		t.Fatalf("callback with a mismatched nonce was accepted; redirect was %q", location)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatal("callback: a session was issued despite a nonce mismatch")
		}
	}
}

// TestDisableBuiltInLoginBlocksPasswordRoutes is the core security property of
// the flag: hiding the form in the UI is not enough, the endpoints must refuse.
func TestDisableBuiltInLoginBlocksPasswordRoutes(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	loginBody := strings.NewReader(`{"username":"local-admin","password":"correct-horse-battery"}`)
	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login", loginBody)
	loginRec := httptest.NewRecorder()
	srv.handleLogin(loginRec, loginReq, session{})
	if loginRec.Code != http.StatusForbidden {
		t.Fatalf("password login: status = %d, want %d", loginRec.Code, http.StatusForbidden)
	}

	// Bootstrap must be closed too, or an unauthenticated caller could still
	// claim the account on an SSO-only deployment.
	bootstrapBody := strings.NewReader(`{"username":"attacker","password":"correct-horse-battery"}`)
	bootstrapReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/bootstrap", bootstrapBody)
	bootstrapRec := httptest.NewRecorder()
	srv.handleBootstrap(bootstrapRec, bootstrapReq, session{})
	if bootstrapRec.Code != http.StatusForbidden {
		t.Fatalf("bootstrap: status = %d, want %d", bootstrapRec.Code, http.StatusForbidden)
	}

	exists, err := srv.auth.Exists()
	if err != nil {
		t.Fatalf("auth exists: %v", err)
	}
	if exists {
		t.Fatal("bootstrap created an account while the built-in login was disabled")
	}
}

// TestBuiltInLoginStillWorksAlongsideOIDC ensures OIDC is additive by default:
// enabling it must not break password login unless explicitly disabled.
func TestBuiltInLoginStillWorksAlongsideOIDC(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, false)

	if err := srv.auth.Bootstrap("local-admin", "correct-horse-battery"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	body := strings.NewReader(`{"username":"local-admin","password":"correct-horse-battery"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login", body)
	rec := httptest.NewRecorder()
	srv.handleLogin(rec, req, session{})

	if rec.Code != http.StatusOK {
		t.Fatalf("password login: status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestAuthStatusAdvertisesOIDC is what the login page keys off to decide whether
// to render the SSO button and whether to hide the password form.
func TestAuthStatusAdvertisesOIDC(t *testing.T) {
	idp := newFakeIDP(t)
	srv := oidcTestServer(t, idp, true)

	rec := httptest.NewRecorder()
	srv.handleAuthStatus(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/auth/status", nil), session{})

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if payload["oidcEnabled"] != true {
		t.Fatalf("oidcEnabled = %v, want true", payload["oidcEnabled"])
	}
	if payload["oidcDisableBuiltInLogin"] != true {
		t.Fatalf("oidcDisableBuiltInLogin = %v, want true", payload["oidcDisableBuiltInLogin"])
	}
	// No account exists yet, but the setup form must not be offered: the first
	// OIDC login provisions the account instead.
	if payload["needsSetup"] != false {
		t.Fatalf("needsSetup = %v, want false on an SSO-only deployment", payload["needsSetup"])
	}
}

// TestOIDCRoutesAreAbsentWhenDisabled keeps the feature entirely inert when off.
func TestOIDCRoutesAreAbsentWhenDisabled(t *testing.T) {
	srv := newAuthTestServer(t, filepath.Join(t.TempDir(), "db.sqlite"))
	srv.oidc = newOIDCService(OIDCConfig{Enabled: false}, func(string, ...any) {})

	if srv.oidc != nil {
		t.Fatal("newOIDCService returned a service while disabled")
	}

	rec := httptest.NewRecorder()
	srv.handleOIDCLogin(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/auth/oidc/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("oidc login while disabled: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestOIDCScopeListAlwaysRequestsOpenID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "adds openid", input: "profile email", want: []string{"openid", "profile", "email"}},
		{name: "keeps openid once", input: "openid openid profile", want: []string{"openid", "profile"}},
		{name: "empty", input: "", want: []string{"openid"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := oidcScopeList(test.input)
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Fatalf("oidcScopeList(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeOIDCConfig(t *testing.T) {
	valid := OIDCConfig{
		Enabled:      true,
		Issuer:       "https://auth.example.test/application/o/upbrr/",
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURL:  "https://upbrr.example.test/api/auth/oidc/callback",
	}

	t.Run("applies default scopes", func(t *testing.T) {
		got, err := normalizeOIDCConfig(valid)
		if err != nil {
			t.Fatalf("normalizeOIDCConfig: %v", err)
		}
		if got.Scopes != DefaultOIDCScopes {
			t.Fatalf("scopes = %q, want %q", got.Scopes, DefaultOIDCScopes)
		}
	})

	t.Run("rejects missing fields", func(t *testing.T) {
		cfg := valid
		cfg.ClientSecret = ""
		if _, err := normalizeOIDCConfig(cfg); err == nil {
			t.Fatal("expected an error for a missing client secret")
		}
	})

	t.Run("rejects a relative issuer", func(t *testing.T) {
		cfg := valid
		cfg.Issuer = "auth.example.test"
		if _, err := normalizeOIDCConfig(cfg); err == nil {
			t.Fatal("expected an error for a scheme-less issuer")
		}
	})

	// The lockout guard: this combination would leave no way to sign in.
	t.Run("rejects disabling the built-in login without oidc", func(t *testing.T) {
		cfg := OIDCConfig{Enabled: false, DisableBuiltInLogin: true}
		if _, err := normalizeOIDCConfig(cfg); err == nil {
			t.Fatal("expected an error when disabling the only remaining login method")
		}
	})
}
