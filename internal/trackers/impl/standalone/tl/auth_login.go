// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/sync/semaphore"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/internal/trackers/auth/totp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	loginPath        = "/user/account/login/"
	otpPath          = "/user/account/otpcheck/"
	maxAuthPageBytes = 2 << 20
)

// Serialize validation and login so concurrent operations can reuse the first
// session that gets persisted instead of replacing it with another login.
var sessionGate = semaphore.NewWeighted(1)

type authPage struct {
	fromGET   bool
	loggedIn  bool
	loginForm url.Values
	otpForm   url.Values
}

func resolveAuthSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
	return ensureCookieSession(ctx, cfg, dbPath, login, nil)
}

func lockCookieSession(ctx context.Context) (func(), error) {
	if err := sessionGate.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("TL wait for session access: %w", err)
	}
	return func() { sessionGate.Release(1) }, nil
}

func canLogIn(cfg config.TrackerConfig) bool {
	return strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
}

func ensureCookieSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest, transport http.RoundTripper) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := lockCookieSession(ctx)
	if err != nil {
		return err
	}
	defer release()

	origin, _ := url.Parse(baseURL)
	session, err := loadCookieSession(ctx, dbPath, cfg)
	if err != nil && !errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		return err
	}
	if len(session.Cookies) > 0 {
		client, err := loginClient(origin, transport)
		if err != nil {
			return err
		}
		client.Jar.SetCookies(origin, session.Cookies)
		page, err := requestAuthPage(ctx, client, origin, http.MethodGet, "/", nil)
		if err != nil {
			return err
		}
		if page.loggedIn {
			return persistRotatedCookies(ctx, &session, client.Jar.Cookies(origin))
		}
		if page.loginForm == nil && page.otpForm == nil {
			return unavailableAuth("TL returned an unrecognized session page")
		}
		// Record confirmed sign-out without deleting browser exports.
		if err := replaceAuthCookies(ctx, &session, nil, true); err != nil {
			return err
		}
	}
	if login.ValidateStoredOnly || !canLogIn(cfg) {
		return &trackerauth.AuthRequiredError{TrackerID: "TL", Reason: "import current cookies or configure a username and password"}
	}
	if _, err := authmaterial.LoadFromDBPath(dbPath); err != nil {
		return fmt.Errorf("TL encrypted storage unavailable: %w", cookies.ErrAuthHelperUnavailable)
	}
	return loginAndSaveCookies(ctx, cfg, &session, login, origin, transport)
}

func loginClient(origin *url.URL, transport http.RoundTripper) (*http.Client, error) {
	client, err := newCookieClient(30*time.Second, transport)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.Method != http.MethodGet || !sameAuthOrigin(req.URL, origin) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return client, nil
}

func sameAuthOrigin(target, origin *url.URL) bool {
	return target.Scheme == origin.Scheme && strings.EqualFold(target.Host, origin.Host) && target.User == nil
}

func loginAndSaveCookies(
	ctx context.Context,
	cfg config.TrackerConfig,
	session *cookies.TrackerHTTPSession,
	login api.TrackerAuthLoginRequest,
	origin *url.URL,
	transport http.RoundTripper,
) error {
	client, err := loginClient(origin, transport)
	if err != nil {
		return err
	}
	page, err := requestAuthPage(ctx, client, origin, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	if page.loginForm == nil {
		return unavailableAuth("TL login form unavailable")
	}
	page.loginForm.Set("username", strings.TrimSpace(cfg.Username))
	page.loginForm.Set("password", cfg.Password)
	page, err = requestAuthPage(ctx, client, origin, http.MethodPost, loginPath, page.loginForm)
	if err != nil {
		return err
	}
	if page.otpForm != nil && !page.loggedIn {
		code, err := loginCode(cfg, login)
		if err != nil {
			return err
		}
		page.otpForm.Set("otpkey", code)
		page, err = requestAuthPage(ctx, client, origin, http.MethodPost, otpPath, page.otpForm)
		if err != nil {
			return err
		}
		if !page.loggedIn && (page.otpForm != nil || page.loginForm != nil) {
			if strings.TrimSpace(login.Code) != "" {
				return trackers.ErrSubmitted2FARejected
			}
			return &trackerauth.Needs2FAError{TrackerID: "TL", Reason: "configured TOTP was rejected"}
		}
	}
	if !page.loggedIn {
		if page.loginForm != nil {
			return &trackerauth.AuthRequiredError{TrackerID: "TL", Reason: "login rejected; check the saved credentials"}
		}
		return unavailableAuth("TL login did not return an authenticated page")
	}
	if !page.fromGET {
		page, err = requestAuthPage(ctx, client, origin, http.MethodGet, "/", nil)
		if err != nil {
			return err
		}
		if !page.loggedIn {
			return unavailableAuth("TL session did not remain authenticated after login")
		}
	}
	values := client.Jar.Cookies(origin)
	if len(values) == 0 {
		return unavailableAuth("TL login did not establish session cookies")
	}
	return replaceAuthCookies(ctx, session, values, true)
}

func persistRotatedCookies(ctx context.Context, session *cookies.TrackerHTTPSession, current []*http.Cookie) error {
	if slices.EqualFunc(session.Cookies, current, func(a, b *http.Cookie) bool { return a.Name == b.Name && a.Value == b.Value }) {
		return storedSessionResult(session.CheckCurrent(ctx))
	}
	if len(current) == 0 {
		return unavailableAuth("TL validation cleared the session cookies")
	}
	return replaceAuthCookies(ctx, session, current, session.AccountBound)
}

func replaceAuthCookies(ctx context.Context, session *cookies.TrackerHTTPSession, values []*http.Cookie, accountBound bool) error {
	return storedSessionResult(session.Replace(ctx, values, accountBound))
}

func storedSessionResult(err error) error {
	if errors.Is(err, cookies.ErrTrackerSessionChanged) {
		return unavailableAuth("TL session changed during authentication; retry")
	}
	if err != nil {
		return fmt.Errorf("TL check stored session: %w", err)
	}
	return nil
}

func loginCode(cfg config.TrackerConfig, login api.TrackerAuthLoginRequest) (string, error) {
	if code := strings.TrimSpace(login.Code); code != "" {
		return code, nil
	}
	code, err := totp.FromURI(cfg.OTPURI)
	if err != nil {
		// The underlying parse error can contain the OTP URI.
		return "", &trackerauth.Needs2FAError{TrackerID: "TL", Reason: "enter a 2FA code or configure an OTP URI"}
	}
	return code, nil
}

func unavailableAuth(message string) error {
	return &trackers.AuthResolutionError{
		Reason:       message,
		PublicDetail: message,
		Transient:    true,
	}
}

func requestAuthPage(ctx context.Context, client *http.Client, origin *url.URL, method, route string, form url.Values) (authPage, error) {
	endpoint := origin.ResolveReference(&url.URL{Path: route})
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return authPage{}, unavailableAuth("TL could not create an auth request")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin.String())
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return authPage{}, fmt.Errorf("TL auth request: %w", ctxErr)
		}
		return authPage{}, unavailableAuth("TL auth request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return authPage{}, unavailableAuth(fmt.Sprintf("TL auth returned HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthPageBytes+1))
	if err != nil || len(data) > maxAuthPageBytes {
		return authPage{}, unavailableAuth("TL auth page could not be read")
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return authPage{}, unavailableAuth("TL auth page could not be parsed")
	}
	requestMethod := method
	if resp.Request != nil {
		requestMethod = resp.Request.Method
		if resp.Request.URL != nil {
			endpoint = resp.Request.URL
		}
	}
	page := authPage{fromGET: requestMethod == http.MethodGet}
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		if hasLoggedInNavbar(node) {
			page.loggedIn = true
		}
		if node.Data != "form" || !strings.EqualFold(htmlAttribute(node, "method"), http.MethodPost) {
			continue
		}
		action, err := endpoint.Parse(htmlAttribute(node, "action"))
		if err != nil || !sameAuthOrigin(action, origin) || action.RawQuery != "" || action.Fragment != "" {
			continue
		}
		switch {
		case action.Path == loginPath && htmlAttribute(node, "name") == "login-form":
			page.loginForm = authFormValues(node)
		case action.Path == otpPath && htmlAttribute(node, "name") == "otp-form":
			values := authFormValues(node)
			if values.Has("otpkey") && values.Get("username") != "" && values.Get("password") != "" {
				page.otpForm = values
			}
		}
	}
	return page, nil
}

func hasLoggedInNavbar(node *html.Node) bool {
	classes := strings.Fields(htmlAttribute(node, "class"))
	return slices.Contains(classes, "navbar") && slices.Contains(classes, "navbar-default") &&
		slices.Contains(classes, "tl") && slices.Contains(classes, "loggedin")
}

func authFormValues(form *html.Node) url.Values {
	values := make(url.Values)
	for node := range form.Descendants() {
		if node.Type != html.ElementNode || node.Data != "input" || !strings.EqualFold(htmlAttribute(node, "type"), "hidden") {
			continue
		}
		if name := htmlAttribute(node, "name"); name != "" {
			values.Add(name, htmlAttribute(node, "value"))
		}
	}
	return values
}

func htmlAttribute(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}
