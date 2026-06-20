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
		return finalize(legacy.ImportFromFile(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("import config: read file: %w", err)
	}

	cfg, err := parseNative(filepath.Base(path), data)
	return finalize(cfg, nil, err)
}

// ImportFromContent parses raw file content. The filename is used only to
// decide which parser to invoke; its directory (if any) is ignored.
func ImportFromContent(filename string, data []byte) (*config.Config, []string, error) {
	if len(data) > MaxFileBytes {
		return nil, nil, fmt.Errorf("import config: file is too large (%d bytes, limit %d)", len(data), MaxFileBytes)
	}

	if isPythonFile(filename) {
		return finalize(legacy.ImportFromContent(data))
	}

	cfg, err := parseNative(filename, data)
	return finalize(cfg, nil, err)
}

// finalize applies sanitization that should happen on every import regardless
// of source format: disabling image rehosts for trackers that do not support
// them. Disabled trackers are appended to the warning list so users see why
// their setting changed.
func finalize(cfg *config.Config, warnings []string, err error) (*config.Config, []string, error) {
	if err != nil {
		return nil, nil, err
	}
	if disabled := config.DisableUnsupportedTrackerImageRehosts(cfg); len(disabled) > 0 {
		for _, name := range disabled {
			warnings = append(warnings, "disabled unsupported img_rehost for tracker: "+name)
		}
	}
	return cfg, warnings, nil
}

// parseNative handles .yaml/.yml/.json exports by overlaying the user's data
// onto the embedded default config. This mirrors the legacy conversion flow
// and guarantees that fields absent from the import keep sensible defaults,
// including any new settings added since the file was written. Template torrent
// clients are stripped, but an omitted or null torrent_clients section is
// normalized to an empty map so exports and UI consumers see {} instead of null.
func parseNative(filename string, data []byte) (*config.Config, error) {
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("import config: load defaults: %w", err)
	}
	// Template clients are examples, not imported user configuration.
	cfg.TorrentClients = make(map[string]config.TorrentClientConfig)

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".yaml", ".yml":
		cfg, err = parseNativeYAML(data, cfg)
	case ".json":
		// The export path (ExportToJSON) uses json.MarshalIndent, which
		// uses Go field names because the config structs have no json
		// tags. We must use json.Unmarshal here so the round-trip is
		// symmetric — yaml.Unmarshal would look for yaml tag names
		// (e.g. "tmdb_api") which do not match the exported keys
		// (e.g. "TMDBAPI").
		cfg, err = parseNativeJSON(data, cfg)
	default:
		return nil, fmt.Errorf("import config: unsupported file extension %q (supported: .py, .yaml, .yml, .json)", ext)
	}
	if err != nil {
		return nil, err
	}
	if cfg.TorrentClients == nil {
		cfg.TorrentClients = make(map[string]config.TorrentClientConfig)
	}

	if err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return nil, fmt.Errorf("import config: merge tracker defaults: %w", err)
	}

	return cfg, nil
}

func parseNativeYAML(data []byte, defaults *config.Config) (*config.Config, error) {
	defaultRaw := map[string]any{}
	defaultData, err := yaml.Marshal(defaults)
	if err != nil {
		return nil, fmt.Errorf("import config: marshal yaml defaults: %w", err)
	}
	if err := yaml.Unmarshal(defaultData, &defaultRaw); err != nil {
		return nil, fmt.Errorf("import config: unmarshal yaml defaults: %w", err)
	}
	defaultRaw["torrent_clients"] = map[string]any{}

	overlay := map[string]any{}
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("import config: unmarshal yaml: %w", err)
	}
	mergeConfigMap(defaultRaw, overlay)

	merged, err := yaml.Marshal(defaultRaw)
	if err != nil {
		return nil, fmt.Errorf("import config: marshal merged yaml: %w", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(merged, &cfg); err != nil {
		return nil, fmt.Errorf("import config: unmarshal merged yaml: %w", err)
	}
	decrypted, err := config.DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("import config: decrypt secrets: %w", err)
	}
	return decrypted, nil
}

func parseNativeJSON(data []byte, defaults *config.Config) (*config.Config, error) {
	defaultRaw := map[string]any{}
	defaultData, err := json.Marshal(defaults)
	if err != nil {
		return nil, fmt.Errorf("import config: marshal json defaults: %w", err)
	}
	if err := json.Unmarshal(defaultData, &defaultRaw); err != nil {
		return nil, fmt.Errorf("import config: unmarshal json defaults: %w", err)
	}
	defaultRaw["TorrentClients"] = map[string]any{}

	overlay := map[string]any{}
	if err := json.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("import config: unmarshal json: %w", err)
	}
	mergeConfigMap(defaultRaw, overlay)

	merged, err := json.Marshal(defaultRaw)
	if err != nil {
		return nil, fmt.Errorf("import config: marshal merged json: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(merged, &cfg); err != nil {
		return nil, fmt.Errorf("import config: unmarshal merged json: %w", err)
	}
	decrypted, err := config.DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("import config: decrypt secrets: %w", err)
	}
	return decrypted, nil
}

func mergeConfigMap(base map[string]any, overlay map[string]any) {
	for key, overlayValue := range overlay {
		overlayMap, overlayOK := overlayValue.(map[string]any)
		baseMap, baseOK := base[key].(map[string]any)
		if overlayOK && baseOK {
			mergeConfigMap(baseMap, overlayMap)
			continue
		}
		base[key] = overlayValue
	}
}

func isPythonFile(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".py"
}
