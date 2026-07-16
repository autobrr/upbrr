// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const hdbAuthResponseMaxBytes = 1 << 20

func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          "HDB",
		DisplayName:        "HDB",
		AuthKind:           "passkey_cookies",
		SupportsCookieFile: true,
		RequiresPasskey:    true,
	}
}

// AuthPolicy records HDB's username/passkey and validated-cookie readiness contract.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	return &trackers.AuthPolicy{PasskeyRequiresUsername: true, PasskeyRequiresCookie: true}
}

func (d *Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return func(ctx context.Context, cfg config.TrackerConfig, dbPath string, request api.TrackerAuthLoginRequest) error {
		return resolveAuthSessionAt(ctx, cfg, dbPath, request, d.baseURL, d.httpClient)
	}
}

func resolveAuthSessionAt(
	ctx context.Context,
	cfg config.TrackerConfig,
	dbPath string,
	_ api.TrackerAuthLoginRequest,
	baseURL string,
	httpClient *http.Client,
) error {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Passkey) == "" {
		return &trackers.AuthResolutionError{
			Reason:       "username/passkey missing",
			AuthRequired: true,
			Err:          errors.New("trackers: HDB missing username/passkey"),
		}
	}
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDB", "hdbits.org")
	if err != nil || len(values) == 0 {
		if err == nil {
			err = cookies.ErrTrackerCookiesNotFound
		}
		return &trackers.AuthResolutionError{
			Reason:       "cookies missing",
			AuthRequired: true,
			Err:          fmt.Errorf("trackers: HDB cookies unavailable: %w", err),
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/upload/upload", nil)
	if err != nil {
		return fmt.Errorf("trackers: HDB session validation request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range values {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && strings.TrimSpace(cookie.Value) != "" {
			req.AddCookie(cookie)
		}
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDB session validation request: %w", err),
		}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, hdbAuthResponseMaxBytes+1))
	if err != nil {
		return &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDB read validation response: %w", err),
		}
	}
	if len(body) > hdbAuthResponseMaxBytes {
		return &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       errors.New("trackers: HDB validation response exceeds limit"),
		}
	}
	bodyText := strings.ToLower(string(body))
	location := strings.ToLower(resp.Header.Get("Location"))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || strings.Contains(location, "login") ||
		strings.Contains(bodyText, "name=\"username\"") ||
		strings.Contains(bodyText, "login.php") {
		return &trackers.AuthResolutionError{
			Reason:           "stored session expired",
			ConfirmedInvalid: true,
			Err:              fmt.Errorf("trackers: HDB session validation failed status=%d", resp.StatusCode),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &trackers.AuthResolutionError{
			Reason:    "remote validation failed",
			Transient: true,
			Err:       fmt.Errorf("trackers: HDB session validation failed status=%d", resp.StatusCode),
		}
	}
	if !strings.Contains(bodyText, "upload") && !strings.Contains(bodyText, "torrent") {
		return &trackers.AuthResolutionError{
			Reason:           "stored session expired",
			ConfirmedInvalid: true,
			Err:              errors.New("trackers: HDB upload marker not found"),
		}
	}
	return nil
}
