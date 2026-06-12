// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigSectionSaveLoad(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := OpenWithLogger(dbPath, nopLogger{})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	// Define test data for a config section.
	type MainSettingsSection struct {
		TMDBAPI string `json:"tmdb_api"`
		DBPath  string `json:"db_path"`
	}

	testSettings := MainSettingsSection{
		TMDBAPI: "test-api-key",
		DBPath:  "/home/user/.upbrr/db.sqlite",
	}

	// Save section.
	if err := repo.SaveConfigSection(ctx, "main_settings", testSettings); err != nil {
		t.Fatalf("save config section: %v", err)
	}

	// Load section.
	var loaded MainSettingsSection
	if err := repo.LoadConfigSection(ctx, "main_settings", &loaded); err != nil {
		t.Fatalf("load config section: %v", err)
	}

	// Verify.
	if loaded.TMDBAPI != testSettings.TMDBAPI {
		t.Errorf("TMDBAPI mismatch: got %s, want %s", loaded.TMDBAPI, testSettings.TMDBAPI)
	}
	if loaded.DBPath != testSettings.DBPath {
		t.Errorf("DBPath mismatch: got %s, want %s", loaded.DBPath, testSettings.DBPath)
	}
}

func TestConfigSectionUpdate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	section := map[string]interface{}{
		"screens": 4,
		"cutoff":  2,
	}

	// Save initial.
	if err := repo.SaveConfigSection(ctx, "screenshot_handling", section); err != nil {
		t.Fatalf("save initial: %v", err)
	}

	// Update.
	section["screens"] = 6
	if err := repo.SaveConfigSection(ctx, "screenshot_handling", section); err != nil {
		t.Fatalf("save update: %v", err)
	}

	// Load and verify update.
	var loaded map[string]interface{}
	if err := repo.LoadConfigSection(ctx, "screenshot_handling", &loaded); err != nil {
		t.Fatalf("load updated: %v", err)
	}

	if screens, ok := loaded["screens"].(float64); !ok || int(screens) != 6 {
		t.Errorf("screens not updated: got %v", loaded["screens"])
	}
}

func TestConfigLoadNotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	// Try to load non-existent section.
	var loaded map[string]interface{}
	err = repo.LoadConfigSection(ctx, "nonexistent_section", &loaded)
	if err == nil {
		t.Fatalf("expected error for non-existent section, got nil")
	}
}

func TestFullConfigSaveLoad(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	// Create a full config structure.
	fullConfig := map[string]interface{}{
		"main_settings": map[string]interface{}{
			"tmdb_api": "test-key",
			"db_path":  "/test/db",
		},
		"screenshot_handling": map[string]interface{}{
			"screens":        4,
			"cutoff_screens": 2,
		},
		"torrent_creation": map[string]interface{}{
			"mkbrr_threads": 4,
			"prefer_max_16": true,
		},
	}

	// Save full config.
	if err := repo.SaveFullConfig(ctx, fullConfig); err != nil {
		t.Fatalf("save full config: %v", err)
	}

	// Load full config.
	var loaded map[string]interface{}
	if err := repo.LoadFullConfig(ctx, &loaded); err != nil {
		t.Fatalf("load full config: %v", err)
	}

	// Verify all sections present.
	if _, ok := loaded["main_settings"]; !ok {
		t.Errorf("main_settings section missing")
	}
	if _, ok := loaded["screenshot_handling"]; !ok {
		t.Errorf("screenshot_handling section missing")
	}
	if _, ok := loaded["torrent_creation"]; !ok {
		t.Errorf("torrent_creation section missing")
	}
}

func TestConfigLastUpdated(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	section := map[string]interface{}{
		"test": "data",
	}

	// Save section.
	beforeSave := time.Now().UTC()
	if err := repo.SaveConfigSection(ctx, "test_section", section); err != nil {
		t.Fatalf("save: %v", err)
	}
	afterSave := time.Now().UTC()

	// Get last updated time.
	lastUpdated, err := repo.ConfigSectionLastUpdated(ctx, "test_section")
	if err != nil {
		t.Fatalf("get last updated: %v", err)
	}

	// Verify timestamp is within expected range.
	if lastUpdated.Before(beforeSave) || lastUpdated.After(afterSave.Add(1*time.Second)) {
		t.Errorf("timestamp out of range: %v (expected between %v and %v)", lastUpdated, beforeSave, afterSave)
	}
}

func TestConfigSectionNotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	// Try to get last updated for non-existent section.
	_, err = repo.ConfigSectionLastUpdated(ctx, "nonexistent")
	if err == nil {
		t.Fatalf("expected error for non-existent section, got nil")
	}
}

func TestMultipleSectionsSaveLoad(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	// Save multiple sections.
	sections := map[string]interface{}{
		"main": map[string]string{"key": "value1"},
		"meta": map[string]string{"key": "value2"},
		"shot": map[string]string{"key": "value3"},
	}

	for section, data := range sections {
		if err := repo.SaveConfigSection(ctx, section, data); err != nil {
			t.Fatalf("save section %s: %v", section, err)
		}
	}

	// Load all sections and verify.
	for section, expectedData := range sections {
		var loaded map[string]string
		if err := repo.LoadConfigSection(ctx, section, &loaded); err != nil {
			t.Fatalf("load section %s: %v", section, err)
		}

		expectedJSON, err := json.Marshal(expectedData)
		if err != nil {
			t.Fatalf("marshal expected section %s: %v", section, err)
		}
		loadedJSON, err := json.Marshal(loaded)
		if err != nil {
			t.Fatalf("marshal loaded section %s: %v", section, err)
		}

		if string(expectedJSON) != string(loadedJSON) {
			t.Errorf("section %s mismatch: got %s, want %s", section, string(loadedJSON), string(expectedJSON))
		}
	}
}

func TestConfigComplexTypes(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	// Save complex nested structure.
	type ClientConfig struct {
		Type           string   `json:"type"`
		URL            string   `json:"url"`
		Categories     []string `json:"categories"`
		Tags           []string `json:"tags"`
		PreferMTV      bool     `json:"prefer_mtv"`
		MaxConnections int      `json:"max_connections"`
	}

	clientCfg := ClientConfig{
		Type:           "qbittorrent",
		URL:            "http://localhost:8080",
		Categories:     []string{"upload", "debug"},
		Tags:           []string{"tag1", "tag2"},
		PreferMTV:      true,
		MaxConnections: 10,
	}

	if err := repo.SaveConfigSection(ctx, "qbittorrent", clientCfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Load and verify.
	var loaded ClientConfig
	if err := repo.LoadConfigSection(ctx, "qbittorrent", &loaded); err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Type != clientCfg.Type {
		t.Errorf("type mismatch: got %s, want %s", loaded.Type, clientCfg.Type)
	}
	if loaded.URL != clientCfg.URL {
		t.Errorf("url mismatch: got %s, want %s", loaded.URL, clientCfg.URL)
	}
	if len(loaded.Categories) != len(clientCfg.Categories) {
		t.Errorf("categories mismatch: got %v, want %v", loaded.Categories, clientCfg.Categories)
	}
	if loaded.PreferMTV != clientCfg.PreferMTV {
		t.Errorf("prefer_mtv mismatch: got %v, want %v", loaded.PreferMTV, clientCfg.PreferMTV)
	}
	if loaded.MaxConnections != clientCfg.MaxConnections {
		t.Errorf("max_connections mismatch: got %d, want %d", loaded.MaxConnections, clientCfg.MaxConnections)
	}
}

func TestConfigContextCancellation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	repo, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Create a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	section := map[string]interface{}{
		"test": "data",
	}

	// Try to save with cancelled context.
	err = repo.SaveConfigSection(ctx, "test", section)
	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
}

func TestConfigNilRepository(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Try operations on nil repository.
	var repo *SQLiteRepository

	section := map[string]interface{}{}

	if err := repo.SaveConfigSection(ctx, "test", section); err == nil {
		t.Fatalf("expected error on nil repository SaveConfigSection")
	}

	if err := repo.LoadConfigSection(ctx, "test", &section); err == nil {
		t.Fatalf("expected error on nil repository LoadConfigSection")
	}

	if err := repo.SaveFullConfig(ctx, section); err == nil {
		t.Fatalf("expected error on nil repository SaveFullConfig")
	}

	if err := repo.LoadFullConfig(ctx, &section); err == nil {
		t.Fatalf("expected error on nil repository LoadFullConfig")
	}

	_, err := repo.ConfigSectionLastUpdated(ctx, "test")
	if err == nil {
		t.Fatalf("expected error on nil repository ConfigSectionLastUpdated")
	}
}
