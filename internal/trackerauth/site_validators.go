// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	arDefaultBaseURL  = "https://alpharatio.cc"
	arBrowsePath      = "/torrents.php"
	hdbDefaultBaseURL = "https://hdbits.org"
	hdbUploadPath     = "/upload/upload"
)

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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if isLoginRedirect(resp) || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || arLooksLoggedOut(string(body)) {
		return &ValidationError{TrackerID: "AR", ConfirmedInvalid: true, Reason: "stored session expired", Err: fmt.Errorf("trackers: AR session validation failed status=%d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ValidationError{TrackerID: "AR", Transient: true, Reason: "remote validation failed", Err: fmt.Errorf("trackers: AR session validation failed status=%d", resp.StatusCode)}
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if isLoginRedirect(resp) || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || hdbLooksLoggedOut(string(body)) {
		return &ValidationError{TrackerID: "HDB", ConfirmedInvalid: true, Reason: "stored session expired", Err: fmt.Errorf("trackers: HDB session validation failed status=%d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ValidationError{TrackerID: "HDB", Transient: true, Reason: "remote validation failed", Err: fmt.Errorf("trackers: HDB session validation failed status=%d", resp.StatusCode)}
	}
	return nil
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
