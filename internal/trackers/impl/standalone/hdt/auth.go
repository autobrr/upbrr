// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"

	"regexp"
)

func loadCookies(ctx context.Context, dbPath string, baseURL string) ([]*http.Cookie, error) {
	host := "hd-torrents.me"
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDT", host)
	if err != nil {
		return values, fmt.Errorf("trackers: HDT load cookies: %w", err)
	}
	return values, nil
}

func fetchToken(ctx context.Context, baseURL string, cookies []*http.Cookie) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/upload.php", nil)
	if err != nil {
		return "", fmt.Errorf("trackers: HDT token request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: HDT token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	match := tokenPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", errors.New("trackers: HDT csrf token not found")
	}
	return strings.TrimSpace(match[1]), nil
}

var tokenPattern = regexp.MustCompile(`name="csrfToken"\s+value="([^"]+)"`)
