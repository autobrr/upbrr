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

// LoadFromDatabase loads the full config from the repository.
func LoadFromDatabase(ctx context.Context, repo interface {
	LoadFullConfig(ctx context.Context, dest any) error
}) (*Config, error) {
	if repo == nil {
		return nil, errors.New("config load: nil repository")
	}

	var cfg Config
	if err := repo.LoadFullConfig(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("config load from database: %w", err)
	}

	decryptedCfg, err := DecryptConfigSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("config load from database: decrypt secrets: %w", err)
	}

	return decryptedCfg, nil
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
