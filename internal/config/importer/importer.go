// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package importer dispatches configuration imports to the appropriate parser
// based on file extension. It handles legacy Upload Assistant Python files
// (.py) and native upbrr YAML/JSON exports, always producing a config that is
// backfilled with embedded defaults so users end up with up-to-date settings.
package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/legacy"
)

// MaxFileBytes caps the size of a config file accepted by the importer. It
// protects callers from accidentally reading huge files into memory. The
// value matches the web upload limit used by the webserver backend.
const MaxFileBytes = 2 * 1024 * 1024

// ImportFromFile reads the file at path and returns the parsed config along
// with any non-fatal warnings. The extension determines which parser is used.
func ImportFromFile(path string) (*config.Config, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("import config: stat file: %w", err)
	}
	if info.Size() > MaxFileBytes {
		return nil, nil, fmt.Errorf("import config: file is too large (%d bytes, limit %d)", info.Size(), MaxFileBytes)
	}

	if isPythonFile(path) {
		return legacy.ImportFromFile(path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("import config: read file: %w", err)
	}

	return parseNative(filepath.Base(path), data)
}

// ImportFromContent parses raw file content. The filename is used only to
// decide which parser to invoke; its directory (if any) is ignored.
func ImportFromContent(filename string, data []byte) (*config.Config, []string, error) {
	if len(data) > MaxFileBytes {
		return nil, nil, fmt.Errorf("import config: file is too large (%d bytes, limit %d)", len(data), MaxFileBytes)
	}

	if isPythonFile(filename) {
		return legacy.ImportFromContent(data)
	}

	return parseNative(filename, data)
}

// parseNative handles .yaml/.yml/.json exports by overlaying the user's data
// onto the embedded default config. This mirrors the legacy conversion flow
// and guarantees that fields absent from the import keep sensible defaults,
// including any new settings added since the file was written.
func parseNative(filename string, data []byte) (*config.Config, []string, error) {
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("import config: load defaults: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("import config: unmarshal yaml: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("import config: unmarshal json: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf("import config: unsupported file extension %q (supported: .py, .yaml, .yml, .json)", ext)
	}

	if err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return nil, nil, fmt.Errorf("import config: merge tracker defaults: %w", err)
	}

	return cfg, nil, nil
}

func isPythonFile(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".py"
}
