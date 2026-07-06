// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
)

const cliConfigFileName = "web-config.json"

// CLIConfig stores the serve settings that can come from persisted web config,
// environment variables, or CLI flags.
type CLIConfig struct {
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	OpenBrowser    bool     `json:"open_browser"`
	TrustedProxies []string `json:"trusted_proxies"`
	BaseURL        string   `json:"base_url"`
	SessionTTL     int      `json:"session_ttl"`
}

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
	return cfg, nil
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
