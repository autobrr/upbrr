// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	arDefaultBaseURL  = "https://alpharatio.cc"
	authPreviewBytes  = 64 * 1024
	arBrowsePath      = "/torrents.php"
	ffDefaultBaseURL  = "https://www.funfile.org"
	ffLoginPath       = "/takelogin.php"
	ffUploadPath      = "/upload.php"
	flDefaultBaseURL  = "https://filelist.io"
	flIndexPath       = "/index.php"
	flLoginPagePath   = "/login.php"
	flLoginPath       = "/takelogin.php"
	hdbDefaultBaseURL = "https://hdbits.org"
	hdbUploadPath     = "/upload/upload"
	thrDefaultBaseURL = "https://www.torrenthr.org"
	thrLoginPath      = "/takelogin.php"
)

// flValidatorPattern extracts the anti-CSRF validator required by FL login.
var flValidatorPattern = regexp.MustCompile(`name="validator"\s+value="([^"]+)"`)

// resolveARStoredSessionForTrackerAuth verifies imported AR cookies against
// the browse page. Missing cookies require user auth material, login redirects
// or logged-out page markers invalidate stored cookies, and transport or
// unexpected HTTP failures preserve stored cookies as transient failures.
func resolveARStoredSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "AR", "alpharatio.cc")
	if err != nil || len(values) == 0 {
		return &AuthRequiredError{TrackerID: "AR", Reason: "cookies missing", Err: cookieLoadError("AR", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinAuthURL(resolveAuthBaseURL(cfg, arDefaultBaseURL), arBrowsePath), nil)
	if err != nil {
		return fmt.Errorf("trackers: AR session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, values)

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return &ValidationError{TrackerID: "AR", Transient: true, Reason: "remote validation unavailable", Err: fmt.Errorf("trackers: AR session validation request: %w", err)}
	}
	defer resp.Body.Close()
	body, readErr := readTrackerAuthResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 300)
	if readErr != nil {
		return &ValidationError{TrackerID: "AR", Transient: true, Reason: "remote validation unavailable", Err: readErr}
	}
	if isLoginRedirect(resp) || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || arLooksLoggedOut(string(body)) {
		return &ValidationError{TrackerID: "AR", ConfirmedInvalid: true, Reason: "stored session expired", Err: fmt.Errorf("trackers: AR session validation failed status=%d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ValidationError{TrackerID: "AR", Transient: true, Reason: "remote validation failed", Err: fmt.Errorf("trackers: AR session validation failed status=%d", resp.StatusCode)}
	}
	return nil
}

// resolveFFSessionForTrackerAuth verifies stored FF cookies against the upload
// page, or logs in with configured credentials and persists returned cookies.
func resolveFFSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	baseURL := resolveAuthBaseURL(cfg, ffDefaultBaseURL)
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "FF", "www.funfile.org")
	if err == nil && len(values) > 0 {
		validationErr := validateFFStoredCookies(ctx, baseURL, values)
		if validationErr == nil {
			return nil
		}
		if !shouldRefreshStoredCookiesWithCredentials(validationErr) || !hasLoginCredentials(cfg) {
			return validationErr
		}
	}
	if !hasLoginCredentials(cfg) {
		return &AuthRequiredError{TrackerID: "FF", Reason: "cookies or username/password missing", Err: cookieLoadError("FF", err)}
	}
	return loginFFForTrackerAuth(ctx, cfg, dbPath, baseURL)
}

// validateFFStoredCookies checks imported FF cookies without mutating storage.
// Login pages and missing logged-in markers are confirmed invalid; transport
// and unexpected HTTP failures are transient.
func validateFFStoredCookies(ctx context.Context, baseURL string, values []*http.Cookie) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinAuthURL(baseURL, ffUploadPath), nil)
	if err != nil {
		return fmt.Errorf("trackers: FF session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, values)

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return &ValidationError{TrackerID: "FF", Transient: true, Reason: "remote validation unavailable", Err: fmt.Errorf("trackers: FF session validation request: %w", err)}
	}
	defer resp.Body.Close()
	body, readErr := readTrackerAuthResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 300)
	if readErr != nil {
		return &ValidationError{TrackerID: "FF", Transient: true, Reason: "remote validation unavailable", Err: readErr}
	}
	if isLoginRedirect(resp) || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || ffLooksLoggedOut(string(body)) {
		return &ValidationError{TrackerID: "FF", ConfirmedInvalid: true, Reason: "stored session expired", Err: fmt.Errorf("trackers: FF session validation failed status=%d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ValidationError{TrackerID: "FF", Transient: true, Reason: "remote validation failed", Err: fmt.Errorf("trackers: FF session validation failed status=%d", resp.StatusCode)}
	}
	if !ffLooksLoggedIn(string(body)) {
		return &ValidationError{TrackerID: "FF", ConfirmedInvalid: true, Reason: "stored session expired", Err: errors.New("trackers: FF login marker not found")}
	}
	return nil
}

// loginFFForTrackerAuth performs the FF credential login flow, verifies the
// returned cookies against the upload page, then persists them.
func loginFFForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string, baseURL string) error {
	data := url.Values{}
	data.Set("returnto", "/index.php")
	data.Set("username", strings.TrimSpace(cfg.Username))
	data.Set("password", strings.TrimSpace(cfg.Password))
	data.Set("login", "Login")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinAuthURL(baseURL, ffLoginPath), strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("trackers: FF login request build: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("trackers: FF login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return &ValidationError{TrackerID: "FF", ConfirmedInvalid: true, Reason: "login failed", Err: fmt.Errorf("trackers: FF login failed status=%d", resp.StatusCode)}
	}
	loginCookies := usableHTTPCookies(resp.Cookies())
	if len(loginCookies) == 0 {
		return errors.New("trackers: FF login returned no usable cookies")
	}
	if err := validateFFStoredCookies(ctx, baseURL, loginCookies); err != nil {
		return fmt.Errorf("trackers: FF validate login cookies: %w", err)
	}
	if err := persistHTTPCookies(ctx, dbPath, "FF", loginCookies); err != nil {
		return fmt.Errorf("trackers: FF persist login cookies: %w", err)
	}
	return nil
}

// resolveFLSessionForTrackerAuth verifies stored FL cookies against the index
// page, or logs in with configured credentials and persists returned cookies.
func resolveFLSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	baseURL := resolveAuthBaseURL(cfg, flDefaultBaseURL)
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "FL", ".filelist.io")
	if err == nil && len(values) > 0 {
		validationErr := validateFLStoredCookies(ctx, baseURL, values)
		if validationErr == nil {
			return nil
		}
		if !shouldRefreshStoredCookiesWithCredentials(validationErr) || !hasLoginCredentials(cfg) {
			return validationErr
		}
	}
	if !hasLoginCredentials(cfg) {
		return &AuthRequiredError{TrackerID: "FL", Reason: "cookies or username/password missing", Err: cookieLoadError("FL", err)}
	}
	return loginFLForTrackerAuth(ctx, cfg, dbPath, baseURL)
}

// validateFLStoredCookies checks imported FL cookies against the index page
// without mutating storage. Logout text is treated as the logged-in marker.
func validateFLStoredCookies(ctx context.Context, baseURL string, values []*http.Cookie) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinAuthURL(baseURL, flIndexPath), nil)
	if err != nil {
		return fmt.Errorf("trackers: FL session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, values)

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return &ValidationError{TrackerID: "FL", Transient: true, Reason: "remote validation unavailable", Err: fmt.Errorf("trackers: FL session validation request: %w", err)}
	}
	defer resp.Body.Close()
	body, readErr := readTrackerAuthResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 300)
	if readErr != nil {
		return &ValidationError{TrackerID: "FL", Transient: true, Reason: "remote validation unavailable", Err: readErr}
	}
	if isLoginRedirect(resp) || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || flLooksLoggedOut(string(body)) {
		return &ValidationError{TrackerID: "FL", ConfirmedInvalid: true, Reason: "stored session expired", Err: fmt.Errorf("trackers: FL session validation failed status=%d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ValidationError{TrackerID: "FL", Transient: true, Reason: "remote validation failed", Err: fmt.Errorf("trackers: FL session validation failed status=%d", resp.StatusCode)}
	}
	if !flLooksLoggedIn(string(body)) {
		return &ValidationError{TrackerID: "FL", ConfirmedInvalid: true, Reason: "stored session expired", Err: errors.New("trackers: FL logout marker not found")}
	}
	return nil
}

// loginFLForTrackerAuth fetches FL's login validator, submits credentials, and
// persists the resulting cookie jar when login succeeds.
func loginFLForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string, baseURL string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("trackers: FL create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinAuthURL(baseURL, flLoginPagePath), nil)
	if err != nil {
		return fmt.Errorf("trackers: FL login page request build: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: FL login page request: %w", err)
	}
	body, readErr := readTrackerAuthResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 300)
	resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("trackers: FL read login page response: %w", readErr)
	}
	match := flValidatorPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return errors.New("trackers: FL validator token not found")
	}
	data := url.Values{}
	data.Set("validator", match[1])
	data.Set("username", strings.TrimSpace(cfg.Username))
	data.Set("password", strings.TrimSpace(cfg.Password))
	data.Set("unlock", "1")
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinAuthURL(baseURL, flLoginPath), strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("trackers: FL login request build: %w", err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return fmt.Errorf("trackers: FL login request: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode < 200 || loginResp.StatusCode >= 400 {
		return &ValidationError{TrackerID: "FL", ConfirmedInvalid: true, Reason: "login failed", Err: fmt.Errorf("trackers: FL login failed status=%d", loginResp.StatusCode)}
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return fmt.Errorf("trackers: FL parse base URL: %w", err)
	}
	if err := persistHTTPCookies(ctx, dbPath, "FL", jar.Cookies(base)); err != nil {
		return fmt.Errorf("trackers: FL persist login cookies: %w", err)
	}
	return nil
}

// resolveHDBStoredSessionForTrackerAuth verifies the HDB upload prerequisites:
// username/passkey config plus imported cookies that can reach the upload page.
// Confirmed login responses invalidate stored cookies; remote failures remain
// transient so a temporary tracker outage does not discard auth material.
func resolveHDBStoredSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Passkey) == "" {
		return &AuthRequiredError{TrackerID: "HDB", Reason: "username/passkey missing", Err: errors.New("trackers: HDB missing username/passkey")}
	}
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDB", "hdbits.org")
	if err != nil || len(values) == 0 {
		return &AuthRequiredError{TrackerID: "HDB", Reason: "cookies missing", Err: cookieLoadError("HDB", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinAuthURL(resolveAuthBaseURL(cfg, hdbDefaultBaseURL), hdbUploadPath), nil)
	if err != nil {
		return fmt.Errorf("trackers: HDB session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, values)

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return &ValidationError{TrackerID: "HDB", Transient: true, Reason: "remote validation unavailable", Err: fmt.Errorf("trackers: HDB session validation request: %w", err)}
	}
	defer resp.Body.Close()
	body, readErr := readTrackerAuthResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 300)
	if readErr != nil {
		return &ValidationError{TrackerID: "HDB", Transient: true, Reason: "remote validation unavailable", Err: readErr}
	}
	if isLoginRedirect(resp) || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || hdbLooksLoggedOut(string(body)) {
		return &ValidationError{TrackerID: "HDB", ConfirmedInvalid: true, Reason: "stored session expired", Err: fmt.Errorf("trackers: HDB session validation failed status=%d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ValidationError{TrackerID: "HDB", Transient: true, Reason: "remote validation failed", Err: fmt.Errorf("trackers: HDB session validation failed status=%d", resp.StatusCode)}
	}
	return nil
}

// resolveTHRSessionForTrackerAuth validates THR credentials by performing a
// login check. THR uploads log in per request and do not use stored cookies.
func resolveTHRSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, _ string, _ api.TrackerAuthLoginRequest) error {
	if !hasLoginCredentials(cfg) {
		return &AuthRequiredError{TrackerID: "THR", Reason: "username/password missing", Err: errors.New("trackers: THR missing username/password")}
	}
	baseURL := resolveAuthBaseURL(cfg, thrDefaultBaseURL)
	data := url.Values{}
	data.Set("username", strings.TrimSpace(cfg.Username))
	data.Set("password", strings.TrimSpace(cfg.Password))
	data.Set("ssl", "yes")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinAuthURL(baseURL, thrLoginPath), strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("trackers: THR login request build: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: THR login request: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := readTrackerAuthResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 400)
	if readErr != nil {
		return &ValidationError{TrackerID: "THR", Transient: true, Reason: "remote validation unavailable", Err: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return &ValidationError{TrackerID: "THR", ConfirmedInvalid: true, Reason: "login failed", Err: fmt.Errorf("trackers: THR login failed status=%d", resp.StatusCode)}
	}
	if !thrLooksLoggedIn(string(body), resp) {
		return &ValidationError{TrackerID: "THR", ConfirmedInvalid: true, Reason: "login failed", Err: errors.New("trackers: THR login marker not found")}
	}
	return nil
}

// readTrackerAuthResponseBody reads full success-candidate auth pages for
// marker parsing while keeping failed-status diagnostics bounded.
func readTrackerAuthResponseBody(resp *http.Response, successCandidate bool) ([]byte, error) {
	if successCandidate {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("trackers: read auth response body: %w", err)
		}
		return body, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, authPreviewBytes))
	if err != nil {
		return nil, fmt.Errorf("trackers: read auth response preview: %w", err)
	}
	return body, nil
}

// shouldRefreshStoredCookiesWithCredentials reports whether stored cookies were
// proven expired, allowing credential login to refresh them.
func shouldRefreshStoredCookiesWithCredentials(err error) bool {
	validation, ok := asValidationError(err)
	return ok && validation.ConfirmedInvalid && !validation.Transient
}

// persistHTTPCookies saves non-empty login cookies and rejects empty login
// responses so tracker auth never reports success without durable auth material.
func persistHTTPCookies(ctx context.Context, dbPath string, trackerID string, values []*http.Cookie) error {
	valid := usableHTTPCookies(values)
	if len(valid) == 0 {
		return fmt.Errorf("trackers: %s login returned no usable cookies", trackerID)
	}
	if err := cookies.SaveTrackerHTTPCookies(ctx, dbPath, trackerID, valid); err != nil {
		return fmt.Errorf("trackers: %s save cookies: %w", trackerID, err)
	}
	return nil
}

// usableHTTPCookies keeps only cookies with non-empty names and values so login
// flows never persist placeholders as usable auth material.
func usableHTTPCookies(values []*http.Cookie) []*http.Cookie {
	valid := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		valid = append(valid, cookie)
	}
	return valid
}

// cookieLoadError preserves the underlying cookie load failure when available
// while providing a stable tracker-specific error for empty cookie storage.
func cookieLoadError(trackerID string, err error) error {
	if err != nil {
		return fmt.Errorf("trackers: %s load cookies: %w", trackerID, err)
	}
	return fmt.Errorf("trackers: %s cookies not found", trackerID)
}

// noRedirectHTTPClient exposes login redirects as validation evidence instead
// of following them and classifying the login page as a successful response.
func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// resolveAuthBaseURL applies a tracker override when configured and otherwise
// falls back to the site's production base URL.
func resolveAuthBaseURL(cfg config.TrackerConfig, fallback string) string {
	if value := strings.TrimRight(strings.TrimSpace(cfg.URL), "/"); value != "" {
		return value
	}
	return fallback
}

// joinAuthURL resolves site-relative validation paths against tracker base URLs
// without using path joins that would corrupt URL semantics.
func joinAuthURL(baseURL string, urlPath string) string {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(baseURL, "/") + urlPath
	}
	ref, err := url.Parse(strings.TrimLeft(urlPath, "/"))
	if err != nil {
		return strings.TrimRight(baseURL, "/") + urlPath
	}
	return parsed.ResolveReference(ref).String()
}

// isLoginRedirect reports redirects whose target path indicates an unauthenticated session.
func isLoginRedirect(resp *http.Response) bool {
	if resp == nil || resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	location := strings.ToLower(strings.TrimSpace(resp.Header.Get("Location")))
	return strings.Contains(location, "login")
}

// arLooksLoggedOut recognizes AR login/recovery page markers in validation responses.
func arLooksLoggedOut(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "forgot your password") || strings.Contains(lower, "login.php?act=recover")
}

// hdbLooksLoggedOut recognizes HDB login form markers in validation responses.
func hdbLooksLoggedOut(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "login.php") || strings.Contains(lower, "name=\"username\"") || strings.Contains(lower, "name=\"password\"")
}

// ffLooksLoggedIn recognizes the FF upload-page marker used by Upload Assistant.
func ffLooksLoggedIn(body string) bool {
	return strings.Contains(strings.ToLower(body), "friends.php")
}

// ffLooksLoggedOut recognizes FF login form markers in validation responses.
func ffLooksLoggedOut(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "takelogin.php") || strings.Contains(lower, "name=\"username\"") || strings.Contains(lower, "name=\"password\"")
}

// flLooksLoggedIn recognizes FL's logout marker on authenticated pages.
func flLooksLoggedIn(body string) bool {
	return strings.Contains(strings.ToLower(body), "logout")
}

// flLooksLoggedOut recognizes FL login form markers in validation responses.
func flLooksLoggedOut(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "login.php") || strings.Contains(lower, "name=\"username\"") || strings.Contains(lower, "name=\"password\"")
}

// thrLooksLoggedIn recognizes THR credential-login success by response body or
// final URL because THR uploads use per-request login instead of stored cookies.
func thrLooksLoggedIn(body string, resp *http.Response) bool {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "logout.php") {
		return true
	}
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		path := strings.ToLower(resp.Request.URL.Path)
		return strings.Contains(path, "index.php")
	}
	return false
}
