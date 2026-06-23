// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// TestLoadFromDBPathPersistsMergedTrackerDefaults verifies legacy metadata BTN
// tokens move into tracker defaults without keeping a duplicate metadata copy.
func TestLoadFromDBPathPersistsMergedTrackerDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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
	cfg.Metadata.BTNAPI = "legacy-btn-token"
	btn := cfg.Trackers.Trackers["BTN"]
	btn.APIKey = ""
	cfg.Trackers.Trackers["BTN"] = btn

	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		_ = repo.Close()
		t.Fatalf("save config: %v", err)
	}
	_ = repo.Close()

	loaded, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load config from database: %v", err)
	}
	if got := loaded.Trackers.Trackers["BTN"].APIKey; got != "legacy-btn-token" {
		t.Fatalf("expected runtime BTN API key to be merged, got %q", got)
	}
	if got := loaded.Metadata.BTNAPI; got != "" {
		t.Fatalf("expected legacy BTN metadata API key to be cleared, got %q", got)
	}

	repo, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	var trackersJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"Trackers",
	).Scan(&trackersJSON); err != nil {
		t.Fatalf("query trackers: %v", err)
	}
	var persisted struct {
		Trackers map[string]config.TrackerConfig `json:"Trackers"`
	}
	if err := json.Unmarshal([]byte(trackersJSON), &persisted); err != nil {
		t.Fatalf("unmarshal trackers: %v", err)
	}
	if got := persisted.Trackers["BTN"].APIKey; got != "legacy-btn-token" {
		t.Fatalf("expected persisted BTN API key to be merged, got %q", got)
	}
	var metadataJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"Metadata",
	).Scan(&metadataJSON); err != nil {
		t.Fatalf("query metadata: %v", err)
	}
	var metadata config.MetadataConfig
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if got := metadata.BTNAPI; got != "" {
		t.Fatalf("expected persisted metadata BTN API key to be cleared, got %q", got)
	}
}

func TestLoadFromDBPathRollsBackMultiSectionRepairOnLaterFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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
	cfg.Metadata.BTNAPI = "legacy-btn-token"
	btn := cfg.Trackers.Trackers["BTN"]
	btn.APIKey = ""
	cfg.Trackers.Trackers["BTN"] = btn

	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		_ = repo.Close()
		t.Fatalf("save config: %v", err)
	}
	_ = repo.Close()

	installFailTrackersSectionTrigger(ctx, t, dbPath)

	_, err = configstore.LoadFromDBPath(ctx, dbPath)
	if err == nil {
		t.Fatal("expected load repair save to fail")
	}

	repo, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	var metadataJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"Metadata",
	).Scan(&metadataJSON); err != nil {
		t.Fatalf("query metadata: %v", err)
	}
	var metadata config.MetadataConfig
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if got := metadata.BTNAPI; got != "legacy-btn-token" {
		t.Fatalf("expected metadata BTN token rollback, got %q", got)
	}

	var trackersJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"Trackers",
	).Scan(&trackersJSON); err != nil {
		t.Fatalf("query trackers: %v", err)
	}
	var trackers struct {
		Trackers map[string]config.TrackerConfig `json:"Trackers"`
	}
	if err := json.Unmarshal([]byte(trackersJSON), &trackers); err != nil {
		t.Fatalf("unmarshal trackers: %v", err)
	}
	if got := trackers.Trackers["BTN"].APIKey; got != "" {
		t.Fatalf("expected tracker BTN API key rollback, got %q", got)
	}
}

func TestLoadFromDBPathBackfillsMissingStoredOptions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `
		INSERT INTO config_settings (section, data, updated_at)
		VALUES ('PostUpload', '{"CrossSeeding":false}', datetime('now'))
	`); err != nil {
		_ = repo.Close()
		t.Fatalf("insert partial config: %v", err)
	}
	_ = repo.Close()

	loaded, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load config from database: %v", err)
	}
	if got := loaded.PostUpload.MaxConcurrentTrackers; got != 4 {
		t.Fatalf("expected max concurrent tracker uploads default, got %d", got)
	}
	if loaded.PostUpload.CrossSeeding {
		t.Fatal("expected explicit cross_seeding=false to be preserved")
	}
	if len(loaded.TorrentClients) != 0 {
		t.Fatalf("expected template torrent clients to stay stripped, got %d", len(loaded.TorrentClients))
	}

	repo, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	var postUploadJSON string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"PostUpload",
	).Scan(&postUploadJSON); err != nil {
		t.Fatalf("query post_upload: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal([]byte(postUploadJSON), &persisted); err != nil {
		t.Fatalf("unmarshal post_upload: %v", err)
	}
	if got, ok := persisted["MaxConcurrentTrackers"].(float64); !ok || got != 4 {
		t.Fatalf("expected persisted max concurrent tracker uploads default, got %#v", persisted["MaxConcurrentTrackers"])
	}
	if got, ok := persisted["CrossSeeding"].(bool); !ok || got {
		t.Fatalf("expected persisted explicit cross_seeding=false, got %#v", persisted["CrossSeeding"])
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

	var persistedSalt struct {
		Salt string `json:"salt"`
	}
	if err := json.Unmarshal([]byte(saltJSON), &persistedSalt); err != nil {
		t.Fatalf("unmarshal encryption salt: %v", err)
	}
	if len(persistedSalt.Salt) == 0 {
		t.Fatalf("expected persisted cookie encryption salt, got %s", saltJSON)
	}

	authPath := webserver.AuthFilePath(dbPath)
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("expected auth file to remain present: %v", err)
	}
}

func TestSaveToDBPathMalformedWebAuthFailsBeforeConfigSave(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	if err := os.WriteFile(webserver.AuthFilePath(dbPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed auth file: %v", err)
	}

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = dbPath

	err = configstore.SaveToDBPath(ctx, cfg, dbPath)
	if err == nil {
		t.Fatal("expected SaveToDBPath to fail for malformed web auth")
	}

	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	for _, section := range []string{
		"MainSettings",
		"cookies_encryption_salt",
		"cookies_encryption_auth_state",
	} {
		var data string
		err = repo.RawDB().QueryRowContext(ctx,
			`SELECT data FROM config_settings WHERE section = ?`,
			section,
		).Scan(&data)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected section %q to be absent after failed sync, got row=%q err=%v", section, data, err)
		}
	}
}

func TestSaveToDBPathRollsBackCookieSyncWhenConfigSaveFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}
	for _, statement := range []string{
		`CREATE TRIGGER fail_main_settings_insert BEFORE INSERT ON config_settings WHEN NEW.section = 'MainSettings' BEGIN SELECT RAISE(ABORT, 'forced main settings failure'); END`,
		`CREATE TRIGGER fail_main_settings_update BEFORE UPDATE ON config_settings WHEN NEW.section = 'MainSettings' BEGIN SELECT RAISE(ABORT, 'forced main settings failure'); END`,
	} {
		if _, err := repo.RawDB().ExecContext(ctx, statement); err != nil {
			_ = repo.Close()
			t.Fatalf("create failure trigger: %v", err)
		}
	}
	_ = repo.Close()

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = dbPath

	err = configstore.SaveToDBPath(ctx, cfg, dbPath)
	if err == nil {
		t.Fatal("expected SaveToDBPath to fail")
	}

	repo, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	for _, section := range []string{
		"cookies_encryption_salt",
		"cookies_encryption_auth_state",
	} {
		var data string
		err = repo.RawDB().QueryRowContext(ctx,
			`SELECT data FROM config_settings WHERE section = ?`,
			section,
		).Scan(&data)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected section %q to be absent after failed config save, got row=%q err=%v", section, data, err)
		}
	}
}

func TestSaveToDBPathCookieSyncRollsBackWhenConfigSaveFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	installFailMainSettingsTrigger(ctx, t, dbPath)

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = dbPath

	err = configstore.SaveToDBPath(ctx, cfg, dbPath)
	if err == nil {
		t.Fatal("expected SaveToDBPath to fail")
	}

	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	assertConfigStoreSectionsAbsent(ctx, t, repo, "MainSettings", "cookies_encryption_salt", "cookies_encryption_auth_state")
}

func TestSaveToDBPathIncompleteWebAuthStillSavesConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "guiapp.db")
	if err := os.WriteFile(webserver.AuthFilePath(dbPath), []byte(`{"username":"tester"}`), 0o600); err != nil {
		t.Fatalf("write incomplete auth file: %v", err)
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

	var mainSettings string
	if err := repo.RawDB().QueryRowContext(ctx,
		`SELECT data FROM config_settings WHERE section = ?`,
		"MainSettings",
	).Scan(&mainSettings); err != nil {
		t.Fatalf("expected main_settings to be saved with unavailable auth helper: %v", err)
	}
}

func installFailMainSettingsTrigger(ctx context.Context, t *testing.T, dbPath string) {
	t.Helper()

	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `
		CREATE TRIGGER fail_main_settings_save
		BEFORE INSERT ON config_settings
		WHEN NEW.section = 'MainSettings'
		BEGIN
			SELECT RAISE(FAIL, 'forced main settings failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
}

func installFailTrackersSectionTrigger(ctx context.Context, t *testing.T, dbPath string) {
	t.Helper()

	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}
	for _, statement := range []string{
		`CREATE TRIGGER fail_trackers_section_insert BEFORE INSERT ON config_settings WHEN NEW.section = 'Trackers' BEGIN SELECT RAISE(FAIL, 'forced config section failure'); END`,
		`CREATE TRIGGER fail_trackers_section_update BEFORE UPDATE ON config_settings WHEN NEW.section = 'Trackers' BEGIN SELECT RAISE(FAIL, 'forced config section failure'); END`,
	} {
		if _, err := repo.RawDB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}
	}
}

func assertConfigStoreSectionsAbsent(ctx context.Context, t *testing.T, repo *db.SQLiteRepository, sections ...string) {
	t.Helper()

	for _, section := range sections {
		var data string
		err := repo.RawDB().QueryRowContext(ctx,
			`SELECT data FROM config_settings WHERE section = ?`,
			section,
		).Scan(&data)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected section %q to be absent, got row=%q err=%v", section, data, err)
		}
	}
}
