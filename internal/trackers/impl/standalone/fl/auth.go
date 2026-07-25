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
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/pkg/api"
)

const flAuthResponseMaxBytes = 1 << 20

var flAuthValidatorPattern = regexp.MustCompile(`name="validator"\s+value="([^"]+)"`)

// validateAuthCookies checks bounded FL home-page evidence. Explicit login
// evidence and a missing logout marker are confirmed-invalid; transport, read,
// and other HTTP failures remain transient.
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

// loginAuthSession fetches FL's validator token, submits configured
// credentials, and persists only non-empty cookies that pass remote validation.
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

const (
	baseURL      = "https://filelist.io"
	loginPageURL = baseURL + "/login.php"
	loginURL     = baseURL + "/takelogin.php"
	uploadURL    = baseURL + "/takeupload.php"
	downloadURL  = baseURL + "/download.php?id="
)

// resolveCookies returns usable stored FL cookies or performs credential login.
// Login-page read errors are reported before token parsing, and successful
// credential login requires durable cookie persistence.
func resolveCookies(ctx context.Context, logger api.Logger, cfg config.TrackerConfig, dbPath string, dryRun bool) ([]*http.Cookie, error) {
	loaded, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "FL", ".filelist.io")
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: LoadTrackerHTTPCookies failed for FL/.filelist.io, dbPath=%s: %v", dbPath, err)
		}
	} else if valid := validFLCookies(loaded); len(valid) > 0 {
		return valid, nil
	} else if logger != nil {
		logger.Debugf("trackers: FL loaded cookies were missing/expired, falling back to credential login, dbPath=%s", dbPath)
	}
	if dryRun {
		if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			return nil, errors.New("trackers: FL cookies not found")
		}
		// #nosec G124 -- Dry-run sentinel is an outbound tracker jar cookie, not a browser-set cookie.
		return []*http.Cookie{{
			Name:   "dryrun",
			Value:  "1",
			Domain: ".filelist.io",
			Path:   "/",
		}}, nil
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: FL cookie invalid/missing and username/password not configured")
	}
	if err := ensureLoginCookieStorageAvailable(dbPath); err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: FL create login cookie jar: %w", err)
	}
	client := newHTTPClient(httpclient.DefaultTimeout)
	client.Jar = jar
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: FL login page request build: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: FL login page request: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("trackers: FL read login page response: %w", err)
	}
	match := validatorPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return nil, errors.New("trackers: FL validator token not found")
	}
	data := url.Values{}
	data.Set("validator", match[1])
	data.Set("username", strings.TrimSpace(cfg.Username))
	data.Set("password", strings.TrimSpace(cfg.Password))
	data.Set("unlock", "1")
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: FL login request build: %w", err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return nil, fmt.Errorf("trackers: FL login request: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode < 200 || loginResp.StatusCode >= 400 {
		return nil, fmt.Errorf("trackers: FL login failed status=%d", loginResp.StatusCode)
	}
	base, _ := url.Parse(baseURL)
	loginCookies := client.Jar.Cookies(base)
	if err := persistLoginCookies(ctx, dbPath, loginCookies); err != nil {
		return nil, err
	}
	return loginCookies, nil
}

// persistLoginCookies saves FL login cookies and returns any persistence error
// so callers do not report login success without durable cookie storage.
func persistLoginCookies(ctx context.Context, dbPath string, values []*http.Cookie) error {
	valid := validFLCookies(values)
	if len(valid) == 0 {
		return errors.New("trackers: FL login returned no usable cookies")
	}
	if err := cookies.SaveTrackerHTTPCookies(ctx, dbPath, "FL", valid); err != nil {
		return fmt.Errorf("trackers: FL persist login cookies: %w", err)
	}
	return nil
}

func ensureLoginCookieStorageAvailable(dbPath string) error {
	if _, err := authmaterial.LoadFromDBPath(dbPath); err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return fmt.Errorf("trackers: FL encrypted cookie storage unavailable before credential login: %w", cookies.ErrAuthHelperUnavailable)
		}
		return fmt.Errorf("trackers: FL check encrypted cookie storage before credential login: %w", err)
	}
	return nil
}

func validFLCookies(values []*http.Cookie) []*http.Cookie {
	now := time.Now()
	valid := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie == nil {
			continue
		}
		if strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		if !cookie.Expires.IsZero() && cookie.Expires.Before(now) {
			continue
		}
		valid = append(valid, cookie)
	}
	return valid
}
