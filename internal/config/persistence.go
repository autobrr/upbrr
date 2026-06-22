// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

type exportFormat int

const (
	exportFormatYAML exportFormat = iota
	exportFormatJSON
)

// ExportToYAML writes the config to a YAML file.
func ExportToYAML(cfg *Config, path string) error {
	return exportToFile(cfg, path, exportFormatYAML, true)
}

// ExportToPlaintextYAML writes the config to a YAML file without encrypting secret fields.
func ExportToPlaintextYAML(cfg *Config, path string) error {
	return exportToFile(cfg, path, exportFormatYAML, false)
}

func exportToFile(cfg *Config, path string, format exportFormat, encryptSecrets bool) error {
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}
	if path == "" {
		return errors.New("config export: empty path")
	}

	// Ensure directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("config export: mkdir: %w", err)
	}

	exportCfg, err := exportableConfig(cfg, encryptSecrets)
	if err != nil {
		return err
	}

	var data []byte
	switch format {
	case exportFormatYAML:
		data, err = yaml.Marshal(exportCfg)
		if err != nil {
			return fmt.Errorf("config export: marshal yaml: %w", err)
		}
		// TODO: exportFormatJSON is currently unused by public callers (they route through exportToJSON);
		// keep this branch so file-based JSON export can be re-enabled without duplicating marshal logic.
	case exportFormatJSON:
		data, err = json.MarshalIndent(exportCfg, "", "  ")
		if err != nil {
			return fmt.Errorf("config export: marshal json: %w", err)
		}
	default:
		return errors.New("config export: unknown format")
	}

	// Write to file.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config export: write file: %w", err)
	}

	return nil
}

// ImportFromYAML reads the config from a YAML file.
func ImportFromYAML(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("config import: empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, internalerrors.ErrNotFound
		}
		return nil, fmt.Errorf("config import: read file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config import: unmarshal yaml: %w", err)
	}

	decryptedCfg, err := DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("config import: decrypt secrets: %w", err)
	}

	return decryptedCfg, nil
}

// ExportToJSON serializes the config to a JSON string.
func ExportToJSON(cfg *Config) (string, error) {
	return exportToJSON(cfg, true)
}

// ExportToPlaintextJSON serializes the config to JSON without encrypting secret fields.
func ExportToPlaintextJSON(cfg *Config) (string, error) {
	return exportToJSON(cfg, false)
}

func exportToJSON(cfg *Config, encryptSecrets bool) (string, error) {
	if cfg == nil {
		return "", internalerrors.ErrInvalidInput
	}

	exportCfg, err := exportableConfig(cfg, encryptSecrets)
	if err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(exportCfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("config export: marshal json: %w", err)
	}

	return string(data), nil
}

// ImportFromJSON deserializes plaintext JSON config (for example,
// ExportToPlaintextJSON output) without attempting secret decryption.
func ImportFromJSON(payload string) (*Config, error) {
	return importFromJSON(payload, false)
}

// ImportFromJSONEncrypted deserializes JSON config that contains encrypted
// secret envelopes (for example, ExportToJSON output) and decrypts secrets.
func ImportFromJSONEncrypted(payload string) (*Config, error) {
	return importFromJSON(payload, true)
}

func importFromJSON(payload string, decryptSecrets bool) (*Config, error) {
	if payload == "" {
		return nil, errors.New("config import: empty json")
	}

	var cfg Config
	if err := json.Unmarshal([]byte(payload), &cfg); err != nil {
		return nil, fmt.Errorf("config import: unmarshal json: %w", err)
	}
	if !decryptSecrets {
		return &cfg, nil
	}

	decryptedCfg, err := DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("config import: decrypt secrets: %w", err)
	}

	return decryptedCfg, nil
}

// BackupToYAML creates a timestamped YAML backup of the current config.
// Returns the path to the backup file.
func BackupToYAML(cfg *Config, baseDir string) (string, error) {
	if cfg == nil {
		return "", internalerrors.ErrInvalidInput
	}
	if baseDir == "" {
		return "", errors.New("config backup: empty base directory")
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("config backup: mkdir: %w", err)
	}

	// Create timestamped filename.
	backupDir := filepath.Join(baseDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("config backup: mkdir backups: %w", err)
	}

	backupPath := filepath.Join(backupDir, "config.yaml")
	if err := ExportToYAML(cfg, backupPath); err != nil {
		return "", fmt.Errorf("config backup: export: %w", err)
	}

	return backupPath, nil
}

// fullConfigLoader reconstructs persisted config data into either the Config
// struct or a raw section map. Production repositories support both forms.
type fullConfigLoader interface {
	LoadFullConfig(ctx context.Context, dest any) error
}

// LoadFromDatabase loads the full config from the repository, overlaying saved
// sections onto embedded defaults so older persisted configs pick up newly
// added options while preserving explicit zero values and decrypted secrets.
func LoadFromDatabase(ctx context.Context, repo fullConfigLoader) (*Config, error) {
	cfg, _, err := LoadFromDatabaseWithDefaultBackfill(ctx, repo)
	return cfg, err
}

// LoadFromDatabaseWithDefaultBackfill returns a loaded config plus a changed
// flag indicating whether embedded defaults filled fields missing from storage.
// The flag is based on raw stored JSON key presence before Config unmarshaling,
// so explicit false, zero, and empty values do not look like missing fields.
func LoadFromDatabaseWithDefaultBackfill(ctx context.Context, repo fullConfigLoader) (*Config, bool, error) {
	if repo == nil {
		return nil, false, errors.New("config load: nil repository")
	}

	cfg, backfilledDefaults, err := loadFullConfigOverlayingDefaults(ctx, repo)
	if err != nil {
		return nil, false, err
	}

	decryptedCfg, err := DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, false, fmt.Errorf("config load from database: decrypt secrets: %w", err)
	}

	return decryptedCfg, backfilledDefaults, nil
}

// loadFullConfigOverlayingDefaults overlays raw stored JSON sections onto the
// embedded default config and reports whether any default keys were absent from
// storage. Raw overlay is required because unmarshaled structs cannot
// distinguish omitted fields from explicit false, zero, or empty values.
func loadFullConfigOverlayingDefaults(ctx context.Context, repo fullConfigLoader) (Config, bool, error) {
	defaults, err := LoadEmbeddedDefaultConfig()
	if err != nil {
		return Config{}, false, fmt.Errorf("config load from database: load defaults: %w", err)
	}
	// Template clients are examples, not persisted user configuration.
	defaults.TorrentClients = map[string]TorrentClientConfig{}

	base, err := configJSONMap(defaults)
	if err != nil {
		return Config{}, false, fmt.Errorf("config load from database: marshal defaults: %w", err)
	}

	stored := map[string]any{}
	if err := repo.LoadFullConfig(ctx, &stored); err != nil {
		cfg := *defaults
		if err := repo.LoadFullConfig(ctx, &cfg); err != nil {
			return Config{}, false, fmt.Errorf("config load from database: %w", err)
		}
		return cfg, false, nil
	}
	missingDefaultPaths := mergeStoredConfigMap(base, stored, "")

	merged, err := json.Marshal(base)
	if err != nil {
		return Config{}, false, fmt.Errorf("config load from database: marshal merged defaults: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(merged, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("config load from database: unmarshal merged defaults: %w", err)
	}
	return cfg, len(missingDefaultPaths) > 0, nil
}

// configJSONMap converts cfg to the same exported-field JSON shape used by DB
// section storage and native JSON exports.
func configJSONMap(cfg *Config) (map[string]any, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config json map: %w", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("unmarshal config json map: %w", err)
	}
	return out, nil
}

// mergeStoredConfigMap recursively overlays stored config onto defaults and
// returns default-key paths absent from the stored JSON shape. Raw presence is
// tracked before Config unmarshaling so explicit false, zero, and empty values
// are not confused with omitted fields. Overlay keys are processed in sorted
// order so duplicate tracker case variants fold into canonical entries
// deterministically.
func mergeStoredConfigMap(base map[string]any, overlay map[string]any, path string) []string {
	var missingDefaultPaths []string
	for key := range base {
		if _, exists := overlay[key]; !exists {
			missingDefaultPaths = append(missingDefaultPaths, configMapPath(path, key))
		}
	}

	overlayKeys := make([]string, 0, len(overlay))
	for key := range overlay {
		overlayKeys = append(overlayKeys, key)
	}
	sort.Strings(overlayKeys)

	for _, key := range overlayKeys {
		overlayValue := overlay[key]
		baseValue, exists := base[key]
		if !exists {
			if allowsStoredDynamicConfigEntry(path) || isStoredTrackerEntryPath(path) {
				missingDefaultPaths = append(missingDefaultPaths, mergeStoredDynamicConfigValue(base, key, overlayValue, path)...)
			}
			continue
		}

		baseMap, baseOK := baseValue.(map[string]any)
		overlayMap, overlayOK := overlayValue.(map[string]any)
		if baseOK {
			if !overlayOK {
				continue
			}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			missingDefaultPaths = append(missingDefaultPaths, mergeStoredConfigMap(baseMap, overlayMap, childPath)...)
			continue
		}
		base[key] = overlayValue
	}
	return missingDefaultPaths
}

// mergeStoredDynamicConfigValue inserts a stored dynamic entry or folds it into
// an existing tracker entry that differs only by case.
func mergeStoredDynamicConfigValue(base map[string]any, key string, overlayValue any, path string) []string {
	if usesCaseInsensitiveStoredTrackerKeys(path) {
		if existingKey, ok := caseInsensitiveConfigMapKey(base, key); ok {
			baseMap, baseOK := base[existingKey].(map[string]any)
			overlayMap, overlayOK := overlayValue.(map[string]any)
			if baseOK && overlayOK {
				return mergeStoredConfigMap(baseMap, overlayMap, configMapPath(path, existingKey))
			}
			base[existingKey] = overlayValue
			return nil
		}
	}

	base[key] = overlayValue
	return nil
}

// caseInsensitiveConfigMapKey returns the first map key equal to key under
// Unicode case folding. Callers use it only where config keys are already
// constrained by tracker schema paths.
func caseInsensitiveConfigMapKey(values map[string]any, key string) (string, bool) {
	for existingKey := range values {
		if strings.EqualFold(existingKey, key) {
			return existingKey, true
		}
	}
	return "", false
}

// usesCaseInsensitiveStoredTrackerKeys reports whether a stored JSON path is a
// tracker map or tracker field map where casing drift should not create
// duplicate entries.
func usesCaseInsensitiveStoredTrackerKeys(path string) bool {
	return path == "Trackers.Trackers" || isStoredTrackerEntryPath(path)
}

// configMapPath appends key to a dotted raw-config path.
func configMapPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func allowsStoredDynamicConfigEntry(path string) bool {
	return path == "Trackers.Trackers" || path == "TorrentClients"
}

func isStoredTrackerEntryPath(path string) bool {
	return strings.HasPrefix(path, "Trackers.Trackers.")
}

// SaveToDatabase persists the config to the repository.
func SaveToDatabase(ctx context.Context, cfg *Config, repo interface {
	SaveFullConfig(ctx context.Context, cfg any) error
}) error {
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config save: nil repository")
	}

	encryptedCfg, err := EncryptConfigSecrets(cfg)
	if err != nil {
		return fmt.Errorf("config save to database: encrypt secrets: %w", err)
	}

	if err := repo.SaveFullConfig(ctx, encryptedCfg); err != nil {
		return fmt.Errorf("config save to database: %w", err)
	}

	return nil
}

// SaveSectionToDatabase persists a single config section to the repository.
func SaveSectionToDatabase(ctx context.Context, section string, data any, repo interface {
	SaveConfigSection(ctx context.Context, section string, data any) error
}) error {
	if section == "" {
		return errors.New("config save section: empty section name")
	}
	if data == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config save section: nil repository")
	}

	if err := repo.SaveConfigSection(ctx, section, data); err != nil {
		return fmt.Errorf("config save section %s to database: %w", section, err)
	}

	return nil
}

// LoadSectionFromDatabase retrieves a single config section from the repository.
func LoadSectionFromDatabase(ctx context.Context, section string, dest any, repo interface {
	LoadConfigSection(ctx context.Context, section string, dest any) error
}) error {
	if section == "" {
		return errors.New("config load section: empty section name")
	}
	if dest == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config load section: nil repository")
	}

	if err := repo.LoadConfigSection(ctx, section, dest); err != nil {
		return fmt.Errorf("config load section %s from database: %w", section, err)
	}

	return nil
}

// ExportFromDatabaseToYAML loads config from database, applies environment overrides,
// and writes the resulting config to a YAML file.
func ExportFromDatabaseToYAML(ctx context.Context, outputPath string, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}) error {
	return exportFromDatabaseToYAML(ctx, outputPath, repo, true)
}

// ExportFromDatabaseToPlaintextYAML loads config from database, applies environment overrides,
// and writes the resulting config to a YAML file without encrypting secret fields.
func ExportFromDatabaseToPlaintextYAML(ctx context.Context, outputPath string, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}) error {
	return exportFromDatabaseToYAML(ctx, outputPath, repo, false)
}

func exportFromDatabaseToYAML(ctx context.Context, outputPath string, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}, encryptSecrets bool) error {
	if strings.TrimSpace(outputPath) == "" {
		return errors.New("config export from database: empty output path")
	}

	cfg, err := LoadFromDatabase(ctx, repo)
	if err != nil {
		return fmt.Errorf("config export from database: load: %w", err)
	}

	ApplyEnvOverrides(cfg)
	var exportErr error
	if encryptSecrets {
		exportErr = ExportToYAML(cfg, outputPath)
	} else {
		exportErr = ExportToPlaintextYAML(cfg, outputPath)
	}
	if exportErr != nil {
		return fmt.Errorf("config export from database: %w", exportErr)
	}

	return nil
}

func exportableConfig(cfg *Config, encryptSecrets bool) (*Config, error) {
	if !encryptSecrets {
		return cloneConfig(cfg)
	}

	encryptedCfg, err := EncryptConfigSecrets(cfg)
	if err != nil {
		return nil, fmt.Errorf("config export: encrypt secrets: %w", err)
	}

	return encryptedCfg, nil
}
