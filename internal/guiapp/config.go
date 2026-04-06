// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
)

const (
	defaultConfigName = "config.yaml"
)

func loadConfig(configPath string, configProvided bool) (config.Config, string, error) {
	ctx := context.Background()
	if configProvided {
		resolved, err := resolveConfigPath(configPath, configProvided)
		if err != nil {
			return config.Config{}, "", err
		}

		loaded, err := config.ImportFromYAML(resolved)
		if err != nil {
			return config.Config{}, "", err
		}
		config.ApplyEnvOverrides(loaded)
		cfg := *loaded

		dbPath := strings.TrimSpace(cfg.MainSettings.DBPath)
		if dbPath == "" {
			dbPath, err = db.DefaultPath()
			if err != nil {
				return config.Config{}, "", fmt.Errorf("default db path: %w", err)
			}
			cfg.MainSettings.DBPath = dbPath
		}

		if err := saveConfigToDatabase(ctx, &cfg, dbPath); err != nil {
			return config.Config{}, "", err
		}
		return cfg, dbPath, nil
	}

	dbPath, err := db.DefaultPath()
	if err != nil {
		return config.Config{}, "", fmt.Errorf("default db path: %w", err)
	}

	cfg, err := loadConfigFromDatabase(ctx, dbPath)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			resolved, err := resolveConfigPath(configPath, configProvided)
			if err != nil {
				return config.Config{}, "", err
			}
			loaded, err := loadConfigFromPathOrEmbedded(resolved)
			if err != nil {
				return config.Config{}, "", err
			}
			config.ApplyEnvOverrides(loaded)
			cfg = *loaded
			cfg.MainSettings.DBPath = dbPath
			if err := saveConfigToDatabase(ctx, &cfg, dbPath); err != nil {
				return config.Config{}, "", err
			}
		} else {
			return config.Config{}, "", err
		}
	}

	if strings.TrimSpace(cfg.MainSettings.DBPath) == "" || cfg.MainSettings.DBPath != dbPath {
		cfg.MainSettings.DBPath = dbPath
		if err := saveConfigToDatabase(ctx, &cfg, dbPath); err != nil {
			return config.Config{}, "", err
		}
	}

	return cfg, dbPath, nil
}

func resolveConfigPath(configPath string, configProvided bool) (string, error) {
	if configProvided {
		if strings.TrimSpace(configPath) == "" {
			return "", errors.New("config path is required when --config is provided")
		}
		return configPath, nil
	}

	defaultPath, err := defaultConfigPath()
	if err != nil {
		return "", err
	}
	return defaultPath, nil
}

func loadConfigFromPathOrEmbedded(path string) (*config.Config, error) {
	if strings.TrimSpace(path) != "" {
		if _, err := os.Stat(path); err == nil {
			return config.ImportFromYAML(path)
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

func defaultConfigPath() (string, error) {
	defaultDBPath, err := db.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("default db path: %w", err)
	}
	return filepath.Join(filepath.Dir(defaultDBPath), defaultConfigName), nil
}

func loadConfigFromDatabase(ctx context.Context, dbPath string) (config.Config, error) {
	repo, err := db.Open(dbPath)
	if err != nil {
		return config.Config{}, err
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		return config.Config{}, err
	}

	loaded, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		return config.Config{}, err
	}
	if err := config.MergeMissingTrackerDefaults(loaded); err != nil {
		return config.Config{}, err
	}
	if len(config.DisableUnsupportedTrackerImageRehosts(loaded)) > 0 {
		if err := config.SaveToDatabase(ctx, loaded, repo); err != nil {
			return config.Config{}, err
		}
	}
	config.ApplyEnvOverrides(loaded)
	return *loaded, nil
}

func saveConfigToDatabase(ctx context.Context, cfg *config.Config, dbPath string) error {
	repo, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		return err
	}

	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		return err
	}

	return nil
}
