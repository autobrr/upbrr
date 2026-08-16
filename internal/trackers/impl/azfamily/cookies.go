// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
)

// newSessionCookieJar uses standard domain scoping so tracker cookies do not
// accompany unrelated external image requests.
func newSessionCookieJar(rawBaseURL string, cookies []*http.Cookie) (http.CookieJar, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if strings.TrimSpace(baseURL.Hostname()) == "" {
		return nil, errors.New("invalid base URL: missing hostname")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	jar.SetCookies(baseURL, cookies)
	return jar, nil
}

func resolveCookies(ctx context.Context, dbPath string, site siteDefinition) ([]*http.Cookie, error) {
	baseURL, err := url.Parse(site.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("trackers: %s invalid base URL %q: %w", site.Name, site.BaseURL, err)
	}
	hostname := strings.TrimSpace(baseURL.Hostname())
	if hostname == "" {
		return nil, fmt.Errorf("trackers: %s invalid base URL %q: missing hostname", site.Name, site.BaseURL)
	}
	loaded, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, site.Name, hostname)
	if err != nil {
		return nil, fmt.Errorf("trackers: %s cookies unavailable: %w", site.Name, err)
	}
	return loaded, nil
}
