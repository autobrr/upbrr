// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const defaultDirName = ".upbrr"
const defaultDBName = "db.sqlite"

// DefaultPath returns the preferred database path without creating it. When
// XDG_CONFIG_HOME is set, an existing canonical database wins, followed by an
// existing legacy .upbrr database under that same config root; otherwise a new
// canonical XDG path is returned. Without XDG_CONFIG_HOME, the user-home
// .upbrr path is returned.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		preferred := filepath.Join(xdg, "upbrr", defaultDBName)
		if dbFileExists(preferred) {
			return preferred, nil
		}
		// Migration fallback: installs predating the XDG move keep their data in
		// a dotted ".upbrr" dir. The documented Docker setup ran with
		// HOME=/config (== XDG_CONFIG_HOME here), so that data lives at
		// $XDG_CONFIG_HOME/.upbrr; older $HOME-based installs at $HOME/.upbrr.
		// Prefer an existing legacy DB so upgrades do not orphan it.
		if legacy := filepath.Join(xdg, defaultDirName, defaultDBName); dbFileExists(legacy) {
			return legacy, nil
		}
		if home := os.Getenv("HOME"); home != "" && sameConfigRoot(home, xdg) {
			if legacy := filepath.Join(home, defaultDirName, defaultDBName); dbFileExists(legacy) {
				return legacy, nil
			}
		}
		return preferred, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("database: user home dir: %w", err)
	}
	return filepath.Join(home, defaultDirName, defaultDBName), nil
}

// dbFileExists reports whether path is an existing regular file.
func dbFileExists(path string) bool {
	info, err := os.Lstat(path) //nolint:gosec // Existence probe for a default/legacy DB path derived from trusted env config.
	return err == nil && info.Mode().IsRegular()
}

func sameConfigRoot(a, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cleanA, cleanB)
	}
	return cleanA == cleanB
}

// RootDir returns and creates the parent directory used for database-adjacent
// files. Empty, in-memory, and SQLite URI inputs deliberately resolve to the
// default on-disk database directory instead of deriving a directory from the
// connection string.
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

// Subdir creates name beneath [RootDir] and returns its path. Newly created
// directories use mode 0700.
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

// FileInSubdir creates dirName beneath [RootDir] and returns fileName joined
// within it; it does not create the file.
func FileInSubdir(dbPath, dirName, fileName string) (string, error) {
	dir, err := Subdir(dbPath, dirName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// CookiePath returns a path beneath the managed cookies subdirectory, creating
// that directory when needed.
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
