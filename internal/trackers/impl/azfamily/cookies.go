// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
)

type simpleCookieJar struct {
	baseURL *url.URL
	cookies map[string]*http.Cookie
}

func (j simpleCookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	if j.cookies == nil {
		return
	}
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		j.cookies[cookie.Name] = cookie
	}
}

func (j simpleCookieJar) Cookies(_ *url.URL) []*http.Cookie {
	if j.cookies == nil {
		return nil
	}
	out := make([]*http.Cookie, 0, len(j.cookies))
	for _, cookie := range j.cookies {
		out = append(out, cookie)
	}
	return out
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
