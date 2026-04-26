// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package authmaterial

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	// AuthPasswordMinLength defines the minimum web auth password length.
	AuthPasswordMinLength = 12

	DesktopUsername = "__upbrr_desktop__"
	// DesktopAPITokenName is a token display name, not a secret.
	DesktopAPITokenName    = "upbrr desktop local" //nolint:gosec
	WebSessionAPITokenName = "web ui session"

	APITokenPurposeDesktop = "desktop"

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
	APITokens               []APIToken      `json:"api_tokens,omitempty"`
	PendingUpgrade          *PendingUpgrade `json:"pending_upgrade,omitempty"`
	CreatedAt               time.Time       `json:"created_at"`
}

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Purpose    string     `json:"purpose,omitempty"`
	Hash       string     `json:"hash"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type CreatedAPIToken struct {
	Token  string
	Record APIToken
}

type APITokenStatus struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Purpose    string     `json:"purpose,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
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
	return false, err
}

func (s *Store) Load() (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, err
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

func (s *Store) BootstrapReplacingDesktop(username string, password string) error {
	trimmedUsername := strings.TrimSpace(username)
	if trimmedUsername == "" {
		return errors.New("web auth: username is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	if strings.TrimSpace(record.Username) != DesktopUsername {
		return errors.New("web auth: user already exists")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	seed := strings.TrimSpace(record.EncryptionKeySeed)
	if seed == "" {
		seed, err = GenerateSeed()
		if err != nil {
			return err
		}
	}
	record.Username = trimmedUsername
	record.PasswordHash = hash
	record.EncryptionKeySeed = seed
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
		record.APITokens = append([]APIToken(nil), updated.APITokens...)
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
		target.APITokens = append([]APIToken(nil), record.APITokens...)
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

func (s *Store) CreateAPIToken(name string) (CreatedAPIToken, error) {
	return s.createAPIToken(name, "")
}

func (s *Store) CreateDesktopAPIToken() (CreatedAPIToken, error) {
	return s.createAPIToken(DesktopAPITokenName, APITokenPurposeDesktop)
}

func (s *Store) createAPIToken(name string, purpose string) (CreatedAPIToken, error) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return CreatedAPIToken{}, errors.New("api token name is required")
	}

	var created CreatedAPIToken
	err := s.updateRecordLocked(func(record *Record) error {
		id, err := randomHexString(8)
		if err != nil {
			return err
		}
		secret, err := randomHexString(32)
		if err != nil {
			return err
		}
		token := "upbrr_" + id + "_" + secret
		apiToken := APIToken{
			ID:        id,
			Name:      trimmedName,
			Purpose:   strings.TrimSpace(purpose),
			Hash:      hashAPIToken(token),
			CreatedAt: time.Now().UTC(),
		}
		record.APITokens = append(record.APITokens, apiToken)
		created = CreatedAPIToken{Token: token, Record: apiToken}
		return nil
	})
	return created, err
}

func (s *Store) ListAPITokens() ([]APITokenStatus, error) {
	record, err := s.Load()
	if err != nil {
		return nil, err
	}
	statuses := make([]APITokenStatus, 0, len(record.APITokens))
	for _, token := range record.APITokens {
		statuses = append(statuses, APITokenStatus{
			ID:         token.ID,
			Name:       token.Name,
			Purpose:    token.Purpose,
			CreatedAt:  token.CreatedAt,
			LastUsedAt: token.LastUsedAt,
			RevokedAt:  token.RevokedAt,
		})
	}
	return statuses, nil
}

func (s *Store) RevokeAPIToken(id string) error {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return errors.New("api token id is required")
	}
	now := time.Now().UTC()
	return s.updateRecordLocked(func(record *Record) error {
		for idx := range record.APITokens {
			if record.APITokens[idx].ID == trimmedID {
				record.APITokens[idx].RevokedAt = &now
				return nil
			}
		}
		return errors.New("api token not found")
	})
}

func (s *Store) VerifyAPIToken(rawToken string) (APITokenStatus, bool, error) {
	token := strings.TrimSpace(rawToken)
	id := apiTokenID(token)
	if id == "" {
		return APITokenStatus{}, false, nil
	}
	hash := hashAPIToken(token)
	now := time.Now().UTC()
	var status APITokenStatus
	found := false
	err := s.updateRecordLocked(func(record *Record) error {
		for idx := range record.APITokens {
			current := &record.APITokens[idx]
			if current.ID != id || current.RevokedAt != nil {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(current.Hash), []byte(hash)) != 1 {
				return nil
			}
			current.LastUsedAt = &now
			status = APITokenStatus{
				ID:         current.ID,
				Name:       current.Name,
				Purpose:    current.Purpose,
				CreatedAt:  current.CreatedAt,
				LastUsedAt: current.LastUsedAt,
				RevokedAt:  current.RevokedAt,
			}
			found = true
			return nil
		}
		return nil
	})
	return status, found, err
}

func apiTokenID(token string) string {
	remaining, ok := strings.CutPrefix(strings.TrimSpace(token), "upbrr_")
	if !ok {
		return ""
	}
	id, secret, ok := strings.Cut(remaining, "_")
	if !ok || strings.TrimSpace(secret) == "" {
		return ""
	}
	return strings.TrimSpace(id)
}

func hashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) updateRecordLocked(apply func(record *Record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
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
		return err
	}

	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(raw); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
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
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf)[:length], nil
}

func randomHexString(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
