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

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
)

var errSessionExpired = errors.New("ASC session expired or cookies invalid")

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

func authProblem(ctx context.Context, dbPath string) string {
	cookies, _, err := LoadCookies(ctx, dbPath)
	if err == nil && len(cookies) > 0 {
		return ""
	}
	return "missing valid ASC cookies"
}

// newSessionClient wraps base with a cookie jar seeded from stored cookies so
// Laravel's rotating session and XSRF cookies follow redirects and later requests.
func newSessionClient(base *http.Client, cookies []*http.Cookie) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: ASC cookie jar: %w", err)
	}
	site, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("trackers: ASC base url: %w", err)
	}
	jar.SetCookies(site, cookies)
	client := &http.Client{Jar: jar}
	if base != nil {
		client.Transport = base.Transport
		client.Timeout = base.Timeout
	}
	return client, nil
}

// xsrfToken returns the decoded Laravel XSRF cookie expected in X-XSRF-TOKEN.
func xsrfToken(client *http.Client) string {
	if client == nil || client.Jar == nil {
		return ""
	}
	site, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	for _, cookie := range client.Jar.Cookies(site) {
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
