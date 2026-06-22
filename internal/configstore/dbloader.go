// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package configstore bridges the config package and the sqlite repository
// so CLI, GUI, and webserver share a single implementation of the
// open-database → migrate → load/save flow that used to be duplicated across
// cmd/upbrr/main.go and internal/guiapp/config.go.
package configstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/cookies"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
)

// DefaultConfigFileName is the default file name used when upbrr materializes
// a YAML config next to the database.
const DefaultConfigFileName = "config.yaml"

// DefaultYAMLPath returns the default location for a YAML config file,
// colocated with the default sqlite database.
func DefaultYAMLPath() (string, error) {
	dbPath, err := db.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("default db path: %w", err)
	}
	return filepath.Join(filepath.Dir(dbPath), DefaultConfigFileName), nil
}

// ResolveYAMLPath returns path when provided is true, falling back to
// DefaultYAMLPath otherwise. An empty provided path is an error.
func ResolveYAMLPath(path string, provided bool) (string, error) {
	if provided {
		if strings.TrimSpace(path) == "" {
			return "", errors.New("config path is required when --config is provided")
		}
		return path, nil
	}
	return DefaultYAMLPath()
}

// LoadFromPathOrEmbedded reads the YAML file at path if it exists, otherwise
// returns the embedded default config. An empty path skips the file lookup.
func LoadFromPathOrEmbedded(path string) (*config.Config, error) {
	if strings.TrimSpace(path) != "" {
		if _, err := os.Stat(path); err == nil {
			loaded, _, err := importer.ImportFromFile(path)
			if err != nil {
				return nil, fmt.Errorf("load config from yaml: %w", err)
			}
			return loaded, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("check config: %w", err)
		}
	}

	loaded, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("load embedded config: %w", err)
	}
	return loaded, nil
}

// LoadFromDBPath opens the sqlite database at dbPath, runs migrations, loads
// the full configuration, backfills missing embedded defaults, disables
// unsupported tracker image rehosts, persists any load-time fixes back to the
// database, and applies environment overrides.
//
// Callers decide whether to validate the returned config — the CLI fails fast
// while the web/GUI start with invalid config so users can fix it via the UI.
func LoadFromDBPath(ctx context.Context, dbPath string) (*config.Config, error) {
	return loadFromDBPath(ctx, dbPath, true)
}

// loadFromDBPath loads persisted config and optionally applies environment
// overrides to the returned runtime copy. Missing defaults and sanitized
// tracker settings are written before env overrides so persisted config remains
// environment-neutral.
func loadFromDBPath(ctx context.Context, dbPath string, applyEnv bool) (*config.Config, error) {
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	defer repo.Close()

	if err := repo.MigrateContext(ctx); err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}

	loaded, backfilledDefaults, err := config.LoadFromDatabaseWithDefaultBackfill(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	if err := config.MergeMissingTrackerDefaults(loaded); err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	sanitizedTrackers := len(config.DisableUnsupportedTrackerImageRehosts(loaded)) > 0
	if backfilledDefaults || sanitizedTrackers {
		if err := config.SaveToDatabase(ctx, loaded, repo); err != nil {
			return nil, fmt.Errorf("config store: %w", err)
		}
	}
	if applyEnv {
		config.ApplyEnvOverrides(loaded)
	}
	return loaded, nil
}

// SaveToDBPath opens the sqlite database at dbPath, runs migrations, validates
// any web auth material, syncs cookie encryption metadata when auth material is
// usable, and persists the provided config. Malformed auth material and other
// cookie sync failures are returned before config or cookie metadata is saved.
func SaveToDBPath(ctx context.Context, cfg *config.Config, dbPath string) error {
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("config store: %w", err)
	}
	defer repo.Close()

	if err := repo.MigrateContext(ctx); err != nil {
		return fmt.Errorf("config store: %w", err)
	}

	if err := SaveToRepository(ctx, cfg, repo, dbPath); err != nil {
		return fmt.Errorf("config store: %w", err)
	}

	return nil
}

// SaveToRepository persists cfg and cookie encryption auth metadata through one
// repository transaction.
func SaveToRepository(ctx context.Context, cfg *config.Config, repo *db.SQLiteRepository, dbPath string) error {
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}
	if repo == nil {
		return errors.New("config save: nil repository")
	}

	encryptedCfg, err := config.EncryptConfigSecrets(cfg)
	if err != nil {
		return fmt.Errorf("config save to database: encrypt secrets: %w", err)
	}

	if err := repo.SaveFullConfigWithPreSave(ctx, encryptedCfg, func(ctx context.Context, tx *sql.Tx) error {
		if err := syncCookieEncryptionBeforeConfigSaveTx(ctx, tx, dbPath); err != nil {
			return fmt.Errorf("cookie encryption sync before config save: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("config save to database: %w", err)
	}

	return nil
}

// syncCookieEncryptionBeforeConfigSaveTx updates cookie encryption metadata in
// the caller's config-save transaction, treating missing auth material as a
// plaintext-cookie install rather than a save failure.
func syncCookieEncryptionBeforeConfigSaveTx(ctx context.Context, tx *sql.Tx, dbPath string) error {
	if err := cookies.SyncCookieEncryptionWithAuthTx(ctx, tx, dbPath); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			return nil
		}
		return fmt.Errorf("sync cookie encryption with auth: %w", err)
	}
	return nil
}

// Bootstrap resolves the effective config and database path at process
// startup. It prefers the sqlite database (with YAML used only as a bootstrap
// source when the database is empty). The persistYAML flag controls whether a
// YAML import is written back to the database. Callers that accept incomplete
// setup config can pass false or omit validation; CLI callers validate before
// saving so bad --config input cannot overwrite valid database state.
//
// Environment overrides are applied to the returned runtime config but not to
// the config written to the database. This keeps the persisted state free of
// env-specific values so unsetting an env var later does not leave a stale
// override sitting in the database.
func Bootstrap(ctx context.Context, configPath string, configProvided, persistYAML bool) (config.Config, string, error) {
	return BootstrapWithValidator(ctx, configPath, configProvided, persistYAML, nil)
}

// BootstrapWithValidator is Bootstrap plus an optional validation hook for
// provided configs. The hook validates the exact config that would be persisted
// so environment-only fixes cannot write a config that fails after env removal.
// Without a hook, provided YAML/JSON is merged with stored DB config when
// present and persisted only if the persisted candidate validates.
func BootstrapWithValidator(ctx context.Context, configPath string, configProvided, persistYAML bool, validateBeforePersist func(*config.Config) error) (config.Config, string, error) {
	if configProvided {
		resolved, err := ResolveYAMLPath(configPath, configProvided)
		if err != nil {
			return config.Config{}, "", err
		}
		providedData, err := os.ReadFile(resolved)
		if err != nil {
			return config.Config{}, "", fmt.Errorf("config store: read provided config: %w", err)
		}
		loaded, _, err := importer.ImportFromContent(resolved, providedData)
		if err != nil {
			return config.Config{}, "", fmt.Errorf("config store: %w", err)
		}

		dbPath, err := resolveDBPath(loaded)
		if err != nil {
			return config.Config{}, "", err
		}
		loaded.MainSettings.DBPath = dbPath

		if persistYAML {
			if validateBeforePersist != nil {
				if err := validatePersistableConfig(loaded, dbPath, validateBeforePersist); err != nil {
					return config.Config{}, "", fmt.Errorf("config store: validate provided config: %w", err)
				}
			} else {
				persisted, runtime, shouldPersist, err := prepareProvidedConfigForSave(ctx, resolved, providedData, loaded, dbPath)
				if err != nil {
					return config.Config{}, "", err
				}
				loaded = runtime
				if !shouldPersist {
					runtime := *loaded
					config.ApplyEnvOverrides(&runtime)
					runtime.MainSettings.DBPath = dbPath
					return runtime, dbPath, nil
				}
				loaded = persisted
			}
			if err := SaveToDBPath(ctx, loaded, dbPath); err != nil {
				return config.Config{}, "", err
			}
		}

		runtime := *loaded
		config.ApplyEnvOverrides(&runtime)
		runtime.MainSettings.DBPath = dbPath
		return runtime, dbPath, nil
	}

	defaultDBPath, err := db.DefaultPath()
	if err != nil {
		return config.Config{}, "", fmt.Errorf("default db path: %w", err)
	}

	loaded, err := loadFromDBPath(ctx, defaultDBPath, false)
	if err == nil {
		if strings.TrimSpace(loaded.MainSettings.DBPath) == "" || loaded.MainSettings.DBPath != defaultDBPath {
			loaded.MainSettings.DBPath = defaultDBPath
			if err := SaveToDBPath(ctx, loaded, defaultDBPath); err != nil {
				return config.Config{}, "", err
			}
		}
		runtime := *loaded
		config.ApplyEnvOverrides(&runtime)
		runtime.MainSettings.DBPath = defaultDBPath
		return runtime, defaultDBPath, nil
	}
	if !errors.Is(err, internalerrors.ErrNotFound) {
		return config.Config{}, "", err
	}

	resolved, err := ResolveYAMLPath(configPath, configProvided)
	if err != nil {
		return config.Config{}, "", err
	}
	bootstrap, err := LoadFromPathOrEmbedded(resolved)
	if err != nil {
		return config.Config{}, "", err
	}

	fallbackDBPath, err := resolveDBPath(bootstrap)
	if err != nil {
		return config.Config{}, "", err
	}
	if strings.TrimSpace(fallbackDBPath) == "" {
		fallbackDBPath = defaultDBPath
	}

	if fallbackDBPath != defaultDBPath {
		fallbackCfg, err := LoadFromDBPath(ctx, fallbackDBPath)
		if err == nil {
			return *fallbackCfg, fallbackDBPath, nil
		}
		if !errors.Is(err, internalerrors.ErrNotFound) {
			return config.Config{}, "", err
		}
	}

	bootstrap.MainSettings.DBPath = fallbackDBPath
	if persistYAML {
		if err := SaveToDBPath(ctx, bootstrap, fallbackDBPath); err != nil {
			return config.Config{}, "", err
		}
	}

	runtime := *bootstrap
	config.ApplyEnvOverrides(&runtime)
	runtime.MainSettings.DBPath = fallbackDBPath
	return runtime, fallbackDBPath, nil
}

// validatePersistableConfig validates the exact config shape that would be
// written to the database, including the resolved DB path and excluding env-only
// runtime overrides.
func validatePersistableConfig(cfg *config.Config, dbPath string, validate func(*config.Config) error) error {
	candidate := *cfg
	candidate.MainSettings.DBPath = dbPath
	return validate(&candidate)
}

// prepareProvidedConfigForSave decides whether a provided config should replace
// or merge with stored DB config. It uses the already-read providedData for
// native YAML/JSON overlays so validation and persistence share one input.
func prepareProvidedConfigForSave(ctx context.Context, configPath string, providedData []byte, imported *config.Config, dbPath string) (*config.Config, *config.Config, bool, error) {
	stored, err := loadStoredConfigForProvidedMerge(ctx, dbPath)
	storedLoaded := err == nil
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return nil, nil, false, err
	}

	persisted := imported
	runtime := imported
	if storedLoaded {
		merged, mergeErr := mergeProvidedConfig(stored, configPath, providedData, imported)
		if mergeErr != nil {
			return nil, nil, false, mergeErr
		}
		persisted = merged
		runtime = merged
	}

	persisted.MainSettings.DBPath = dbPath
	if err := validatePersistableConfig(persisted, dbPath, func(cfg *config.Config) error {
		return cfg.Validate()
	}); err != nil {
		if storedLoaded {
			runtime = stored
		}
		return persisted, runtime, false, nil
	}

	return persisted, runtime, true, nil
}

// loadStoredConfigForProvidedMerge loads stored config for a provided-file merge
// without persisting sanitizer side effects. Unsupported image rehosts are
// disabled only in the returned copy so an invalid provided config cannot mutate
// the database during validation.
func loadStoredConfigForProvidedMerge(ctx context.Context, dbPath string) (*config.Config, error) {
	if _, err := os.Stat(dbPath); err != nil { //nolint:gosec // Existence probe for resolved DB path avoids creating SQLite DB before provided config validates.
		if errors.Is(err, os.ErrNotExist) {
			return nil, internalerrors.ErrNotFound
		}
		return nil, fmt.Errorf("config store: stat database: %w", err)
	}

	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	defer repo.Close()

	if err := repo.MigrateContext(ctx); err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}

	loaded, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	if err := config.MergeMissingTrackerDefaults(loaded); err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	config.DisableUnsupportedTrackerImageRehosts(loaded)
	return loaded, nil
}

// mergeProvidedConfig merges native YAML/JSON overlays into stored config and
// returns non-native imports unchanged.
func mergeProvidedConfig(base *config.Config, configPath string, providedData []byte, imported *config.Config) (*config.Config, error) {
	switch strings.ToLower(filepath.Ext(configPath)) {
	case ".yaml", ".yml":
		return mergeYAMLConfig(base, providedData)
	case ".json":
		return mergeJSONConfig(base, providedData)
	default:
		return imported, nil
	}
}

// mergeYAMLConfig overlays provided YAML bytes onto a stored config using YAML
// field names, then finalizes decrypted secrets and tracker defaults.
func mergeYAMLConfig(base *config.Config, overlayData []byte) (*config.Config, error) {
	baseRaw := map[string]any{}
	baseData, err := yaml.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("config store: marshal stored config: %w", err)
	}
	if err := yaml.Unmarshal(baseData, &baseRaw); err != nil {
		return nil, fmt.Errorf("config store: unmarshal stored config: %w", err)
	}

	overlayRaw := map[string]any{}
	if err := yaml.Unmarshal(overlayData, &overlayRaw); err != nil {
		return nil, fmt.Errorf("config store: unmarshal provided config: %w", err)
	}
	if err := mergeConfigMap(baseRaw, overlayRaw); err != nil {
		return nil, fmt.Errorf("config store: merge provided config: %w", err)
	}

	mergedData, err := yaml.Marshal(baseRaw)
	if err != nil {
		return nil, fmt.Errorf("config store: marshal merged config: %w", err)
	}
	var merged config.Config
	if err := yaml.Unmarshal(mergedData, &merged); err != nil {
		return nil, fmt.Errorf("config store: unmarshal merged config: %w", err)
	}
	return finalizeMergedConfig(&merged)
}

// mergeJSONConfig overlays provided JSON bytes onto a stored config using the
// exported field-name shape produced by native JSON export.
func mergeJSONConfig(base *config.Config, overlayData []byte) (*config.Config, error) {
	baseRaw := map[string]any{}
	baseData, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("config store: marshal stored config: %w", err)
	}
	if err := json.Unmarshal(baseData, &baseRaw); err != nil {
		return nil, fmt.Errorf("config store: unmarshal stored config: %w", err)
	}

	overlayRaw := map[string]any{}
	if err := json.Unmarshal(overlayData, &overlayRaw); err != nil {
		return nil, fmt.Errorf("config store: unmarshal provided config: %w", err)
	}
	if err := mergeConfigMap(baseRaw, overlayRaw); err != nil {
		return nil, fmt.Errorf("config store: merge provided config: %w", err)
	}

	mergedData, err := json.Marshal(baseRaw)
	if err != nil {
		return nil, fmt.Errorf("config store: marshal merged config: %w", err)
	}
	var merged config.Config
	if err := json.Unmarshal(mergedData, &merged); err != nil {
		return nil, fmt.Errorf("config store: unmarshal merged config: %w", err)
	}
	return finalizeMergedConfig(&merged)
}

// mergeConfigMap recursively overlays provided config onto stored config.
// Unknown sections and fields are rejected except tracker extension fields, and
// dynamically named torrent clients are validated against the client schema.
// Null or empty object overlays leave stored objects intact; scalar or array
// overlays cannot replace an object section.
func mergeConfigMap(base map[string]any, overlay map[string]any) error {
	return mergeConfigMapAt(base, overlay, "")
}

// mergeConfigMapAt applies one overlay level and tracks a dotted schema path so
// dynamic tracker and torrent-client entries can be validated by context.
func mergeConfigMapAt(base map[string]any, overlay map[string]any, path string) error {
	for key, overlayValue := range overlay {
		field := key
		if path != "" {
			field = path + "." + key
		}
		baseValue, baseExists := base[key]
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
		if !baseExists {
			if path == "" {
				return fmt.Errorf("unknown config section %q", key)
			}
			if allowsDynamicConfigEntry(path) {
				if err := mergeDynamicConfigEntry(base, path, key, overlayValue); err != nil {
					return err
				}
				continue
			}
			if allowsTrackerExtensionField(path) {
				base[key] = overlayValue
				continue
			}
			return fmt.Errorf("unknown config field %q", field)
		}
		base[key] = overlayValue
	}
	return nil
}

// allowsDynamicConfigEntry reports whether path accepts user-named child
// entries such as tracker names or torrent-client names.
func allowsDynamicConfigEntry(path string) bool {
	switch path {
	case "trackers", "Trackers.Trackers", "torrent_clients", "TorrentClients":
		return true
	default:
		return false
	}
}

// allowsTrackerExtensionField reports whether path is inside a dynamic tracker
// config, where tracker-specific extension fields are preserved.
func allowsTrackerExtensionField(path string) bool {
	return strings.HasPrefix(path, "trackers.") || strings.HasPrefix(path, "Trackers.Trackers.")
}

// configMapEntrySchema returns the schema for a dynamically named map entry
// when the entry requires fixed field validation.
func configMapEntrySchema(path string) (map[string]any, bool) {
	switch path {
	case "torrent_clients":
		return torrentClientSchemaMap(path), true
	case "TorrentClients":
		return torrentClientSchemaMap(path), true
	default:
		return nil, false
	}
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

// mergeDynamicConfigEntry merges a dynamically named tracker or torrent-client
// entry. Nil overlays are treated as absent so partial provided configs cannot
// clobber stored dynamic entries, while scalar overlays still fail schema checks.
func mergeDynamicConfigEntry(base map[string]any, path, key string, overlayValue any) error {
	field := path + "." + key
	if overlayValue == nil {
		return nil
	}
	overlayMap, overlayOK := overlayValue.(map[string]any)
	if !overlayOK {
		return fmt.Errorf("cannot replace config object %q with %T", field, overlayValue)
	}
	if len(overlayMap) == 0 {
		return nil
	}
	schema, hasSchema := configMapEntrySchema(path)
	if hasSchema {
		if err := mergeConfigMapAt(schema, overlayMap, field); err != nil {
			return err
		}
		base[key] = schema
		return nil
	}
	base[key] = overlayMap
	return nil
}

// finalizeMergedConfig restores imported-secret rewrap requirements and fills
// tracker defaults before a merged native config is validated or persisted.
func finalizeMergedConfig(cfg *config.Config) (*config.Config, error) {
	decrypted, err := config.DecryptImportedConfigSecrets(cfg)
	if err != nil {
		return nil, fmt.Errorf("config store: decrypt merged config: %w", err)
	}
	if err := config.MergeMissingTrackerDefaults(decrypted); err != nil {
		return nil, fmt.Errorf("config store: merge tracker defaults: %w", err)
	}
	config.DisableUnsupportedTrackerImageRehosts(decrypted)
	return decrypted, nil
}

// resolveDBPath returns the database path to use for cfg, honoring any env
// override without mutating cfg. Falls back to the user's default path when
// neither cfg nor env specify one.
func resolveDBPath(cfg *config.Config) (string, error) {
	probe := *cfg
	config.ApplyEnvOverrides(&probe)
	if dbPath := strings.TrimSpace(probe.MainSettings.DBPath); dbPath != "" {
		return dbPath, nil
	}
	defaultPath, err := db.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("default db path: %w", err)
	}
	return defaultPath, nil
}
