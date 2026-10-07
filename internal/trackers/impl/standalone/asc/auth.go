// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var errSessionExpired = errors.New("ASC session expired or cookies invalid")

// missingCookiesReason is the blocking reason when no ASC cookies are stored.
const missingCookiesReason = "missing valid ASC cookies"

// siteURL is the cookie-jar scope for the ASC session.
var siteURL = &url.URL{Scheme: "https", Host: cookieDomain}

// LoadCookies loads ASC cookies from shared storage for the ASC web domain.
// The legacy source-label return value is always empty. Callers must pass a
// valid non-nil context.
func LoadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, string, error) {
	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, sourceFlag, cookieDomain)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: %w", err)
	}
	return loaded, "", nil
}

// loadSessionCookies returns the stored ASC cookies. A tracker with no stored
// cookies yields none rather than an error, so callers report it as missing
// credentials; storage or decryption failures are returned as errors.
func loadSessionCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	cookies, _, err := LoadCookies(ctx, dbPath)
	if errors.Is(err, cookiepkg.ErrTrackerCookiesNotFound) {
		return nil, nil
	}
	return cookies, err
}

// newSessionClient copies base with a cookie jar seeded from stored cookies so
// Laravel's rotating session and XSRF cookies follow redirects and later requests.
func newSessionClient(base *http.Client, cookies []*http.Cookie) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: ASC cookie jar: %w", err)
	}
	jar.SetCookies(siteURL, cookies)
	client := httpclient.CloneWithTimeout(base, httpclient.UploadTimeout)
	client.Jar = jar
	return client, nil
}

// withoutRedirects copies a session client so a write's own redirect response
// is returned instead of followed; Go would otherwise replay POST/PATCH as GET.
func withoutRedirects(client *http.Client) *http.Client {
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

// xsrfToken returns the decoded Laravel XSRF cookie expected in X-XSRF-TOKEN.
func xsrfToken(client *http.Client) string {
	if client == nil || client.Jar == nil {
		return ""
	}
	for _, cookie := range client.Jar.Cookies(siteURL) {
		if cookie.Name != "XSRF-TOKEN" {
			continue
		}
		decoded, err := url.QueryUnescape(cookie.Value)
		if err != nil {
			return strings.TrimSpace(cookie.Value)
		}
		return strings.TrimSpace(decoded)
	}
	return ""
}

// isLoginRedirect reports whether a followed response landed on the login page.
func isLoginRedirect(resp *http.Response) bool {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	return strings.HasPrefix(resp.Request.URL.Path, "/login")
}

// warmUploadSession loads the upload page to validate the session and rotate
// the XSRF cookie before a write.
func warmUploadSession(ctx context.Context, client *http.Client) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+uploadPagePath, nil)
	if err != nil {
		return fmt.Errorf("trackers: ASC session request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("trackers: ASC session request: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	_ = resp.Body.Close()
	if isLoginRedirect(resp) {
		return fmt.Errorf("trackers: %w", errSessionExpired)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("trackers: ASC upload page returned status %d", resp.StatusCode)
	}
	if xsrfToken(client) == "" {
		return errors.New("trackers: ASC session did not provide an XSRF token")
	}
	return nil
}

// setXHRHeaders marks a write as an XHR so Laravel answers validation
// failures with a 422 JSON body instead of a redirect.
func setXHRHeaders(httpReq *http.Request, client *http.Client) {
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	httpReq.Header.Set("X-Xsrf-Token", xsrfToken(client))
	httpReq.Header.Set("Origin", baseURL)
	httpReq.Header.Set("Referer", baseURL+uploadPagePath)
}

// resolveAuthSession validates the imported ASC cookies by loading the upload
// page. It never attempts a login: ASC auth is cookie import only.
func resolveAuthSession(ctx context.Context, _ config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	cookies, err := loadSessionCookies(ctx, dbPath)
	if err != nil {
		return err
	}
	if len(cookies) == 0 {
		return &trackers.AuthResolutionError{
			Reason:       "cookies missing",
			PublicDetail: "Import ASC cookies for amigos-share.club.",
			AuthRequired: true,
			Err:          cookiepkg.ErrTrackerCookiesNotFound,
		}
	}
	client, err := newSessionClient(httpclient.New(httpclient.DefaultTimeout), cookies)
	if err != nil {
		return err
	}
	return validateSession(ctx, client)
}

// validateSession classifies a session check. An expired session needs fresh
// cookies but is not reported as confirmed-invalid, so stored cookies are
// never deleted on a single login redirect.
func validateSession(ctx context.Context, client *http.Client) error {
	err := warmUploadSession(ctx, client)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errSessionExpired):
		return &trackers.AuthResolutionError{
			Reason:       "session expired",
			PublicDetail: "ASC session expired; import fresh cookies for amigos-share.club.",
			AuthRequired: true,
			Err:          err,
		}
	default:
		return &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       err,
		}
	}
}
