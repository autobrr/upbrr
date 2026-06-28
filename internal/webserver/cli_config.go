// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const cliConfigFileName = "web-config.json"

type CLIConfig struct {
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	OpenBrowser    bool     `json:"open_browser"`
	TrustedProxies []string `json:"trusted_proxies"`
	BaseURL        string   `json:"base_url"`
	SessionTTL     int      `json:"session_ttl"`
}

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
		return CLIConfig{}, fmt.Errorf("web config: parse: %w", err)
	}
	normalized, err := normalizeCLIConfig(cfg)
	if err != nil {
		return CLIConfig{}, fmt.Errorf("web config: %w", err)
	}
	return normalized, nil
}

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

func normalizeCLIConfig(cfg CLIConfig) (CLIConfig, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		cfg.Host = "localhost"
	}
	if cfg.Port <= 0 {
		cfg.Port = 7480
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 1440
	}
	baseURL, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return CLIConfig{}, err
	}
	cfg.BaseURL = baseURL
	if len(cfg.TrustedProxies) == 0 {
		cfg.TrustedProxies = nil
	}
	return cfg, nil
}
