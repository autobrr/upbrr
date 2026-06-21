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
	"reflect"
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
// decide which parser to invoke; its directory (if any) is ignored. Native
// YAML/JSON imports with encrypted secret envelopes are decrypted and marked so
// later database saves require helper-backed re-encryption.
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

// parseNativeYAML merges YAML input into defaults using YAML tag names, then
// decrypts imported encrypted secret fields and preserves the later persistence
// guard for helper-backed re-encryption.
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
	if err := mergeConfigMap(defaultRaw, overlay); err != nil {
		return nil, fmt.Errorf("import config: merge yaml: %w", err)
	}

	merged, err := yaml.Marshal(defaultRaw)
	if err != nil {
		return nil, fmt.Errorf("import config: marshal merged yaml: %w", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(merged, &cfg); err != nil {
		return nil, fmt.Errorf("import config: unmarshal merged yaml: %w", err)
	}
	decrypted, err := config.DecryptImportedConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("import config: decrypt secrets: %w", err)
	}
	return decrypted, nil
}

// parseNativeJSON merges JSON input into defaults using exported Go field
// names, matching the shape produced by config.ExportToJSON. Encrypted secret
// envelopes are decrypted with the same persistence guard used for YAML.
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
	if err := mergeConfigMap(defaultRaw, overlay); err != nil {
		return nil, fmt.Errorf("import config: merge json: %w", err)
	}

	merged, err := json.Marshal(defaultRaw)
	if err != nil {
		return nil, fmt.Errorf("import config: marshal merged json: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(merged, &cfg); err != nil {
		return nil, fmt.Errorf("import config: unmarshal merged json: %w", err)
	}
	decrypted, err := config.DecryptImportedConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("import config: decrypt secrets: %w", err)
	}
	return decrypted, nil
}

// mergeConfigMap recursively overlays user-supplied maps onto default maps.
// Unknown sections and fields are rejected except tracker extension fields, and
// dynamically named torrent clients are validated against the client schema.
// Null or empty object overlays leave default objects intact; scalar or array
// overlays cannot replace an object section.
func mergeConfigMap(base map[string]any, overlay map[string]any) error {
	return mergeConfigMapAt(base, overlay, "")
}

// mergeConfigMapAt applies one overlay level and tracks a dotted schema path
// so dynamic tracker and torrent-client entries can be validated by context.
func mergeConfigMapAt(base map[string]any, overlay map[string]any, path string) error {
	for key, overlayValue := range overlay {
		field := key
		if path != "" {
			field = path + "." + key
		}
		baseValue, exists := base[key]
		baseMap, baseOK := baseValue.(map[string]any)
		if baseOK {
			if overlayValue == nil {
				continue
			}
			overlayMap, overlayOK := overlayValue.(map[string]any)
			if !overlayOK {
				return fmt.Errorf("cannot replace config object %q with %T", field, overlayValue)
			}
			if err := mergeConfigMapAt(baseMap, overlayMap, field); err != nil {
				return err
			}
			continue
		}
		if !exists {
			switch {
			case isTrackerEntryPath(path):
				base[key] = overlayValue
				continue
			case isTrackerCollectionPath(path):
				entry, ok := overlayValue.(map[string]any)
				if overlayValue == nil {
					continue
				}
				if !ok {
					return fmt.Errorf("cannot replace config object %q with %T", field, overlayValue)
				}
				target := map[string]any{}
				base[key] = target
				if err := mergeConfigMapAt(target, entry, field); err != nil {
					return err
				}
				continue
			case isTorrentClientCollectionPath(path):
				entry, ok := overlayValue.(map[string]any)
				if overlayValue == nil {
					continue
				}
				if !ok {
					return fmt.Errorf("cannot replace config object %q with %T", field, overlayValue)
				}
				target := torrentClientSchemaMap(path)
				base[key] = target
				if err := mergeConfigMapAt(target, entry, field); err != nil {
					return err
				}
				continue
			case path == "":
				return fmt.Errorf("unknown config section %q", key)
			default:
				return fmt.Errorf("unknown config key %q", field)
			}
		}
		base[key] = overlayValue
	}
	return nil
}

// isTrackerCollectionPath reports whether path points at the map of dynamic
// tracker configs for YAML or native JSON imports.
func isTrackerCollectionPath(path string) bool {
	return path == "trackers" || path == "Trackers.Trackers"
}

// isTrackerEntryPath reports whether path points inside one dynamic tracker
// config, where tracker-specific extension fields are allowed.
func isTrackerEntryPath(path string) bool {
	return strings.HasPrefix(path, "trackers.") || strings.HasPrefix(path, "Trackers.Trackers.")
}

// isTorrentClientCollectionPath reports whether path points at the map of
// dynamic torrent-client configs for YAML or native JSON imports.
func isTorrentClientCollectionPath(path string) bool {
	return path == "torrent_clients" || path == "TorrentClients"
}

// torrentClientSchemaMap builds the allowed key set for a dynamic torrent
// client entry using YAML tag names or exported JSON field names.
func torrentClientSchemaMap(path string) map[string]any {
	t := reflect.TypeFor[config.TorrentClientConfig]()
	schema := make(map[string]any, t.NumField())
	for field := range t.Fields() {
		key := field.Name
		if path == "torrent_clients" {
			key = strings.TrimSpace(strings.Split(field.Tag.Get("yaml"), ",")[0])
		}
		if key == "" || key == "-" {
			continue
		}
		schema[key] = nil
	}
	return schema
}

func isPythonFile(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".py"
}
