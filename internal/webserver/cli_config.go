// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
)

const cliConfigFileName = "web-config.json"

// CLIConfig stores the serve settings that can come from persisted web config,
// environment variables, or CLI flags.
type CLIConfig struct {
	Host           string     `json:"host"`
	Port           int        `json:"port"`
	OpenBrowser    bool       `json:"open_browser"`
	TrustedProxies []string   `json:"trusted_proxies"`
	BaseURL        string     `json:"base_url"`
	SessionTTL     int        `json:"session_ttl"`
	OIDC           OIDCConfig `json:"oidc"`
}

// OIDCConfig stores the OpenID Connect settings for the embedded web UI.
// Field names mirror autobrr so operators configuring both projects meet the
// same vocabulary.
type OIDCConfig struct {
	Enabled bool `json:"enabled"`
	// Issuer is the OIDC issuer URL used for discovery, for example
	// https://auth.example.test/application/o/upbrr/.
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// RedirectURL must exactly match a redirect URI registered with the
	// provider and point at the callback route, for example
	// https://upbrr.example.test/api/auth/oidc/callback.
	RedirectURL string `json:"redirect_url"`
	// Scopes is a space-separated scope list. "openid" is always requested.
	Scopes string `json:"scopes"`
	// DisableBuiltInLogin removes the username/password form and rejects
	// password logins, leaving OIDC as the only way in.
	DisableBuiltInLogin bool `json:"disable_built_in_login"`
}

// DefaultOIDCScopes is requested when no scope list is configured.
const DefaultOIDCScopes = "openid profile email"

// DefaultCLIConfig returns the serve settings used when no persisted web config
// exists or persisted fields are omitted.
func DefaultCLIConfig() CLIConfig {
	return CLIConfig{
		Host:           "localhost",
		Port:           7480,
		OpenBrowser:    true,
		TrustedProxies: nil,
		BaseURL:        "",
		SessionTTL:     1440,
		OIDC:           OIDCConfig{Scopes: DefaultOIDCScopes},
	}
}

// LoadCLIConfig reads persisted serve settings from the directory containing
// dbPath. BaseURL is intentionally left unvalidated so env or CLI overrides can
// replace stale stored values before final validation.
func LoadCLIConfig(dbPath string) (CLIConfig, error) {
	cfg := DefaultCLIConfig()
	path := cliConfigPath(dbPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return CLIConfig{}, fmt.Errorf("web config: read: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return CLIConfig{}, fmt.Errorf("web config: parse: %s", redaction.RedactValue(err.Error(), nil))
	}
	return normalizeCLIConfigLoaded(cfg), nil
}

// SaveCLIConfig writes normalized serve settings next to dbPath.
func SaveCLIConfig(dbPath string, cfg CLIConfig) error {
	cfg, err := normalizeCLIConfig(cfg)
	if err != nil {
		return fmt.Errorf("web config: %w", err)
	}
	path := cliConfigPath(dbPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("web config: mkdir: %w", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("web config: encode: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("web config: write: %w", err)
	}
	return nil
}

func cliConfigPath(dbPath string) string {
	return filepath.Join(filepath.Dir(strings.TrimSpace(dbPath)), cliConfigFileName)
}

// normalizeCLIConfig applies defaults and validates fields used by the runtime
// or persisted config writer.
func normalizeCLIConfig(cfg CLIConfig) (CLIConfig, error) {
	cfg = normalizeCLIConfigLoaded(cfg)
	baseURL, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return CLIConfig{}, err
	}
	cfg.BaseURL = baseURL
	oidcCfg, err := normalizeOIDCConfig(cfg.OIDC)
	if err != nil {
		return CLIConfig{}, err
	}
	cfg.OIDC = oidcCfg
	return cfg, nil
}

// normalizeOIDCConfig trims fields, applies the default scope list, and
// validates the settings needed to complete an authorization code flow.
//
// Disabling the built-in login while OIDC is off is rejected rather than
// silently ignored: that combination would leave no usable way to sign in.
func normalizeOIDCConfig(cfg OIDCConfig) (OIDCConfig, error) {
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	cfg.Scopes = strings.TrimSpace(cfg.Scopes)

	if !cfg.Enabled {
		if cfg.DisableBuiltInLogin {
			return OIDCConfig{}, errors.New("web config: oidc disable_built_in_login requires oidc enabled")
		}
		return cfg, nil
	}

	if cfg.Scopes == "" {
		cfg.Scopes = DefaultOIDCScopes
	}

	var missing []string
	if cfg.Issuer == "" {
		missing = append(missing, "issuer")
	}
	if cfg.ClientID == "" {
		missing = append(missing, "client_id")
	}
	if cfg.ClientSecret == "" {
		missing = append(missing, "client_secret")
	}
	if cfg.RedirectURL == "" {
		missing = append(missing, "redirect_url")
	}
	if len(missing) > 0 {
		return OIDCConfig{}, fmt.Errorf("web config: oidc enabled but missing: %s", strings.Join(missing, ", "))
	}

	if err := validateOIDCAbsoluteURL("issuer", cfg.Issuer); err != nil {
		return OIDCConfig{}, err
	}
	if err := validateOIDCAbsoluteURL("redirect_url", cfg.RedirectURL); err != nil {
		return OIDCConfig{}, err
	}

	return cfg, nil
}

// validateOIDCAbsoluteURL requires an absolute http(s) URL. The redirect URL is
// sent to the provider and the issuer is fetched over the network, so a
// relative or scheme-less value can only fail later and less clearly.
func validateOIDCAbsoluteURL(field string, value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("web config: oidc %s: %s", field, redaction.RedactValue(err.Error(), nil))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("web config: oidc %s must be an absolute http(s) URL", field)
	}
	if parsed.Host == "" {
		return fmt.Errorf("web config: oidc %s must include a host", field)
	}
	return nil
}

// normalizeCLIConfigLoaded applies defaults that are safe before env and CLI
// precedence resolution. It deliberately does not validate BaseURL.
func normalizeCLIConfigLoaded(cfg CLIConfig) CLIConfig {
	if strings.TrimSpace(cfg.Host) == "" {
		cfg.Host = "localhost"
	}
	if cfg.Port <= 0 {
		cfg.Port = 7480
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 1440
	}
	if len(cfg.TrustedProxies) == 0 {
		cfg.TrustedProxies = nil
	}
	return cfg
}
