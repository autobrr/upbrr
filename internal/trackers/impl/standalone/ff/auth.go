// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

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
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
)

const ffAuthResponseMaxBytes = 1 << 20

// validateAuthCookies checks bounded FF upload-page evidence. Explicit login
// evidence and a missing authenticated-page marker are confirmed-invalid;
// transport, read, and other HTTP failures remain transient.
func validateAuthCookies(ctx context.Context, baseURL string, values []*http.Cookie) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/upload.php", nil)
	if err != nil {
		return fmt.Errorf("trackers: FF session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range values {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && strings.TrimSpace(cookie.Value) != "" {
			req.AddCookie(cookie)
		}
	}
	resp, err := ffAuthHTTPClient().Do(req)
	if err != nil {
		return &trackerauth.ValidationError{
			TrackerID: "FF",
			Transient: true,
			Reason:    "remote validation unavailable",
			Err:       fmt.Errorf("trackers: FF session validation request: %w", err),
		}
	}
	defer resp.Body.Close()
	body, err := readFFAuthBody(resp)
	if err != nil {
		return &trackerauth.ValidationError{
			TrackerID: "FF",
			Transient: true,
			Reason:    "remote validation unavailable",
			Err:       err,
		}
	}
	lower := strings.ToLower(string(body))
	location := strings.ToLower(resp.Header.Get("Location"))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || strings.Contains(location, "login") ||
		strings.Contains(lower, "takelogin.php") ||
		strings.Contains(lower, "name=\"username\"") ||
		strings.Contains(lower, "name=\"password\"") {
		return &trackerauth.ValidationError{
			TrackerID:        "FF",
			ConfirmedInvalid: true,
			Reason:           "stored session expired",
			Err:              fmt.Errorf("trackers: FF session validation failed status=%d", resp.StatusCode),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &trackerauth.ValidationError{
			TrackerID: "FF",
			Transient: true,
			Reason:    "remote validation failed",
			Err:       fmt.Errorf("trackers: FF session validation failed status=%d", resp.StatusCode),
		}
	}
	if !strings.Contains(lower, "friends.php") {
		return &trackerauth.ValidationError{
			TrackerID:        "FF",
			ConfirmedInvalid: true,
			Reason:           "stored session expired",
			Err:              errors.New("trackers: FF login marker not found"),
		}
	}
	return nil
}

// loginAuthSession logs in with configured credentials and persists only
// non-empty cookies that pass the same remote session validation.
func loginAuthSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, baseURL string) error {
	data := url.Values{
		"returnto": {"/index.php"},
		"username": {strings.TrimSpace(cfg.Username)},
		"password": {strings.TrimSpace(cfg.Password)},
		"login":    {"Login"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/takelogin.php", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("trackers: FF login request build: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	resp, err := ffAuthHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("trackers: FF login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return &trackerauth.ValidationError{
			TrackerID:        "FF",
			ConfirmedInvalid: true,
			Reason:           "login failed",
			Err:              fmt.Errorf("trackers: FF login failed status=%d", resp.StatusCode),
		}
	}
	loginCookies := usableFFAuthCookies(resp.Cookies())
	if len(loginCookies) == 0 {
		return errors.New("trackers: FF login returned no usable cookies")
	}
	if err := validateAuthCookies(ctx, baseURL, loginCookies); err != nil {
		return fmt.Errorf("trackers: FF validate login cookies: %w", err)
	}
	if err := cookies.SaveTrackerHTTPCookies(ctx, dbPath, "FF", loginCookies); err != nil {
		return fmt.Errorf("trackers: FF persist login cookies: %w", err)
	}
	return nil
}

func ffAuthHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func readFFAuthBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, ffAuthResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("trackers: FF read auth response: %w", err)
	}
	if len(body) > ffAuthResponseMaxBytes {
		return nil, errors.New("trackers: FF auth response exceeds limit")
	}
	return body, nil
}

func usableFFAuthCookies(values []*http.Cookie) []*http.Cookie {
	usable := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && strings.TrimSpace(cookie.Value) != "" {
			usable = append(usable, cookie)
		}
	}
	return usable
}

func ffHasLoginCredentials(cfg config.TrackerConfig) bool {
	return strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
}
