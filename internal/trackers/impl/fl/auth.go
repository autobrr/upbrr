// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

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
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

const flAuthResponseMaxBytes = 1 << 20

var flAuthValidatorPattern = regexp.MustCompile(`name="validator"\s+value="([^"]+)"`)

func (Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          "FL",
		DisplayName:        "FL",
		AuthKind:           "cookies_login",
		SupportsCookieFile: true,
		SupportsLogin:      true,
		SupportsAutoLogin:  true,
	}
}

func (Definition) AuthSessionResolver() trackers.AuthSessionResolver { return resolveAuthSession }

func resolveAuthSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	baseURL := "https://filelist.io"
	values, loadErr := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "FL", ".filelist.io")
	if loadErr == nil && len(values) > 0 {
		validationErr := validateAuthCookies(ctx, baseURL, values)
		if validationErr == nil {
			return nil
		}
		var validation *trackerauth.ValidationError
		if !errors.As(validationErr, &validation) || !validation.ConfirmedInvalid || validation.Transient || !flHasLoginCredentials(cfg) {
			return validationErr
		}
	}
	if !flHasLoginCredentials(cfg) {
		if loadErr == nil {
			loadErr = cookies.ErrTrackerCookiesNotFound
		}
		return &trackerauth.AuthRequiredError{
			TrackerID: "FL",
			Reason:    "cookies or username/password missing",
			Err:       fmt.Errorf("trackers: FL cookies unavailable: %w", loadErr),
		}
	}
	return loginAuthSession(ctx, cfg, dbPath, baseURL)
}

func validateAuthCookies(ctx context.Context, baseURL string, values []*http.Cookie) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/index.php", nil)
	if err != nil {
		return fmt.Errorf("trackers: FL session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range values {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && strings.TrimSpace(cookie.Value) != "" {
			req.AddCookie(cookie)
		}
	}
	resp, err := flAuthHTTPClient(nil).Do(req)
	if err != nil {
		return &trackerauth.ValidationError{
			TrackerID: "FL",
			Transient: true,
			Reason:    "remote validation unavailable",
			Err:       fmt.Errorf("trackers: FL session validation request: %w", err),
		}
	}
	defer resp.Body.Close()
	body, err := readFLAuthBody(resp)
	if err != nil {
		return &trackerauth.ValidationError{
			TrackerID: "FL",
			Transient: true,
			Reason:    "remote validation unavailable",
			Err:       err,
		}
	}
	lower := strings.ToLower(string(body))
	location := strings.ToLower(resp.Header.Get("Location"))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || strings.Contains(location, "login") ||
		strings.Contains(lower, "login.php") ||
		strings.Contains(lower, "name=\"username\"") ||
		strings.Contains(lower, "name=\"password\"") {
		return &trackerauth.ValidationError{
			TrackerID:        "FL",
			ConfirmedInvalid: true,
			Reason:           "stored session expired",
			Err:              fmt.Errorf("trackers: FL session validation failed status=%d", resp.StatusCode),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &trackerauth.ValidationError{
			TrackerID: "FL",
			Transient: true,
			Reason:    "remote validation failed",
			Err:       fmt.Errorf("trackers: FL session validation failed status=%d", resp.StatusCode),
		}
	}
	if !strings.Contains(lower, "logout") {
		return &trackerauth.ValidationError{
			TrackerID:        "FL",
			ConfirmedInvalid: true,
			Reason:           "stored session expired",
			Err:              errors.New("trackers: FL logout marker not found"),
		}
	}
	return nil
}

func loginAuthSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, baseURL string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("trackers: FL create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/login.php", nil)
	if err != nil {
		return fmt.Errorf("trackers: FL login page request build: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: FL login page request: %w", err)
	}
	body, readErr := readFLAuthBody(resp)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("trackers: FL read login page response: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("trackers: FL close login page response: %w", closeErr)
	}
	match := flAuthValidatorPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return errors.New("trackers: FL validator token not found")
	}
	data := url.Values{
		"validator": {match[1]},
		"username":  {strings.TrimSpace(cfg.Username)},
		"password":  {strings.TrimSpace(cfg.Password)},
		"unlock":    {"1"},
	}
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/takelogin.php", strings.NewReader(data.Encode()))
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
		return &trackerauth.ValidationError{
			TrackerID:        "FL",
			ConfirmedInvalid: true,
			Reason:           "login failed",
			Err:              fmt.Errorf("trackers: FL login failed status=%d", loginResp.StatusCode),
		}
	}
	base, err := url.Parse(baseURL + "/")
	if err != nil {
		return fmt.Errorf("trackers: FL parse base URL: %w", err)
	}
	loginCookies := jar.Cookies(base)
	if len(usableFLAuthCookies(loginCookies)) == 0 {
		return errors.New("trackers: FL login returned no usable cookies")
	}
	if err := validateAuthCookies(ctx, baseURL, loginCookies); err != nil {
		return fmt.Errorf("trackers: FL validate login cookies: %w", err)
	}
	if err := cookies.SaveTrackerHTTPCookies(ctx, dbPath, "FL", usableFLAuthCookies(loginCookies)); err != nil {
		return fmt.Errorf("trackers: FL persist login cookies: %w", err)
	}
	return nil
}

func flAuthHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func readFLAuthBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, flAuthResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("trackers: FL read auth response: %w", err)
	}
	if len(body) > flAuthResponseMaxBytes {
		return nil, errors.New("trackers: FL auth response exceeds limit")
	}
	return body, nil
}

func usableFLAuthCookies(values []*http.Cookie) []*http.Cookie {
	usable := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && strings.TrimSpace(cookie.Value) != "" {
			usable = append(usable, cookie)
		}
	}
	return usable
}

func flHasLoginCredentials(cfg config.TrackerConfig) bool {
	return strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
}
