// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionManagerDeletesExpiredSessionsInBackground(t *testing.T) {
	manager := &sessionManager{
		ttl:          time.Minute,
		cleanupEvery: 10 * time.Millisecond,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		sessions: map[string]session{
			"expired": {
				ID:        "expired",
				Username:  "tester",
				CSRFToken: "csrf",
				ExpiresAt: time.Now().UTC().Add(-time.Second),
			},
		},
	}
	go manager.cleanupLoop()
	defer manager.Close()

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		_, exists := manager.sessions["expired"]
		manager.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("expected expired session to be removed by background cleanup")
}

func TestSessionManagerPersistsRetainedSessionsAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")

	manager, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager: %v", err)
	}
	current, err := manager.Create("tester", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	manager.Close()

	reloaded, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager reload: %v", err)
	}
	defer reloaded.Close()

	restored, ok := reloaded.Get(current.ID)
	if !ok {
		t.Fatal("expected retained session to be restored")
	}
	if restored.Username != "tester" {
		t.Fatalf("restored username = %q, want %q", restored.Username, "tester")
	}
	if !restored.Retain {
		t.Fatal("expected restored session to remain marked as retained")
	}
}

func TestSessionManagerDoesNotPersistNonRetainedSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")

	manager, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager: %v", err)
	}
	current, err := manager.Create("tester", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	manager.Close()

	reloaded, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager reload: %v", err)
	}
	defer reloaded.Close()

	if _, ok := reloaded.Get(current.ID); ok {
		t.Fatal("expected non-retained session to be discarded after restart")
	}
}

func TestSessionManagerSkipsExpiredPersistedSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	store, err := newSessionStore(dbPath)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	if err := store.Save([]session{{
		ID:        "expired",
		Username:  "tester",
		CSRFToken: "csrf",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Retain:    true,
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	manager, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager: %v", err)
	}
	defer manager.Close()

	if _, ok := manager.Get("expired"); ok {
		t.Fatal("expected expired persisted session to be ignored")
	}

	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected expired persisted sessions to be cleaned up, got %d entries", len(stored))
	}
}

func TestSessionManagerDeleteRestoresRetainedSessionOnPersistFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")

	manager, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager: %v", err)
	}
	defer manager.Close()

	current, err := manager.Create("tester", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	blockedPath := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	manager.store.path = blockedPath

	if err := manager.Delete(current.ID); err == nil {
		t.Fatal("expected Delete to fail when persistence fails")
	}
	if _, ok := manager.Get(current.ID); !ok {
		t.Fatal("expected retained session to be restored in memory after persistence failure")
	}
}

func TestSessionManagerCloseStopsCleanupLoop(t *testing.T) {
	manager := &sessionManager{
		ttl:          time.Minute,
		cleanupEvery: time.Hour,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		sessions:     make(map[string]session),
	}
	go manager.cleanupLoop()

	done := make(chan struct{})
	go func() {
		manager.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected Close to stop the cleanup loop")
	}
}
