// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore_test

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
)

func liveTestFixture(t *testing.T, legacy ...bool) (string, string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("XDG_CACHE_HOME", base)
	t.Setenv("HOME", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "source"))
	t.Setenv("UA_DEFAULT_DB_PATH", "")
	t.Setenv("UA_TRACKERS_DEFAULT", "")
	source, err := db.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	authfixture.Write(t, source)
	if len(legacy) != 0 && legacy[0] {
		record, err := authmaterial.LoadRecordFromDBPath(source)
		if err != nil {
			t.Fatal(err)
		}
		record.EncryptionKeySeed = ""
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(authmaterial.AuthFilePath(source), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = source
	cfg.MainSettings.TMDBAPI = "synthetic-config-secret"
	cfg.Trackers.DefaultTrackers = config.CSVList{"LST", "BHD"}
	cfg.TorrentClients = map[string]config.TorrentClientConfig{"client": {
		Type:        "watch",
		WatchFolder: filepath.Join(base, "source-watch"),
		StorageDir:  filepath.Join(base, "source-storage"),
		LocalPath:   config.StringList{filepath.Join(base, "media")},
		RemotePath:  config.StringList{filepath.Join(base, "remote")},
	}}
	if err := configstore.SaveToDBPath(t.Context(), cfg, source); err != nil {
		t.Fatal(err)
	}
	if err := cookies.SaveTrackerCookieMap(t.Context(), source, "LST", map[string]string{"session": "synthetic-cookie"}); err != nil {
		t.Fatal(err)
	}
	if err := trackerauth.SaveAuthState(t.Context(), source, "LST", "token", "synthetic-auth-state"); err != nil {
		t.Fatal(err)
	}
	root, err := livetest.PrivateRoot()
	if err != nil {
		t.Fatal(err)
	}
	return source, filepath.Join(root, "runs", "synthetic-run")
}

func TestCreateLiveTestProfilePreservesEncryptedConfigurationAndSource(t *testing.T) {
	source, runDir := liveTestFixture(t)
	legacyPath, err := db.CookiePath(source, "LST.json")
	if err != nil {
		t.Fatal(err)
	}
	legacyData := []byte(`{"session":"synthetic-legacy-cookie"}`)
	if err := os.WriteFile(legacyPath, legacyData, 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := authmaterial.LoadRecordFromDBPath(source)
	if err != nil {
		t.Fatal(err)
	}
	record.AllowUnencryptedExport = true
	record.AllowUnrestrictedBrowse = true
	record.APIKeys = []authmaterial.APIKeyRecord{{ID: "synthetic-key"}}
	authData, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authmaterial.AuthFilePath(source), authData, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.DefaultTrackers, []string{"LST", "BHD"}) {
		t.Fatalf("default ordering changed: %v", p.DefaultTrackers)
	}
	for _, legacy := range []string{legacyPath, filepath.Join(filepath.Dir(p.DBPath), "cookies", "LST.json")} {
		data, err := os.ReadFile(legacy)
		if err != nil || !bytes.Equal(data, legacyData) {
			t.Fatal("legacy cookie source or copy changed")
		}
	}
	ready, err := livetest.ProfileForDB(p.DBPath)
	if err != nil || ready.RunID != p.RunID {
		t.Fatalf("ready profile: %+v, %v", ready, err)
	}
	cfg, err := configstore.LoadFromDBPath(t.Context(), p.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MainSettings.TMDBAPI != "synthetic-config-secret" || cfg.MainSettings.DBPath != p.DBPath {
		t.Fatal("cloned encrypted configuration was not preserved")
	}
	if cfg.TorrentClients["client"].WatchFolder != filepath.Join(runDir, "artifacts", "watch") {
		t.Fatal("watch output was not isolated")
	}
	if got, err := cookies.LoadTrackerCookieMap(t.Context(), p.DBPath, "LST"); err != nil || got["session"] != "synthetic-cookie" {
		t.Fatalf("cloned cookies unusable: %v", err)
	}
	if got, err := trackerauth.LoadAuthState(t.Context(), p.DBPath, "LST", "token"); err != nil || got != "synthetic-auth-state" {
		t.Fatalf("cloned auth state unusable: %v", err)
	}
	clonedAuth, err := authmaterial.LoadRecordFromDBPath(p.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if clonedAuth.Username != record.Username || clonedAuth.PasswordHash != record.PasswordHash || clonedAuth.EncryptionKeySeed != record.EncryptionKeySeed || !clonedAuth.CreatedAt.Equal(record.CreatedAt) {
		t.Fatal("auth derivation material changed")
	}
	if len(clonedAuth.APIKeys) != 0 || clonedAuth.AllowUnencryptedExport || clonedAuth.AllowUnrestrictedBrowse || clonedAuth.BrowseRoot != runDir {
		t.Fatal("production web auth permissions survived")
	}
	for _, pair := range []struct {
		path   string
		before []byte
	}{{source, sourceBytes}, {authmaterial.AuthFilePath(source), authData}} {
		after, err := os.ReadFile(pair.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pair.before, after) {
			t.Fatal("source bytes changed")
		}
	}
	manifest, err := os.ReadFile(filepath.Join(runDir, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifest, []byte("synthetic-config-secret")) || bytes.Contains(manifest, []byte(record.PasswordHash)) {
		t.Fatal("profile manifest contains secrets")
	}
}

func TestCreateLiveTestProfilePreservesLegacyAuthHelper(t *testing.T) {
	_, runDir := liveTestFixture(t, true)
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	record, err := authmaterial.LoadRecordFromDBPath(p.DBPath)
	if err != nil || record.EncryptionKeySeed != "" {
		t.Fatal("legacy auth derivation changed")
	}
	if got, err := trackerauth.LoadAuthState(t.Context(), p.DBPath, "LST", "token"); err != nil || got != "synthetic-auth-state" {
		t.Fatalf("legacy cloned auth unusable: %v", err)
	}
}

func TestCreateLiveTestProfileRejectsUnsafeSources(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{"pending_upgrade", func(t *testing.T, source string) {
			record, err := authmaterial.LoadRecordFromDBPath(source)
			if err != nil {
				t.Fatal(err)
			}
			record.PendingUpgrade = &authmaterial.PendingUpgrade{Stage: authmaterial.UpgradeStagePrepared}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(authmaterial.AuthFilePath(source), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"mismatched_helper", func(t *testing.T, source string) {
			authfixture.Write(t, source, authfixture.Options{EncryptionKeySeed: "different-synthetic-seed"})
		}},
		{"empty_defaults", func(t *testing.T, source string) {
			repo, err := db.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if _, err := repo.RawDB().ExecContext(t.Context(), "UPDATE config_settings SET data = json_set(data, '$.DefaultTrackers', json('[]')) WHERE section = 'Trackers'"); err != nil {
				t.Fatal(err)
			}
		}},
		{"unknown_state", func(t *testing.T, source string) {
			repo, err := db.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if _, err := repo.RawDB().ExecContext(t.Context(), "CREATE TABLE future_authority(value TEXT); INSERT INTO future_authority VALUES ('synthetic')"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, runDir := liveTestFixture(t)
			test.prepare(t, source)
			if _, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir); err == nil {
				t.Fatal("unsafe source accepted")
			}
			if _, err := livetest.LoadProfile(runDir); err == nil {
				t.Fatal("failed construction published runnable profile")
			}
		})
	}
}

func TestCreateLiveTestProfileAppliesProvidedConfigOnlyToClone(t *testing.T) {
	source, runDir := liveTestFixture(t)
	provided := filepath.Join(t.TempDir(), "overlay.yaml")
	content := []byte("trackers:\n  default_trackers: BHD\n")
	if err := os.WriteFile(provided, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UA_DEFAULT_DB_PATH", source)
	p, err := configstore.CreateLiveTestProfile(t.Context(), provided, true, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.DefaultTrackers, []string{"BHD"}) {
		t.Fatalf("provided selection lost: %v", p.DefaultTrackers)
	}
	after, err := os.ReadFile(provided)
	if err != nil || !bytes.Equal(content, after) {
		t.Fatal("provided source changed")
	}
}

func TestCreateLiveTestProfileResolvesExplicitEnvironmentScope(t *testing.T) {
	source, runDir := liveTestFixture(t)
	repo, err := db.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), "UPDATE config_settings SET data = json_set(data, '$.DefaultTrackers', json('[]')) WHERE section = 'Trackers'"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UA_TRACKERS_DEFAULT", "bhd,lst")
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.DefaultTrackers, []string{"BHD", "LST"}) {
		t.Fatalf("effective scope changed: %v", p.DefaultTrackers)
	}
}

func TestCreateLiveTestProfilePreservesUnavailableDefaultTracker(t *testing.T) {
	source, runDir := liveTestFixture(t)
	defaults := config.CSVList{"LST", "UNAVAILABLE", "BHD"}
	cfg, err := configstore.LoadFromDBPath(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Trackers.DefaultTrackers = defaults
	if _, exists := cfg.Trackers.Trackers["UNAVAILABLE"]; exists {
		t.Fatal("fixture unexpectedly configures the unavailable tracker")
	}
	if err := configstore.SaveToDBPath(t.Context(), cfg, source); err != nil {
		t.Fatal(err)
	}
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.DefaultTrackers, []string(defaults)) {
		t.Fatal("profile construction dropped or reordered an unavailable default")
	}
	manifest, err := livetest.LoadProfile(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.DefaultTrackers, []string(defaults)) {
		t.Fatal("profile manifest dropped or reordered an unavailable default")
	}
	loaded, err := configstore.LoadLiveTestConfig(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Trackers.DefaultTrackers, defaults) {
		t.Fatal("profile loading dropped or reordered an unavailable default")
	}
	if _, exists := loaded.Trackers.Trackers["UNAVAILABLE"]; exists {
		t.Fatal("profile loading fabricated configuration for an unavailable default")
	}
}

func TestLoadLiveTestConfigIgnoresLaterEnvironment(t *testing.T) {
	_, runDir := liveTestFixture(t)
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := configstore.LoadLiveTestConfig(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("UA_DEFAULT_DB_PATH", filepath.Join(t.TempDir(), "redirected.sqlite"))
	t.Setenv("UA_DEFAULT_TMDB_API", "different-synthetic-secret")
	t.Setenv("UA_TRACKERS_DEFAULT", "BHD")
	t.Setenv("UA_DEFAULT_SCREENS", "99")
	loaded, err := configstore.LoadLiveTestConfig(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(captured, loaded) {
		t.Fatal("profile config changed with later environment overrides")
	}
	if loaded.MainSettings.DBPath != p.DBPath || !reflect.DeepEqual(loaded.Trackers.DefaultTrackers, config.CSVList{"LST", "BHD"}) {
		t.Fatal("profile DB or tracker scope changed")
	}
}

func TestLoadLiveTestConfigValidatesReadyProfileBeforeOpeningDatabase(t *testing.T) {
	_, runDir := liveTestFixture(t)
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	redirectedPath := filepath.Join(t.TempDir(), "redirected.sqlite")
	for _, mutate := range []func(*livetest.Profile){
		func(p *livetest.Profile) { p.DBPath = redirectedPath },
		func(p *livetest.Profile) { p.RunID = "other-run" },
		func(p *livetest.Profile) { p.DefaultTrackers = []string{"BHD"} },
	} {
		candidate := p
		mutate(&candidate)
		if _, err := configstore.LoadLiveTestConfig(t.Context(), candidate); err == nil {
			t.Fatal("mismatched profile accepted")
		}
	}
	if _, err := os.Stat(redirectedPath); !os.IsNotExist(err) {
		t.Fatal("mismatched profile opened a different database")
	}
	if err := os.Remove(filepath.Join(runDir, "profile-ready")); err != nil {
		t.Fatal(err)
	}
	if _, err := configstore.LoadLiveTestConfig(t.Context(), p); err == nil {
		t.Fatal("unready profile accepted")
	}
}

func TestApplyLiveTestPathsPreservesDiscoveryAndConfiguration(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	p := livetest.Profile{
		RunDir: filepath.Join(base, "run"),
		DBPath: filepath.Join(base, "run", "profile", "db.sqlite"),
	}
	client := config.TorrentClientConfig{
		Type:         "qbittorrent",
		URL:          "http://localhost:8080",
		Password:     "synthetic-secret",
		WatchFolder:  filepath.Join(base, "watch"),
		StorageDir:   filepath.Join(base, "storage"),
		LinkedFolder: config.StringList{filepath.Join(base, "linked")},
		LocalPath:    config.StringList{filepath.Join(base, "local")},
		RemotePath:   config.StringList{filepath.Join(base, "remote")},
	}
	cfg := &config.Config{TorrentClients: map[string]config.TorrentClientConfig{"client": client, "unlinked": {}}}
	cfg.MainSettings.DBPath = filepath.Join(base, "production.sqlite")
	configstore.ApplyLiveTestPaths(cfg, p)
	client.WatchFolder = filepath.Join(p.RunDir, "artifacts", "watch")
	client.StorageDir = filepath.Join(p.RunDir, "artifacts", "torrents")
	client.LinkedFolder = config.StringList{filepath.Join(p.RunDir, "artifacts", "links")}
	if cfg.MainSettings.DBPath != p.DBPath || !reflect.DeepEqual(cfg.TorrentClients["client"], client) {
		t.Fatal("path rebase changed discovery or missed writable paths")
	}
	if cfg.TorrentClients["unlinked"].LinkedFolder != nil {
		t.Fatal("path rebase enabled client linking")
	}
}

func TestCreateLiveTestProfilePreservesAuthWithHistoricalMediaMigrations(t *testing.T) {
	source, runDir := liveTestFixture(t)
	repo, err := db.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	mediaPath := filepath.Join(t.TempDir(), "Synthetic.Media")
	imagePath := filepath.Join(t.TempDir(), "synthetic.png")
	// The historical generation migration has the same three runtime tables
	// as the current canonical generation migration already in this fixture.
	for _, id := range []string{
		"2026_07_add_prepared_release_generations",
		"2026_08_add_multi_disc_media_binding",
		"2026_08_bind_prepared_media_assets",
	} {
		if _, err := repo.RawDB().ExecContext(t.Context(), "INSERT INTO schema_migrations VALUES (?, 'synthetic')", id); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`ALTER TABLE playlist_selections ADD COLUMN source_fingerprint TEXT NOT NULL DEFAULT ""`,
		`INSERT INTO playlist_selections (source_path, updated_at, source_fingerprint) VALUES (?, 'synthetic', 'synthetic-fingerprint')`,
		`INSERT INTO external_ids (source_path, generation, updated_at) VALUES (?, 1, 'synthetic')`,
		`INSERT INTO external_metadata (source_path, generation, updated_at) VALUES (?, 1, 'synthetic')`,
		`INSERT INTO prepared_release_current (
			source_path, generation, source_fingerprint, fact_instruction_fingerprint, policy_fingerprint, contract_version,
			source_json, naming_json, episode_json, media_json, disc_json, assessments_json, prepared_at
		) VALUES (?, 1, 'synthetic', 'synthetic', 'synthetic', 'synthetic', '{}', '{}', '{}', '{}', '{}', '{}', 'synthetic')`,
	} {
		var args []any
		if strings.Contains(statement, "?") {
			args = []any{mediaPath}
		}
		if _, err := repo.RawDB().ExecContext(t.Context(), statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"screenshots", "screenshot_final_selections", "uploaded_images", "screenshot_slots", "screenshot_slot_variants"} {
		for _, column := range []string{
			`prepared_media_fingerprint TEXT NOT NULL DEFAULT ""`,
			`prepared_generation INTEGER NOT NULL DEFAULT 0`,
			`disc_id TEXT NOT NULL DEFAULT ""`,
		} {
			if _, err := repo.RawDB().ExecContext(t.Context(), "ALTER TABLE "+table+" ADD COLUMN "+column); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, statement := range []string{
		`INSERT INTO screenshots (source_path, image_path, timestamp, frame_number, width, height, purpose, captured_at)
			VALUES (?, ?, 0, 1, 640, 480, 'scene', 'synthetic')`,
		`INSERT INTO screenshot_final_selections (source_path, image_path, sort_order, source, selected_at) VALUES (?, ?, 1, 'local', 'synthetic')`,
		`INSERT INTO uploaded_images (source_path, image_path, host, uploaded_at) VALUES (?, ?, 'synthetic-host', 'synthetic')`,
		`INSERT INTO screenshot_slots (source_path, image_path, slot_order) VALUES (?, ?, 1)`,
		`INSERT INTO screenshot_slot_variants (source_path, image_path, slot_order, host) VALUES (?, ?, 1, 'synthetic-host')`,
	} {
		if _, err := repo.RawDB().ExecContext(t.Context(), statement, mediaPath, imagePath); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"screenshots", "screenshot_final_selections", "uploaded_images", "screenshot_slots", "screenshot_slot_variants"} {
		if _, err := repo.RawDB().ExecContext(t.Context(), "UPDATE "+table+" SET prepared_media_fingerprint = 'synthetic', prepared_generation = 1, disc_id = 'disc-1'"); err != nil {
			t.Fatal(err)
		}
	}
	expectedConfig, err := config.LoadFromDatabase(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyEnvOverrides(expectedConfig)
	authBefore := snapshotLiveTestAuthFixture(t, repo)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	authBytes, err := os.ReadFile(authmaterial.AuthFilePath(source))
	if err != nil {
		t.Fatal(err)
	}
	p, err := configstore.CreateLiveTestProfile(t.Context(), "", false, runDir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := configstore.LoadLiveTestConfig(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	configstore.ApplyLiveTestPaths(expectedConfig, p)
	if !reflect.DeepEqual(expectedConfig, loaded) {
		t.Fatal("historical profile changed configuration beyond the allowed path rebasing")
	}
	clone, err := db.Open(p.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	if !reflect.DeepEqual(authBefore, snapshotLiveTestAuthFixture(t, clone)) {
		t.Fatal("historical profile changed encrypted auth identities, ciphertext, timestamps, or derivation metadata")
	}
	for _, table := range []string{
		"playlist_selections", "external_ids", "external_metadata", "prepared_release_current", "screenshots",
		"screenshot_final_selections", "uploaded_images", "screenshot_slots", "screenshot_slot_variants",
	} {
		var count int
		if err := clone.RawDB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("historical media domain %s retained production authority", table)
		}
	}
	var retainedIDs int
	if err := clone.RawDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE id IN (
		'2026_07_add_prepared_release_generations', '2026_08_add_multi_disc_media_binding', '2026_08_bind_prepared_media_assets'
	)`).Scan(&retainedIDs); err != nil || retainedIDs != 3 {
		t.Fatalf("historical migration identity changed: count=%d err=%v", retainedIDs, err)
	}
	for _, file := range []struct {
		path   string
		before []byte
	}{{source, sourceBytes}, {authmaterial.AuthFilePath(source), authBytes}} {
		after, err := os.ReadFile(file.path)
		if err != nil || !bytes.Equal(file.before, after) {
			t.Fatal("historical source DB or auth bytes changed")
		}
	}
}

func snapshotLiveTestAuthFixture(t *testing.T, repo *db.SQLiteRepository) []string {
	t.Helper()
	snapshot := make([]string, 0, 4)
	for _, query := range []string{
		`SELECT json_group_array(json_array(id, tracker_id, cookie_name, encrypted_value, nonce, auth_tag, created_at, updated_at))
			FROM (SELECT * FROM tracker_cookies ORDER BY id)`,
		`SELECT json_group_array(json_array(tracker_id, state_key, encrypted_value, nonce, auth_tag, created_at, updated_at))
			FROM (SELECT * FROM tracker_auth_state ORDER BY tracker_id, state_key)`,
		`SELECT data FROM config_settings WHERE section = 'cookies_encryption_salt'`,
		`SELECT data FROM config_settings WHERE section = 'cookies_encryption_auth_state'`,
	} {
		var value string
		if err := repo.RawDB().QueryRowContext(t.Context(), query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, value)
	}
	return snapshot
}
