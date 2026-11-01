// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"net/http"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// ConnectionProbeInput contains only the selected draft tracker and a read-only
// snapshot of its stored cookies. It provides no database or persistence handle.
type ConnectionProbeInput struct {
	Config  config.TrackerConfig
	Cookies map[string]string
}

// ConnectionProbe binds a tracker-owned read-only Settings check. Test must use
// the supplied bounded client and return fixed, secret-free messages. It must
// not upload content, refresh credentials, or save session state.
type ConnectionProbe struct {
	Test func(context.Context, ConnectionProbeInput, *http.Client) api.ConnectionTestResult
	// DraftConfig copies only fields consumed by Test before secret decryption.
	// Nil selects no draft fields, which is appropriate for cookie-only checks.
	DraftConfig   func(config.TrackerConfig) config.TrackerConfig
	StoredCookies bool
	// UnverifiedReason explains a known contract gap when Test is nil. It must
	// not claim that a provider cannot support a safe check without evidence.
	UnverifiedReason string
	// CredentialsOverrideCookies selects a fresh, ephemeral credential login when
	// either draft username or password is entered. Such probes must validate both
	// fields and must never fall back to a stored session after login failure.
	CredentialsOverrideCookies bool
}

// ConnectionProbeProvider declares a tracker-owned draft connection check.
type ConnectionProbeProvider interface {
	ConnectionProbe() ConnectionProbe
}

// APIKeyProbeConfig isolates a probe that consumes only the generic API key.
func APIKeyProbeConfig(cfg config.TrackerConfig) config.TrackerConfig {
	return config.TrackerConfig{APIKey: cfg.APIKey}
}

// LoginProbeConfig isolates a probe's draft username and password from other
// authentication modes configured for the same tracker.
func LoginProbeConfig(cfg config.TrackerConfig) config.TrackerConfig {
	return config.TrackerConfig{Username: cfg.Username, Password: cfg.Password}
}
