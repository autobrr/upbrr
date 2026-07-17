// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookieauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

// CookieLoginSpec describes one stored-cookie validation and credential-login lifecycle.
type CookieLoginSpec struct {
	TrackerID     string
	BaseURL       string
	CookieDomain  string
	Load          func(context.Context, string) ([]*http.Cookie, error)
	Validate      func(context.Context, string, []*http.Cookie) error
	HasCredential func(config.TrackerConfig) bool
	Login         func(context.Context, config.TrackerConfig, string, string) error
}

// CookieLoginResolver returns a resolver that reuses valid cookies, preserves
// transient validation failures, and logs in only after confirmed invalid or
// missing cookie state when credentials are available.
func CookieLoginResolver(spec CookieLoginSpec) trackers.AuthSessionResolver {
	trackerID := strings.ToUpper(strings.TrimSpace(spec.TrackerID))
	baseURL := strings.TrimRight(strings.TrimSpace(spec.BaseURL), "/")
	if spec.Load == nil {
		spec.Load = func(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
			return cookies.LoadTrackerHTTPCookies(ctx, dbPath, trackerID, spec.CookieDomain)
		}
	}
	return func(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
		return resolveCookieLogin(ctx, cfg, dbPath, trackerID, baseURL, spec)
	}
}

func resolveCookieLogin(
	ctx context.Context,
	cfg config.TrackerConfig,
	dbPath string,
	trackerID string,
	baseURL string,
	spec CookieLoginSpec,
) error {
	values, loadErr := spec.Load(ctx, dbPath)
	if loadErr == nil && len(values) > 0 {
		validationErr := spec.Validate(ctx, baseURL, values)
		if validationErr == nil {
			return nil
		}
		var validation *trackerauth.ValidationError
		if !errors.As(validationErr, &validation) || !validation.ConfirmedInvalid || validation.Transient || !spec.HasCredential(cfg) {
			return validationErr
		}
	}
	if !spec.HasCredential(cfg) {
		if loadErr == nil {
			loadErr = cookies.ErrTrackerCookiesNotFound
		}
		return &trackerauth.AuthRequiredError{
			TrackerID: trackerID,
			Reason:    "cookies or username/password missing",
			Err:       fmt.Errorf("trackers: %s cookies unavailable: %w", trackerID, loadErr),
		}
	}
	return spec.Login(ctx, cfg, dbPath, baseURL)
}
