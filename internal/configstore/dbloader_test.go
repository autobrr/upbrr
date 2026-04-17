// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
)

func TestLoadFromDBPathDisablesUnsupportedTrackerImageRehost(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		_ = repo.Close()
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = dbPath
	trackerCfg := cfg.Trackers.Trackers["TL"]
	trackerCfg.ImgRehost = true
	cfg.Trackers.Trackers["TL"] = trackerCfg

	if err := config.SaveToDatabase(context.Background(), cfg, repo); err != nil {
		_ = repo.Close()
		t.Fatalf("save config: %v", err)
	}
	_ = repo.Close()

	loaded, err := configstore.LoadFromDBPath(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("load config from database: %v", err)
	}
	if loaded.Trackers.Trackers["TL"].ImgRehost {
		t.Fatal("expected unsupported TL img_rehost to be disabled on load")
	}
}

func TestLoadFromDBPathBackfillsMissingTrackerDefaults(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		_ = repo.Close()
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = dbPath
	delete(cfg.Trackers.Trackers, "BTN")

	if err := config.SaveToDatabase(context.Background(), cfg, repo); err != nil {
		_ = repo.Close()
		t.Fatalf("save config: %v", err)
	}
	_ = repo.Close()

	loaded, err := configstore.LoadFromDBPath(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("load config from database: %v", err)
	}
	if _, ok := loaded.Trackers.Trackers["BTN"]; !ok {
		t.Fatal("expected BTN tracker to be backfilled on load")
	}
}

func TestSaveToDBPathSyncsCookieEncryptionStateWhenWebAuthExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = dbPath

	if err := configstore.SaveToDBPath(ctx, cfg, dbPath); err != nil {
		t.Fatalf("SaveToDBPath: %v", err)
	}

	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	var authStateJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"cookies_encryption_auth_state",
	).Scan(&authStateJSON); err != nil {
		t.Fatalf("query auth state: %v", err)
	}

	var state map[string]any
	if err := json.Unmarshal([]byte(authStateJSON), &state); err != nil {
		t.Fatalf("unmarshal auth state: %v", err)
	}
	if _, ok := state["helper"]; ok {
		t.Fatalf("expected persisted auth state to omit helper, got %s", authStateJSON)
	}
	if fingerprint, ok := state["fingerprint"].(string); !ok || fingerprint == "" {
		t.Fatalf("expected persisted auth fingerprint, got %s", authStateJSON)
	}

	var saltJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"cookies_encryption_salt",
	).Scan(&saltJSON); err != nil {
		t.Fatalf("query encryption salt: %v", err)
	}
	if saltJSON == "" {
		t.Fatal("expected persisted cookie encryption salt")
	}

	authPath := webserver.AuthFilePath(dbPath)
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("expected auth file to remain present: %v", err)
	}
}
