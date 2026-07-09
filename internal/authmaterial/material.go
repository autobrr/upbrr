// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package authmaterial

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
)

const (
	WebAuthFileName = "web-auth.json"
)

var ErrUnavailable = errors.New("web auth material unavailable")

type Material struct {
	Username               string `json:"username"`
	PasswordHash           string `json:"password_hash"`
	EncryptionKeySeed      string `json:"encryption_key_seed,omitempty"`
	AllowUnencryptedExport bool   `json:"allow_unencrypted_export,omitempty"`
}

func LoadFromDBPath(dbPath string) (Material, error) {
	record, err := LoadRecordFromDBPath(dbPath)
	if err != nil {
		return Material{}, err
	}
	material := record.AuthMaterial()
	if !material.IsUsable() {
		return Material{}, ErrUnavailable
	}

	return material, nil
}

func LoadRecordFromDBPath(dbPath string) (Record, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return Record{}, ErrUnavailable
	}

	authPath := filepath.Join(filepath.Dir(dbPath), WebAuthFileName)
	file, err := os.Open(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrUnavailable
		}
		return Record{}, fmt.Errorf("failed to open web auth file %q: %w", authPath, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Record{}, fmt.Errorf("failed to stat web auth file %q: %w", authPath, err)
	}
	if err := validateWebAuthPermissions(authPath, info); err != nil {
		return Record{}, err
	}

	raw, err := io.ReadAll(file)
	if err != nil {
		return Record{}, fmt.Errorf("failed to read web auth file: %w", err)
	}

	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, fmt.Errorf("failed to parse web auth file: %s", redaction.RedactValue(err.Error(), nil))
	}

	if !record.AuthMaterial().IsUsable() {
		return Record{}, ErrUnavailable
	}

	return record, nil
}

func validateWebAuthPermissions(authPath string, info os.FileInfo) error {
	if runtime.GOOS == "windows" || info == nil {
		return nil
	}

	perm := info.Mode().Perm()
	if perm == 0o600 || perm == 0o640 || perm == 0o400 || perm == 0o440 {
		return nil
	}

	return fmt.Errorf("web auth file %q has insecure permissions %#o: %w", authPath, perm, ErrUnavailable)
}

func (m Material) IsUsable() bool {
	return strings.TrimSpace(m.Username) != "" && strings.TrimSpace(m.PasswordHash) != ""
}

func (m Material) WithSeed(seed string) Material {
	m.EncryptionKeySeed = strings.TrimSpace(seed)
	return m
}

func (m Material) PrimaryHelper() (string, string, error) {
	if helper, fingerprint, ok := m.StableHelper(); ok {
		return helper, fingerprint, nil
	}
	return m.LegacyHelper()
}

func (m Material) StableHelper() (string, string, bool) {
	username := strings.TrimSpace(m.Username)
	seed := strings.TrimSpace(m.EncryptionKeySeed)
	if username == "" || seed == "" {
		return "", "", false
	}
	helper := "stable:" + username + ":" + seed
	return helper, fingerprint(helper), true
}

func (m Material) LegacyHelper() (string, string, error) {
	username := strings.TrimSpace(m.Username)
	passwordHash := strings.TrimSpace(m.PasswordHash)
	if username == "" || passwordHash == "" {
		return "", "", ErrUnavailable
	}
	helper := username + ":" + passwordHash
	return helper, fingerprint(helper), nil
}

func GenerateSeed() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate encryption seed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func fingerprint(helper string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(helper)))
	return hex.EncodeToString(sum[:])
}
