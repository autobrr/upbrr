// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCLIConfigDefersBaseURLValidation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	configPath := filepath.Join(filepath.Dir(dbPath), cliConfigFileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"base_url":"javascript:alert(1)"}`), 0o600); err != nil {
		t.Fatalf("write web config: %v", err)
	}

	cfg, err := LoadCLIConfig(dbPath)
	if err != nil {
		t.Fatalf("LoadCLIConfig: %v", err)
	}
	if cfg.BaseURL != "javascript:alert(1)" {
		t.Fatalf("base url = %q, want raw persisted value", cfg.BaseURL)
	}
	if cfg.Host != "localhost" || cfg.Port != 7480 || cfg.SessionTTL != 1440 {
		t.Fatalf("default fields not normalized: %#v", cfg)
	}
}

func TestNormalizeCLIConfigStillRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	_, err := normalizeCLIConfig(CLIConfig{BaseURL: "javascript:alert(1)"})
	if err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("expected invalid base URL error, got %v", err)
	}
}
