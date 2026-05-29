// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

const (
	sessionCookieName = "ua_web_session"
	sessionFileName   = "web-sessions.json"

	legacyAuthArgon2Time        = 1
	legacyAuthArgon2MemoryKB    = 64 * 1024
	legacyAuthArgon2Parallelism = 4
	legacyAuthArgon2KeyLen      = 32
)

type authRecord = authmaterial.Record
type authStore = authmaterial.Store

// AuthFileName is the canonical web auth file name stored beside the database.
const AuthFileName = authmaterial.WebAuthFileName

// AuthPasswordMinLength defines the minimum web auth password length.
const AuthPasswordMinLength = authmaterial.AuthPasswordMinLength

func newAuthStore(dbPath string) (*authStore, error) {
	return wrapWebResult(authmaterial.NewStore(dbPath))
}

// AuthFilePath returns the auth file path colocated with dbPath.
func AuthFilePath(dbPath string) string {
	return authmaterial.AuthFilePath(dbPath)
}

// BootstrapAuthFile creates the canonical auth file beside dbPath.
func BootstrapAuthFile(dbPath string, username string, password string) error {
	return wrapWebError(authmaterial.BootstrapAuthFile(dbPath, username, password))
}

func hashPassword(password string) (string, error) {
	return wrapWebResult(authmaterial.HashPassword(password))
}

func verifyPassword(password string, encoded string) bool {
	return authmaterial.VerifyPassword(password, encoded)
}

func verifyPasswordWithUpgrade(password string, encoded string) (bool, bool) {
	return authmaterial.VerifyPasswordWithUpgrade(password, encoded)
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
		return nil, fmt.Errorf("web auth: read session file: %w", err)
	}
	var sessions []session
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return nil, fmt.Errorf("web auth: unmarshal session file: %w", err)
	}
	return sessions, nil
}

func (s *sessionStore) Save(sessions []session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(sessions) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("web auth: remove session file: %w", err)
		}
		return nil
	}

	raw, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("web auth: marshal session file: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("web auth: create temp session file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: chmod temp session file: %w", err)
	}
	if _, err := tmpFile.Write(raw); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: write temp session file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: sync temp session file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: close temp session file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("web auth: replace session file: %w", err)
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
		return fmt.Errorf("web auth: sync session dir: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("web auth: close session dir: %w", err)
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
		return "", fmt.Errorf("web: generate random string: %w", err)
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
