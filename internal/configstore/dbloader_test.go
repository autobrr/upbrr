// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/services/db"
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
