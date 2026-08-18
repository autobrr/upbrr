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
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const hdtAuthResponseMaxBytes = 1 << 20

func loadCookies(ctx context.Context, dbPath string, baseURL string) ([]*http.Cookie, error) {
	host := "hd-torrents.me"
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
		host = parsed.Hostname()
	}
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDT", host)
	if err != nil {
		return values, fmt.Errorf("trackers: HDT load cookies: %w", err)
	}
	return values, nil
}

func resolveAuthSession(ctx context.Context, _ config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	return resolveAuthSessionAt(ctx, dbPath, resolveBaseURL(), nil)
}

func resolveAuthSessionAt(ctx context.Context, dbPath string, baseURL string, client *http.Client) error {
	values, err := loadCookies(ctx, dbPath, baseURL)
	if err != nil {
		if errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
			return &trackers.AuthResolutionError{
				Reason:       "cookies missing",
				AuthRequired: true,
				Err:          err,
			}
		}
		return &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       err,
		}
	}
	_, err = fetchTokenAt(ctx, baseURL, values, client)
	return err
}

func fetchToken(ctx context.Context, baseURL string, cookies []*http.Cookie) (string, error) {
	return fetchTokenAt(ctx, baseURL, cookies, nil)
}

func fetchTokenAt(ctx context.Context, baseURL string, cookies []*http.Cookie, client *http.Client) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/upload.php", nil)
	if err != nil {
		return "", fmt.Errorf("trackers: HDT token request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	if client == nil {
		client = httpclient.New(httpclient.DefaultTimeout)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDT token request: %w", err),
		}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, hdtAuthResponseMaxBytes+1))
	if err != nil {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDT read session validation response: %w", err),
		}
	}
	if len(body) > hdtAuthResponseMaxBytes {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       errors.New("trackers: HDT session validation response exceeds limit"),
		}
	}
	return validateHDTAuthResponse(resp, body)
}

func validateHDTAuthResponse(resp *http.Response, body []byte) (string, error) {
	lower := strings.ToLower(string(body))
	if hasHDTCloudflareChallenge(lower) {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDT session validation challenged status=%d", resp.StatusCode),
		}
	}
	if hasHDTLoginEvidence(resp, lower) {
		return "", &trackers.AuthResolutionError{
			Reason:           "stored session expired",
			ConfirmedInvalid: true,
			Err:              fmt.Errorf("trackers: HDT session validation rejected status=%d", resp.StatusCode),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation failed",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDT session validation failed status=%d", resp.StatusCode),
		}
	}
	match := tokenPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", &trackers.AuthResolutionError{
			Reason:           "stored session expired",
			ConfirmedInvalid: true,
			Err:              errors.New("trackers: HDT csrf token not found"),
		}
	}
	return strings.TrimSpace(match[1]), nil
}

func hasHDTCloudflareChallenge(lower string) bool {
	return strings.Contains(lower, "cf-chl-") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "<title>just a moment")
}

func hasHDTLoginEvidence(resp *http.Response, lower string) bool {
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.Request != nil && resp.Request.URL != nil && strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return true
	}
	return strings.Contains(lower, `name="password"`) &&
		(strings.Contains(lower, `name="username"`) || strings.Contains(lower, "login.php"))
}

var tokenPattern = regexp.MustCompile(`name="csrfToken"\s+value="([^"]+)"`)
