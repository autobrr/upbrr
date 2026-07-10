// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package authmaterial

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/redaction"

	"golang.org/x/crypto/argon2"
)

const (
	// AuthPasswordMinLength defines the minimum web auth password length.
	AuthPasswordMinLength = 12

	// OWASP Password Storage Cheat Sheet Argon2id baseline.
	authArgon2Time        = 2
	authArgon2MemoryKB    = 19 * 1024
	authArgon2Parallelism = 1
	authArgon2KeyLen      = 32
	authArgon2Version     = argon2.Version

	legacyAuthArgon2Time        = 1
	legacyAuthArgon2MemoryKB    = 64 * 1024
	legacyAuthArgon2Parallelism = 4
	legacyAuthArgon2KeyLen      = 32
)

type Record struct {
	Username                string          `json:"username"`
	PasswordHash            string          `json:"password_hash"`
	EncryptionKeySeed       string          `json:"encryption_key_seed,omitempty"`
	AllowUnencryptedExport  bool            `json:"allow_unencrypted_export,omitempty"`
	BrowseRoot              string          `json:"browse_root,omitempty"`
	AllowUnrestrictedBrowse bool            `json:"allow_unrestricted_browse,omitempty"`
	PendingUpgrade          *PendingUpgrade `json:"pending_upgrade,omitempty"`
	CreatedAt               time.Time       `json:"created_at"`
}

type PendingUpgrade struct {
	Stage     string    `json:"stage"`
	Target    Record    `json:"target"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	UpgradeStagePrepared         = "prepared"
	UpgradeStageCookiesRewrapped = "cookies_rewrapped"
	UpgradeStageDataRewrapped    = "data_rewrapped"
)

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(strings.TrimSpace(dbPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("web auth: create config dir: %w", err)
	}
	return &Store{path: filepath.Join(dir, WebAuthFileName)}, nil
}

func AuthFilePath(dbPath string) string {
	return filepath.Join(filepath.Dir(strings.TrimSpace(dbPath)), WebAuthFileName)
}

func BootstrapAuthFile(dbPath string, username string, password string) error {
	store, err := NewStore(dbPath)
	if err != nil {
		return err
	}
	return store.Bootstrap(username, password)
}

func (s *Store) Exists() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := os.Stat(s.path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("web auth: stat auth file: %w", err)
}

func (s *Store) Load() (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return Record{}, fmt.Errorf("web auth: read auth file: %w", err)
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, fmt.Errorf("web auth: unmarshal auth file: %s", redaction.RedactValue(err.Error(), nil))
	}
	return record, nil
}

func (s *Store) Bootstrap(username string, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.path); err == nil {
		return errors.New("web auth: user already exists")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	seed, err := GenerateSeed()
	if err != nil {
		return err
	}

	record := Record{
		Username:          strings.TrimSpace(username),
		PasswordHash:      hash,
		EncryptionKeySeed: seed,
		CreatedAt:         time.Now().UTC(),
	}
	return s.saveLocked(record)
}

func (s *Store) UpdatePasswordHash(username string, passwordHash string) error {
	return s.updateRecordLocked(func(record *Record) error {
		if record.Username != strings.TrimSpace(username) {
			return errors.New("web auth: user mismatch")
		}
		record.PasswordHash = strings.TrimSpace(passwordHash)
		return nil
	})
}

func (s *Store) UpdateRecord(updated Record) error {
	return s.updateRecordLocked(func(record *Record) error {
		if record.Username != strings.TrimSpace(updated.Username) {
			return errors.New("web auth: user mismatch")
		}

		record.PasswordHash = strings.TrimSpace(updated.PasswordHash)
		record.EncryptionKeySeed = strings.TrimSpace(updated.EncryptionKeySeed)
		record.AllowUnencryptedExport = updated.AllowUnencryptedExport
		record.BrowseRoot = strings.TrimSpace(updated.BrowseRoot)
		record.AllowUnrestrictedBrowse = updated.AllowUnrestrictedBrowse
		record.PendingUpgrade = updated.PendingUpgrade
		return nil
	})
}

func (s *Store) BeginPendingUpgrade(current Record, target Record) error {
	return s.updateRecordLocked(func(record *Record) error {
		if record.Username != strings.TrimSpace(current.Username) {
			return errors.New("web auth: user mismatch")
		}
		if strings.TrimSpace(record.PasswordHash) != strings.TrimSpace(current.PasswordHash) ||
			strings.TrimSpace(record.EncryptionKeySeed) != strings.TrimSpace(current.EncryptionKeySeed) ||
			record.AllowUnencryptedExport != current.AllowUnencryptedExport ||
			strings.TrimSpace(record.BrowseRoot) != strings.TrimSpace(current.BrowseRoot) ||
			record.AllowUnrestrictedBrowse != current.AllowUnrestrictedBrowse {
			return errors.New("web auth: auth record changed during upgrade")
		}

		target.PendingUpgrade = nil
		record.PendingUpgrade = &PendingUpgrade{
			Stage:     UpgradeStagePrepared,
			Target:    target,
			UpdatedAt: time.Now().UTC(),
		}
		return nil
	})
}

func (s *Store) AdvancePendingUpgrade(username string, stage string) error {
	stage = strings.TrimSpace(stage)
	switch stage {
	case UpgradeStagePrepared, UpgradeStageCookiesRewrapped, UpgradeStageDataRewrapped:
	default:
		return fmt.Errorf("web auth: invalid upgrade stage %q", stage)
	}

	return s.updateRecordLocked(func(record *Record) error {
		if record.Username != strings.TrimSpace(username) {
			return errors.New("web auth: user mismatch")
		}
		if record.PendingUpgrade == nil {
			return errors.New("web auth: no pending upgrade")
		}
		record.PendingUpgrade.Stage = stage
		record.PendingUpgrade.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) FinalizePendingUpgrade(username string) (Record, error) {
	var finalized Record

	err := s.updateRecordLocked(func(record *Record) error {
		if record.Username != strings.TrimSpace(username) {
			return errors.New("web auth: user mismatch")
		}
		if record.PendingUpgrade == nil {
			return errors.New("web auth: no pending upgrade")
		}

		target := record.PendingUpgrade.Target
		target.PendingUpgrade = nil
		target.CreatedAt = record.CreatedAt
		*record = target
		finalized = target
		return nil
	})

	return finalized, err
}

func (s *Store) ClearPendingUpgrade(username string) error {
	return s.updateRecordLocked(func(record *Record) error {
		if record.Username != strings.TrimSpace(username) {
			return errors.New("web auth: user mismatch")
		}
		record.PendingUpgrade = nil
		return nil
	})
}

func (s *Store) updateRecordLocked(apply func(record *Record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("web auth: read auth file: %w", err)
	}

	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("web auth: unmarshal auth file: %s", redaction.RedactValue(err.Error(), nil))
	}
	if err := apply(&record); err != nil {
		return err
	}

	return s.saveLocked(record)
}

func (r Record) AuthMaterial() Material {
	return Material{
		Username:               strings.TrimSpace(r.Username),
		PasswordHash:           strings.TrimSpace(r.PasswordHash),
		EncryptionKeySeed:      strings.TrimSpace(r.EncryptionKeySeed),
		AllowUnencryptedExport: r.AllowUnencryptedExport,
	}
}

func (s *Store) saveLocked(record Record) error {
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("web auth: marshal auth file: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("web auth: create temp auth file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(raw); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: write temp auth file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: sync temp auth file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: close temp auth file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: chmod temp auth file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: replace auth file: %w", err)
	}
	return nil
}

func HashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if len(password) < AuthPasswordMinLength {
		return "", fmt.Errorf("password must be at least %d characters", AuthPasswordMinLength)
	}
	salt, err := randomString(16)
	if err != nil {
		return "", err
	}
	sum := argon2.IDKey(
		[]byte(password),
		[]byte(salt),
		authArgon2Time,
		authArgon2MemoryKB,
		authArgon2Parallelism,
		authArgon2KeyLen,
	)
	return fmt.Sprintf(
		"argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		authArgon2Version,
		authArgon2MemoryKB,
		authArgon2Time,
		authArgon2Parallelism,
		salt,
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

func VerifyPassword(password string, encoded string) bool {
	ok, _ := VerifyPasswordWithUpgrade(password, encoded)
	return ok
}

func VerifyPasswordWithUpgrade(password string, encoded string) (bool, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) < 3 || parts[0] != "argon2id" {
		return false, false
	}

	salt, hashPart, params, legacy, ok := parseAuthHash(parts)
	if !ok {
		return false, false
	}

	sum := argon2.IDKey(
		[]byte(password),
		[]byte(salt),
		params.time,
		params.memoryKB,
		params.parallelism,
		params.keyLen,
	)
	expected, err := base64.RawStdEncoding.DecodeString(hashPart)
	if err != nil {
		return false, false
	}
	return subtle.ConstantTimeCompare(sum, expected) == 1, legacy
}

type authHashParams struct {
	time        uint32
	memoryKB    uint32
	parallelism uint8
	keyLen      uint32
}

func parseAuthHash(parts []string) (string, string, authHashParams, bool, bool) {
	if len(parts) == 3 {
		return parts[1], parts[2], authHashParams{
			time:        legacyAuthArgon2Time,
			memoryKB:    legacyAuthArgon2MemoryKB,
			parallelism: legacyAuthArgon2Parallelism,
			keyLen:      legacyAuthArgon2KeyLen,
		}, true, true
	}

	if len(parts) != 5 {
		return "", "", authHashParams{}, false, false
	}

	version, ok := parseAuthHashVersion(parts[1])
	if !ok || version <= 0 {
		return "", "", authHashParams{}, false, false
	}

	params, ok := parseAuthHashConfig(parts[2])
	if !ok {
		return "", "", authHashParams{}, false, false
	}

	return parts[3], parts[4], params, false, true
}

func parseAuthHashVersion(part string) (int, bool) {
	if !strings.HasPrefix(part, "v=") {
		return 0, false
	}

	version, err := strconv.Atoi(strings.TrimPrefix(part, "v="))
	if err != nil {
		return 0, false
	}

	return version, true
}

func parseAuthHashConfig(part string) (authHashParams, bool) {
	fields := strings.Split(part, ",")
	if len(fields) != 3 {
		return authHashParams{}, false
	}

	var params authHashParams
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			return authHashParams{}, false
		}

		switch key {
		case "m":
			parsed, err := strconv.ParseUint(value, 10, 32)
			if err != nil || parsed == 0 {
				return authHashParams{}, false
			}
			params.memoryKB = uint32(parsed)
		case "t":
			parsed, err := strconv.ParseUint(value, 10, 32)
			if err != nil || parsed == 0 {
				return authHashParams{}, false
			}
			params.time = uint32(parsed)
		case "p":
			parsed, err := strconv.ParseUint(value, 10, 8)
			if err != nil || parsed == 0 {
				return authHashParams{}, false
			}
			params.parallelism = uint8(parsed)
		default:
			return authHashParams{}, false
		}
	}

	if params.memoryKB == 0 || params.time == 0 || params.parallelism == 0 {
		return authHashParams{}, false
	}

	params.keyLen = authArgon2KeyLen
	return params, true
}

func randomString(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth material: generate random string: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)[:length], nil
}
