// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/pkg/api"
)

const azAuthResponseMaxBytes = 1 << 20

// AuthCapability returns the stored-cookie authentication contract for this
// AZ-family profile.
func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return *authcontract.CookieCapability(d.Name())
}

// AuthSessionResolver validates imported cookies without attempting login.
func (d *Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return func(ctx context.Context, _ config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
		_, err := newSession(ctx, d.site, dbPath)
		if err == nil {
			return nil
		}
		var resolution *trackers.AuthResolutionError
		if errors.As(err, &resolution) {
			return err
		}
		if errors.Is(err, cookiepkg.ErrTrackerCookiesNotFound) {
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
}

// AuthPolicy returns the family-owned effective stored-cookie requirements.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	return &trackers.AuthPolicy{
		ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
			"form",
			false,
			[]trackers.AuthRequirement{trackers.AuthRequirementStoredCookie},
		)),
	}
}

func readAZAuthResponse(site siteDefinition, resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, azAuthResponseMaxBytes+1))
	if err != nil {
		return nil, &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: %s read session validation response: %w", site.Name, err),
		}
	}
	if len(body) > azAuthResponseMaxBytes {
		return nil, &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: %s session validation response exceeds limit", site.Name),
		}
	}
	return body, nil
}

func validateAZAuthResponse(site siteDefinition, resp *http.Response, body []byte) (string, error) {
	lower := strings.ToLower(string(body))
	if hasAZCloudflareChallenge(lower) {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation unavailable",
			Transient: true,
			Err:       fmt.Errorf("trackers: %s session validation challenged status=%d", site.Name, resp.StatusCode),
		}
	}
	if hasAZLoginEvidence(resp, lower) {
		return "", &trackers.AuthResolutionError{
			Reason:           "stored session expired",
			ConfirmedInvalid: true,
			Err:              fmt.Errorf("trackers: %s session validation rejected status=%d", site.Name, resp.StatusCode),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &trackers.AuthResolutionError{
			Reason:    "remote validation failed",
			Transient: true,
			Err:       fmt.Errorf("trackers: %s session validation failed status=%d", site.Name, resp.StatusCode),
		}
	}
	token := extractPatternGroup(azTokenPattern, string(body))
	if token == "" {
		return "", &trackers.AuthResolutionError{
			Reason:           "stored session expired",
			ConfirmedInvalid: true,
			Err:              fmt.Errorf("trackers: %s csrf token not found", site.Name),
		}
	}
	return token, nil
}

func hasAZCloudflareChallenge(lower string) bool {
	return strings.Contains(lower, "cf-chl-") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "<title>just a moment")
}

func hasAZLoginEvidence(resp *http.Response, lower string) bool {
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.Request != nil && resp.Request.URL != nil && strings.Contains(strings.ToLower(resp.Request.URL.Path), "/login") {
		return true
	}
	return strings.Contains(lower, `name="password"`) &&
		(strings.Contains(lower, `name="email"`) || strings.Contains(lower, `name="username"`))
}
