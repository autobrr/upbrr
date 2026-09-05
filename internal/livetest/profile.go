// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package livetest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/pathing"
)

// Profile identifies a private, successfully constructed live-test profile.
// Its manifest contains paths and tracker IDs, never configuration secrets.
type Profile struct {
	Version           int      `json:"version"`
	RunID             string   `json:"runId"`
	RunDir            string   `json:"runDir"`
	DBPath            string   `json:"dbPath"`
	ConfigPath        string   `json:"configPath"`
	DefaultTrackers   []string `json:"defaultTrackers"`
	SourceKind        string   `json:"sourceKind"`
	SourceFingerprint string   `json:"sourceFingerprint"`
}

// PrivateRoot returns the OS-private cache root used for live-test state.
func PrivateRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("live-test private root: %w", err)
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("live-test private root must be absolute")
	}
	return filepath.Join(base, "upbrr-live-testing"), nil
}

// ValidateRunDir requires an immediate child of the private runs directory.
// Existing symlink or junction components are rejected, even within the root.
// The result uses PrivateRoot's spelling while preserving the run identifier.
func ValidateRunDir(runDir string) (string, error) {
	root, err := PrivateRoot()
	if err != nil {
		return "", err
	}
	runDir, err = filepath.Abs(runDir)
	if err != nil {
		return "", fmt.Errorf("live-test run directory: %w", err)
	}
	runs := filepath.Join(root, "runs")
	if !pathing.SamePath(filepath.Dir(runDir), runs) || !pathing.IsWithinRoot(root, runDir) {
		return "", errors.New("live-test run directory must be a child of the private runs root")
	}
	for current := runDir; ; {
		info, statErr := os.Lstat(current)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("live-test inspect directory: %w", statErr)
		}
		if statErr == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", errors.New("live-test directory has an unsafe filesystem identity")
		}
		if pathing.SamePath(current, root) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current || !pathing.IsWithinRoot(root, parent) {
			return "", errors.New("live-test directory ancestry did not reach the private root")
		}
		current = parent
	}
	return filepath.Join(runs, filepath.Base(runDir)), nil
}

func validateProfile(p Profile) error {
	runDir, err := ValidateRunDir(p.RunDir)
	if err != nil {
		return err
	}
	if p.Version != 1 || p.RunID == "" || p.RunID != filepath.Base(runDir) || len(p.DefaultTrackers) == 0 {
		return errors.New("live-test profile has invalid identity or empty defaults")
	}
	for _, id := range p.DefaultTrackers {
		if id == "" || strings.IndexFunc(id, func(r rune) bool { return (r < 'A' || r > 'Z') && (r < '0' || r > '9') }) >= 0 {
			return errors.New("live-test profile contains an invalid tracker ID")
		}
	}
	for _, pair := range [][2]string{{p.DBPath, "db.sqlite"}, {p.ConfigPath, "config.yaml"}} {
		expected := filepath.Join(runDir, "profile", pair[1])
		if pair[0] != expected || !pathing.IsWithinRoot(runDir, pair[0]) {
			return errors.New("live-test profile path mismatch")
		}
		info, statErr := os.Lstat(pair[0])
		if statErr != nil {
			return fmt.Errorf("live-test profile file: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return errors.New("live-test profile file is not regular")
		}
	}
	info, err := os.Lstat(filepath.Join(runDir, "profile"))
	if err != nil {
		return fmt.Errorf("live-test profile directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("live-test profile directory has an unsafe identity")
	}
	return nil
}

// PublishProfile publishes the manifest first and an atomic digest marker last.
// Callers must finish clone validation and close the database before publishing.
func PublishProfile(p Profile) error {
	if err := validateProfile(p); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("live-test encode profile: %w", err)
	}
	if err := writeProfileFile(filepath.Join(p.RunDir, "profile.json"), data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	return writeProfileFile(filepath.Join(p.RunDir, "profile-ready"), []byte(hex.EncodeToString(sum[:])))
}

func writeProfileFile(name string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(name), ".profile-*")
	if err != nil {
		return fmt.Errorf("live-test create manifest: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("live-test write manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("live-test sync manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("live-test close manifest: %w", err)
	}
	if err := os.Rename(file.Name(), name); err != nil {
		return fmt.Errorf("live-test publish manifest: %w", err)
	}
	return nil
}

// LoadProfile refuses incomplete, relocated, or path-escaped profiles.
func LoadProfile(runDir string) (Profile, error) {
	runDir, err := ValidateRunDir(runDir)
	if err != nil {
		return Profile{}, err
	}
	for _, name := range []string{"profile.json", "profile-ready"} {
		info, err := os.Lstat(filepath.Join(runDir, name))
		if err != nil {
			return Profile{}, fmt.Errorf("live-test profile is not ready: %w", err)
		}
		if !info.Mode().IsRegular() {
			return Profile{}, errors.New("live-test manifest has an unsafe identity")
		}
	}
	data, err := os.ReadFile(filepath.Join(runDir, "profile.json"))
	if err != nil {
		return Profile{}, fmt.Errorf("live-test read profile: %w", err)
	}
	marker, err := os.ReadFile(filepath.Join(runDir, "profile-ready"))
	if err != nil {
		return Profile{}, fmt.Errorf("live-test profile is not ready: %w", err)
	}
	sum := sha256.Sum256(data)
	if string(marker) != hex.EncodeToString(sum[:]) {
		return Profile{}, errors.New("live-test profile marker mismatch")
	}
	var p Profile
	if err := json.Unmarshal(data, &p, json.RejectUnknownMembers(true)); err != nil {
		return Profile{}, fmt.Errorf("live-test decode profile: %w", err)
	}
	if p.RunDir != runDir {
		return Profile{}, errors.New("live-test profile run identity mismatch")
	}
	if err := validateProfile(p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// ProfileForDB resolves a ready profile for its database, allowing equivalent filesystem paths.
func ProfileForDB(dbPath string) (Profile, error) {
	dbPath, err := filepath.Abs(dbPath)
	if err != nil {
		return Profile{}, fmt.Errorf("live-test database path: %w", err)
	}
	p, err := LoadProfile(filepath.Dir(filepath.Dir(dbPath)))
	if err != nil {
		return Profile{}, err
	}
	if !pathing.SamePath(dbPath, p.DBPath) {
		return Profile{}, errors.New("live-test database identity mismatch")
	}
	return p, nil
}
