// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package configstore bridges the config package and the sqlite repository
// so CLI, GUI, and webserver share a single implementation of the
// open-database → migrate → load/save flow that used to be duplicated across
// cmd/upbrr/main.go and internal/guiapp/config.go.
package configstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
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
			loaded, err := config.ImportFromYAML(path)
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
// the full configuration, backfills any missing tracker defaults, disables
// unsupported tracker image rehosts (persisting the sanitized config back to
// the database when changes are made), and applies environment overrides.
//
// Callers decide whether to validate the returned config — the CLI fails fast
// while the web/GUI start with invalid config so users can fix it via the UI.
func LoadFromDBPath(ctx context.Context, dbPath string) (*config.Config, error) {
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
	if len(config.DisableUnsupportedTrackerImageRehosts(loaded)) > 0 {
		if err := config.SaveToDatabase(ctx, loaded, repo); err != nil {
			return nil, fmt.Errorf("config store: %w", err)
		}
	}
	config.ApplyEnvOverrides(loaded)
	return loaded, nil
}

// SaveToDBPath opens the sqlite database at dbPath, runs migrations, and
// persists the provided config.
func SaveToDBPath(ctx context.Context, cfg *config.Config, dbPath string) error {
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("config store: %w", err)
	}
	defer repo.Close()

	if err := repo.MigrateContext(ctx); err != nil {
		return fmt.Errorf("config store: %w", err)
	}
	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		return fmt.Errorf("config store: %w", err)
	}

	if err := cookies.SyncCookieEncryptionWithAuth(ctx, repo.RawDB(), dbPath); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			return nil
		}
		return fmt.Errorf("cookie encryption sync after config save: %w", err)
	}

	return nil
}

// Bootstrap resolves the effective config and database path at process
// startup. It prefers the sqlite database (with YAML used only as a bootstrap
// source when the database is empty). The persistYAML flag controls whether a
// YAML import is written back to the database: CLI/GUI persist so subsequent
// runs reuse the imported config, while the web server does not, because
// writing an incomplete YAML back would overwrite previously valid database
// state with zero-valued fields.
//
// Environment overrides are applied to the returned runtime config but not to
// the config written to the database. This keeps the persisted state free of
// env-specific values so unsetting an env var later does not leave a stale
// override sitting in the database.
func Bootstrap(ctx context.Context, configPath string, configProvided, persistYAML bool) (config.Config, string, error) {
	if configProvided {
		resolved, err := ResolveYAMLPath(configPath, configProvided)
		if err != nil {
			return config.Config{}, "", err
		}
		loaded, err := config.ImportFromYAML(resolved)
		if err != nil {
			return config.Config{}, "", fmt.Errorf("config store: %w", err)
		}

		dbPath, err := resolveDBPath(loaded)
		if err != nil {
			return config.Config{}, "", err
		}
		loaded.MainSettings.DBPath = dbPath

		if persistYAML {
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

	loaded, err := LoadFromDBPath(ctx, defaultDBPath)
	if err == nil {
		if strings.TrimSpace(loaded.MainSettings.DBPath) == "" || loaded.MainSettings.DBPath != defaultDBPath {
			loaded.MainSettings.DBPath = defaultDBPath
			if err := SaveToDBPath(ctx, loaded, defaultDBPath); err != nil {
				return config.Config{}, "", err
			}
		}
		return *loaded, defaultDBPath, nil
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
