// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
)

// oidcProvisionedPasswordLength is the length of the unusable random password
// assigned to an account provisioned by a first OIDC login. See
// provisionOIDCRecord.
const oidcProvisionedPasswordLength = 32

// handleOIDCLogin starts the authorization code flow and redirects to the
// provider. It is a GET because the browser is navigated here directly from the
// login page.
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}
	if !s.allowAuthRequest(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	authURL, state, err := s.oidc.AuthCodeURL(r.Context())
	if err != nil {
		s.logErrorf("web: oidc login failed stage=authorize error=%v", err)
		s.redirectToLogin(w, r, "oidc_unavailable")
		return
	}

	s.writeOIDCStateCookie(w, r, state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleOIDCCallback completes the flow. On success it mints the same session
// and cookie a password login would, so everything downstream is unchanged.
//
// Failures redirect back to the login page with a coarse reason code rather
// than rendering an error: the details belong in the log, not in a URL that
// ends up in browser history and referrer headers.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}
	if !s.allowAuthRequest(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	query := r.URL.Query()
	if providerErr := strings.TrimSpace(query.Get("error")); providerErr != "" {
		// The provider rejected the request (consent denied, policy failure).
		s.logWarnf("web: oidc login denied stage=callback provider_error=%s", redaction.RedactValue(providerErr, nil))
		s.clearOIDCStateCookie(w, r)
		s.redirectToLogin(w, r, "oidc_denied")
		return
	}

	state := strings.TrimSpace(query.Get("state"))
	code := strings.TrimSpace(query.Get("code"))
	if state == "" || code == "" {
		s.clearOIDCStateCookie(w, r)
		s.redirectToLogin(w, r, "oidc_failed")
		return
	}

	// The state must match the cookie set when the flow started. This binds the
	// callback to the browser that began it, so an attacker cannot complete a
	// flow of their own in a victim's session.
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		s.logWarnf("web: oidc login rejected stage=callback reason=state_mismatch")
		s.clearOIDCStateCookie(w, r)
		s.redirectToLogin(w, r, "oidc_failed")
		return
	}
	s.clearOIDCStateCookie(w, r)

	claims, err := s.oidc.Exchange(r.Context(), state, code)
	if err != nil {
		s.logErrorf("web: oidc login failed stage=exchange error=%v", err)
		s.redirectToLogin(w, r, "oidc_failed")
		return
	}

	identity := claims.Username()
	if identity == "" {
		s.logErrorf("web: oidc login failed stage=claims reason=no_username_claim")
		s.redirectToLogin(w, r, "oidc_failed")
		return
	}

	username, err := s.resolveOIDCAccount(identity)
	if err != nil {
		s.logErrorf("web: oidc login failed stage=account error=%v", err)
		s.redirectToLogin(w, r, "oidc_failed")
		return
	}

	// Retained: an SSO session should survive a browser restart, matching the
	// expectation set by the identity provider's own session.
	current, err := s.sessions.Create(username, true)
	if err != nil {
		s.logErrorf("web: oidc login failed stage=session error=%v", err)
		s.redirectToLogin(w, r, "oidc_failed")
		return
	}
	s.writeSessionCookie(w, r, current)
	s.logInfof("web: oidc login succeeded username=%s", redactAuthUsername(username))

	http.Redirect(w, r, s.loginRedirectTarget(), http.StatusFound)
}

// resolveOIDCAccount maps a verified OIDC identity onto upbrr's single local
// account and returns the username to attach to the session.
//
// upbrr stores one account. Its username and encryption seed derive the key
// that protects tracker credentials, so an OIDC login must reuse the existing
// record rather than create a parallel identity — otherwise stored tracker
// secrets would no longer decrypt. Authorization is the provider's job: if the
// provider issued a valid ID token, the user is allowed in.
func (s *Server) resolveOIDCAccount(identity string) (string, error) {
	exists, err := wrapWebResult(s.auth.Exists())
	if err != nil {
		return "", err
	}
	if !exists {
		return s.provisionOIDCRecord(identity)
	}

	record, err := wrapWebResult(s.auth.Load())
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(strings.TrimSpace(record.Username), identity) {
		// Not fatal: the local account predates SSO and may be named
		// differently from the provider identity. Log it so an operator can
		// see which provider identity is driving the local account.
		s.logInfof(
			"web: oidc identity mapped to local account oidc_user=%s local_user=%s",
			redactAuthUsername(identity),
			redactAuthUsername(record.Username),
		)
	}
	return record.Username, nil
}

// provisionOIDCRecord creates the local account on the first OIDC login of a
// fresh install.
//
// The account is given a random password that is never shown or stored in
// plaintext. This is deliberate: the record's real job is to carry the
// encryption seed that protects tracker credentials, and Bootstrap requires a
// password to produce a complete record. Sign-in goes through the provider, so
// the password is dead weight by design — not a backdoor, since nobody
// (including this process, after this function returns) knows it.
func (s *Server) provisionOIDCRecord(identity string) (string, error) {
	password, err := randomString(oidcProvisionedPasswordLength)
	if err != nil {
		return "", err
	}
	if err := wrapWebError(s.auth.Bootstrap(identity, password)); err != nil {
		return "", err
	}
	s.logInfof("web: oidc provisioned local account username=%s", redactAuthUsername(identity))
	return identity, nil
}

// writeOIDCStateCookie stores the flow state for the duration of the round trip.
func (s *Server) writeOIDCStateCookie(w http.ResponseWriter, r *http.Request, state string) {
	//nolint:gosec // State cookie sets HttpOnly, SameSite, and Secure for HTTPS requests.
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    state,
		Path:     s.sessionCookiePath(),
		HttpOnly: true,
		// Lax, not Strict: the provider redirects the browser back to the
		// callback cross-site, and a Strict cookie would not be sent with it.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.requestScheme(r) == "https",
		MaxAge:   int(oidcFlowTTL.Seconds()),
	})
}

// clearOIDCStateCookie removes the state cookie once a flow has completed or
// failed, so a stale state cannot linger in the browser.
func (s *Server) clearOIDCStateCookie(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // State clear cookie sets HttpOnly, SameSite, and Secure for HTTPS requests.
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     s.sessionCookiePath(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.requestScheme(r) == "https",
		MaxAge:   -1,
	})
}

// loginRedirectTarget returns the app root, honouring a configured base path.
func (s *Server) loginRedirectTarget() string {
	if basePath := s.externalBasePath(); basePath != "" {
		return basePath
	}
	return "/"
}

// redirectToLogin sends the browser back to the UI with a coarse reason code
// the login page can turn into a message.
func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request, reason string) {
	target := s.loginRedirectTarget()
	if !strings.HasSuffix(target, "/") {
		target += "/"
	}
	http.Redirect(w, r, target+"?oidc_error="+reason, http.StatusFound)
}
