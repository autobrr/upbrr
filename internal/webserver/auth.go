// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

const (
	sessionCookieName = "ua_web_session"
	authFileName      = "web-auth.json"
	sessionFileName   = "web-sessions.json"
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

type authRecord struct {
	Username               string    `json:"username"`
	PasswordHash           string    `json:"password_hash"`
	EncryptionKeySeed      string    `json:"encryption_key_seed,omitempty"`
	AllowUnencryptedExport bool      `json:"allow_unencrypted_export,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

type authStore struct {
	path string
	mu   sync.Mutex
}

// AuthFileName is the canonical web auth file name stored beside the database.
const AuthFileName = authFileName

func newAuthStore(dbPath string) (*authStore, error) {
	dir := filepath.Dir(strings.TrimSpace(dbPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("web auth: create config dir: %w", err)
	}
	return &authStore{path: filepath.Join(dir, authFileName)}, nil
}

// AuthFilePath returns the auth file path colocated with dbPath.
func AuthFilePath(dbPath string) string {
	return filepath.Join(filepath.Dir(strings.TrimSpace(dbPath)), authFileName)
}

// BootstrapAuthFile creates the canonical auth file beside dbPath.
func BootstrapAuthFile(dbPath string, username string, password string) error {
	store, err := newAuthStore(dbPath)
	if err != nil {
		return err
	}
	return store.Bootstrap(username, password)
}

func (s *authStore) Exists() (bool, error) {
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

func (s *authStore) Load() (authRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return authRecord{}, err
	}
	var record authRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return authRecord{}, err
	}
	return record, nil
}

func (s *authStore) Bootstrap(username string, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.path); err == nil {
		return errors.New("web auth: user already exists")
	}

	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	seed, err := authmaterial.GenerateSeed()
	if err != nil {
		return err
	}

	record := authRecord{
		Username:          strings.TrimSpace(username),
		PasswordHash:      hash,
		EncryptionKeySeed: seed,
		CreatedAt:         time.Now().UTC(),
	}
	return s.saveLocked(record)
}

func (s *authStore) UpdatePasswordHash(username string, passwordHash string) error {
	record, err := s.Load()
	if err != nil {
		return err
	}
	record.PasswordHash = passwordHash
	return s.UpdateRecord(record)
}

func (s *authStore) UpdateRecord(updated authRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var record authRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	if record.Username != strings.TrimSpace(updated.Username) {
		return errors.New("web auth: user mismatch")
	}

	record.PasswordHash = strings.TrimSpace(updated.PasswordHash)
	record.EncryptionKeySeed = strings.TrimSpace(updated.EncryptionKeySeed)
	return s.saveLocked(record)
}

func (r authRecord) authMaterial() authmaterial.Material {
	return authmaterial.Material{
		Username:               strings.TrimSpace(r.Username),
		PasswordHash:           strings.TrimSpace(r.PasswordHash),
		EncryptionKeySeed:      strings.TrimSpace(r.EncryptionKeySeed),
		AllowUnencryptedExport: r.AllowUnencryptedExport,
	}
}

func (s *authStore) saveLocked(record authRecord) error {
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return err
	}
	return nil
}

func hashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if len(password) < 10 {
		return "", errors.New("password must be at least 10 characters")
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

func verifyPassword(password string, encoded string) bool {
	ok, _ := verifyPasswordWithUpgrade(password, encoded)
	return ok
}

func verifyPasswordWithUpgrade(password string, encoded string) (bool, bool) {
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
	if !ok || version != authArgon2Version {
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

type session struct {
	ID        string
	Username  string
	CSRFToken string
	ExpiresAt time.Time
	Retain    bool
}

type sessionStore struct {
	path string
	mu   sync.Mutex
}

func newSessionStore(dbPath string) (*sessionStore, error) {
	dir := filepath.Dir(strings.TrimSpace(dbPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("web auth: create config dir: %w", err)
	}
	return &sessionStore{path: filepath.Join(dir, sessionFileName)}, nil
}

func (s *sessionStore) Load() ([]session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []session
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (s *sessionStore) Save(sessions []session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(sessions) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	raw, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
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
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := syncDir(dir); err != nil {
		return err
	}

	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return nil
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		if errors.Is(err, os.ErrInvalid) || os.IsPermission(err) {
			return nil
		}
		return err
	}
	if err := dir.Close(); err != nil {
		return err
	}
	return nil
}

type sessionManager struct {
	ttl          time.Duration
	cleanupEvery time.Duration
	stopCh       chan struct{}
	doneCh       chan struct{}
	stopOnce     sync.Once
	store        *sessionStore
	logf         func(string, ...any)
	mu           sync.Mutex
	sessions     map[string]session
}

func newSessionManager(ttlMinutes int, dbPath string) (*sessionManager, error) {
	if ttlMinutes <= 0 {
		ttlMinutes = 1440
	}
	store, err := newSessionStore(dbPath)
	if err != nil {
		return nil, err
	}
	manager := &sessionManager{
		ttl:          time.Duration(ttlMinutes) * time.Minute,
		cleanupEvery: time.Minute,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		store:        store,
		sessions:     make(map[string]session),
	}
	if err := manager.loadPersisted(); err != nil {
		return nil, err
	}
	go manager.cleanupLoop()
	return manager, nil
}

func (m *sessionManager) SetLogger(logf func(string, ...any)) {
	if m == nil {
		return
	}
	m.logf = logf
}

func (m *sessionManager) Create(username string, retainLogin bool) (session, error) {
	id, err := randomString(32)
	if err != nil {
		return session{}, err
	}
	csrf, err := randomString(24)
	if err != nil {
		return session{}, err
	}

	current := session{
		ID:        id,
		Username:  strings.TrimSpace(username),
		CSRFToken: csrf,
		ExpiresAt: time.Now().UTC().Add(m.ttl),
		Retain:    retainLogin,
	}

	m.mu.Lock()
	m.sessions[id] = current
	m.mu.Unlock()

	if retainLogin {
		if err := m.persistRetained(); err != nil {
			m.mu.Lock()
			delete(m.sessions, id)
			m.mu.Unlock()
			m.logPersistError("create retained session", err)
			return session{}, err
		}
	}

	return current, nil
}

func (m *sessionManager) Get(id string) (session, bool) {
	m.mu.Lock()
	current, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return session{}, false
	}
	if time.Now().UTC().After(current.ExpiresAt) {
		delete(m.sessions, id)
		shouldPersist := current.Retain
		m.mu.Unlock()
		if shouldPersist {
			if err := m.persistRetained(); err != nil {
				m.logPersistError("cleanup expired retained session", err)
			}
		}
		return session{}, false
	}
	m.mu.Unlock()
	return current, true
}

func (m *sessionManager) Delete(id string) error {
	m.mu.Lock()
	current, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok && current.Retain {
		if err := m.persistRetained(); err != nil {
			m.mu.Lock()
			m.sessions[id] = current
			m.mu.Unlock()
			return err
		}
	}
	return nil
}

func (m *sessionManager) Close() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		close(m.stopCh)
		<-m.doneCh
	})
}

func (m *sessionManager) cleanupLoop() {
	ticker := time.NewTicker(m.cleanupEvery)
	defer func() {
		ticker.Stop()
		close(m.doneCh)
	}()

	for {
		select {
		case <-ticker.C:
			m.deleteExpired(time.Now().UTC())
		case <-m.stopCh:
			return
		}
	}
}

func (m *sessionManager) deleteExpired(now time.Time) {
	m.mu.Lock()
	changed := false

	for id, current := range m.sessions {
		if now.After(current.ExpiresAt) {
			delete(m.sessions, id)
			if current.Retain {
				changed = true
			}
		}
	}
	m.mu.Unlock()

	if changed {
		if err := m.persistRetained(); err != nil {
			m.logPersistError("cleanup expired retained sessions", err)
		}
	}
}

func (m *sessionManager) loadPersisted() error {
	if m == nil || m.store == nil {
		return nil
	}

	stored, err := m.store.Load()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	filtered := make([]session, 0, len(stored))
	for _, current := range stored {
		if !current.Retain || current.ID == "" || now.After(current.ExpiresAt) {
			continue
		}
		m.sessions[current.ID] = current
		filtered = append(filtered, current)
	}

	if len(filtered) != len(stored) {
		return m.store.Save(filtered)
	}
	return nil
}

func (m *sessionManager) persistRetained() error {
	if m == nil || m.store == nil {
		return nil
	}

	m.mu.Lock()
	retained := make([]session, 0, len(m.sessions))
	now := time.Now().UTC()
	for _, current := range m.sessions {
		if current.Retain && !now.After(current.ExpiresAt) {
			retained = append(retained, current)
		}
	}
	m.mu.Unlock()

	return m.store.Save(retained)
}

func (m *sessionManager) logPersistError(action string, err error) {
	if m == nil || err == nil || m.logf == nil {
		return
	}
	m.logf("web: failed to %s: %v", action, err)
}

func randomString(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func ipFromAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return strings.TrimSpace(remoteAddr)
	}
	return host
}

type rateLimitBucket struct {
	Count   int
	ResetAt time.Time
}

type fixedWindowLimiter struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	items  map[string]rateLimitBucket
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:  limit,
		window: window,
		items:  make(map[string]rateLimitBucket),
	}
}

func (l *fixedWindowLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()

	current := l.items[key]
	if now.After(current.ResetAt) {
		current = rateLimitBucket{ResetAt: now.Add(l.window)}
	}
	if current.Count >= l.limit {
		l.items[key] = current
		return false
	}
	current.Count++
	l.items[key] = current
	return true
}
