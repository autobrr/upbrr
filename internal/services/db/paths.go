// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultDirName = ".upbrr"
const defaultDBName = "db.sqlite"

func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "upbrr", defaultDBName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("database: user home dir: %w", err)
	}
	return filepath.Join(home, defaultDirName, defaultDBName), nil
}

func RootDir(dbPath string) (string, error) {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" || trimmed == ":memory:" || strings.HasPrefix(trimmed, "file:") {
		defaultPath, err := DefaultPath()
		if err != nil {
			return "", err
		}
		trimmed = defaultPath
	}
	cleaned := filepath.Clean(trimmed)
	if err := ensureDir(cleaned); err != nil {
		return "", err
	}
	return filepath.Dir(cleaned), nil
}

func Subdir(dbPath, name string) (string, error) {
	root, err := RootDir(dbPath)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("database: create subdir %q: %w", name, err)
	}
	return path, nil
}

func FileInSubdir(dbPath, dirName, fileName string) (string, error) {
	dir, err := Subdir(dbPath, dirName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

func CookiePath(dbPath, fileName string) (string, error) {
	return FileInSubdir(dbPath, "cookies", fileName)
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("database: create root dir: %w", err)
	}
	return nil
}
