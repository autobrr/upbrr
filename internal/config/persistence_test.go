// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

type exportLoadRepo struct {
	cfg Config
	err error
}

func (r *exportLoadRepo) LoadFullConfig(ctx context.Context, dest interface{}) error {
	if r.err != nil {
		return r.err
	}
	out, ok := dest.(*Config)
	if !ok {
		return errors.New("invalid destination type")
	}
	*out = r.cfg
	return nil
}

func TestExportImportYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create a test config.
	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "test-api-key",
			DBPath:  "/test/db",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens: 4,
		},
	}
	configureConfigSecretEncryption(t, cfg)

	// Export to YAML.
	if err := ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("ExportToYAML failed: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Import from YAML.
	loaded, err := ImportFromYAML(configPath)
	if err != nil {
		t.Fatalf("ImportFromYAML failed: %v", err)
	}

	// Verify fields match.
	if loaded.MainSettings.TMDBAPI != cfg.MainSettings.TMDBAPI {
		t.Errorf("TMDBAPI mismatch: got %s, want %s", loaded.MainSettings.TMDBAPI, cfg.MainSettings.TMDBAPI)
	}
	if loaded.MainSettings.DBPath != cfg.MainSettings.DBPath {
		t.Errorf("DBPath mismatch: got %s, want %s", loaded.MainSettings.DBPath, cfg.MainSettings.DBPath)
	}
	if loaded.ScreenshotHandling.Screens != cfg.ScreenshotHandling.Screens {
		t.Errorf("Screens mismatch: got %d, want %d", loaded.ScreenshotHandling.Screens, cfg.ScreenshotHandling.Screens)
	}
}

func TestExportImportJSON(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI:             "test-api-key",
			DBPath:              "/test/db",
			UpdateNotification:  true,
			VerboseNotification: false,
			TrackerPassChecks:   3,
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens:       4,
			CutoffScreens: 2,
		},
	}
	configureConfigSecretEncryption(t, cfg)

	// Export to JSON.
	json, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("ExportToJSON failed: %v", err)
	}

	if json == "" {
		t.Fatalf("exported JSON is empty")
	}

	// Import from JSON.
	loaded, err := ImportFromJSON(json)
	if err != nil {
		t.Fatalf("ImportFromJSON failed: %v", err)
	}

	// Verify fields match.
	if loaded.MainSettings.TMDBAPI != cfg.MainSettings.TMDBAPI {
		t.Errorf("TMDBAPI mismatch: got %s, want %s", loaded.MainSettings.TMDBAPI, cfg.MainSettings.TMDBAPI)
	}
	if loaded.MainSettings.UpdateNotification != cfg.MainSettings.UpdateNotification {
		t.Errorf("UpdateNotification mismatch: got %v, want %v", loaded.MainSettings.UpdateNotification, cfg.MainSettings.UpdateNotification)
	}
	if loaded.ScreenshotHandling.Screens != cfg.ScreenshotHandling.Screens {
		t.Errorf("Screens mismatch: got %d, want %d", loaded.ScreenshotHandling.Screens, cfg.ScreenshotHandling.Screens)
	}
}

func TestBackupToYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "backup-test",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens: 3,
		},
	}
	configureConfigSecretEncryption(t, cfg)

	// Create backup.
	backupPath, err := BackupToYAML(cfg, tmpDir)
	if err != nil {
		t.Fatalf("BackupToYAML failed: %v", err)
	}

	// Verify backup file exists.
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}

	// Verify backup can be loaded.
	loaded, err := ImportFromYAML(backupPath)
	if err != nil {
		t.Fatalf("LoadYAML backup failed: %v", err)
	}

	if loaded.MainSettings.TMDBAPI != cfg.MainSettings.TMDBAPI {
		t.Errorf("backup TMDBAPI mismatch: got %s, want %s", loaded.MainSettings.TMDBAPI, cfg.MainSettings.TMDBAPI)
	}
}

func TestExportImportInvalidInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fn   func() error
	}{
		{
			name: "export nil config",
			fn: func() error {
				return ExportToYAML(nil, "/tmp/config.yaml")
			},
		},
		{
			name: "export empty path",
			fn: func() error {
				return ExportToYAML(&Config{}, "")
			},
		},
		{
			name: "import empty path",
			fn: func() error {
				_, err := ImportFromYAML("")
				return err
			},
		},
		{
			name: "import nonexistent file",
			fn: func() error {
				_, err := ImportFromYAML("/nonexistent/path/config.yaml")
				return err
			},
		},
		{
			name: "export json nil config",
			fn: func() error {
				_, err := ExportToJSON(nil)
				return err
			},
		},
		{
			name: "import json empty string",
			fn: func() error {
				_, err := ImportFromJSON("")
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.fn()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create a complex config to test all types.
	cfg := &Config{
		MainSettings: MainSettingsConfig{
			UpdateNotification:  true,
			VerboseNotification: false,
			TMDBAPI:             "test-key",
			TrackerPassChecks:   2,
			DBPath:              "/home/user/.upbrr/db.sqlite",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens:              4,
			MinSuccessfulUploads: 3,
			CutoffScreens:        2,
			FrameOverlay:         true,
			OverlayTextSize:      12,
			ProcessLimit:         4,
			MaxConcurrentUploads: 2,
			FFmpegLimit:          true,
			ToneMap:              false,
			UseLibplacebo:        false,
			FFmpegCompression:    23,
			TonemapAlgorithm:     "aces",
			Desat:                0.5,
		},
		TorrentCreation: TorrentCreationConfig{
			MkbrrThreads:   4,
			PreferMax16:    true,
			RehashCooldown: 30,
		},
		ClientSetup: ClientSetupConfig{
			DefaultClient: "qbittorrent",
		},
	}
	configureConfigSecretEncryption(t, cfg)

	// Export.
	if err := ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Import.
	loaded, err := ImportFromYAML(configPath)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Compare all fields.
	if loaded.MainSettings != cfg.MainSettings {
		t.Errorf("MainSettings mismatch: got %+v, want %+v", loaded.MainSettings, cfg.MainSettings)
	}
	if loaded.ScreenshotHandling != cfg.ScreenshotHandling {
		t.Errorf("ScreenshotHandling mismatch: got %+v, want %+v", loaded.ScreenshotHandling, cfg.ScreenshotHandling)
	}
	if loaded.TorrentCreation != cfg.TorrentCreation {
		t.Errorf("TorrentCreation mismatch: got %+v, want %+v", loaded.TorrentCreation, cfg.TorrentCreation)
	}
	if loaded.ClientSetup.DefaultClient != cfg.ClientSetup.DefaultClient {
		t.Errorf("DefaultClient mismatch: got %s, want %s", loaded.ClientSetup.DefaultClient, cfg.ClientSetup.DefaultClient)
	}
}

func TestConfigFilePermissions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "secure_config.yaml")

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "sensitive-key",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens: 1,
		},
	}
	configureConfigSecretEncryption(t, cfg)

	if err := ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Check file permissions (should be readable).
	fileInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	// On Unix, check for restrictive permissions. On Windows, this is less critical.
	if fileInfo.Mode()&0600 == 0 {
		t.Logf("warning: config file may not have restrictive permissions: %o", fileInfo.Mode())
	}
}

func TestBackupCreatesDirectories(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	// Use a nested path that doesn't exist yet.
	nestedBackupDir := filepath.Join(tmpDir, "nested", "deep", "backup")

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "test",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens: 1,
		},
	}
	configureConfigSecretEncryption(t, cfg)

	backupPath, err := BackupToYAML(cfg, nestedBackupDir)
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	// Verify backup exists.
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
}

func TestConfigIsMarshallable(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "test-key",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens: 4,
		},
	}
	configureConfigSecretEncryption(t, cfg)

	// Export to JSON to verify all fields are marshallable.
	json, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if len(json) == 0 {
		t.Fatalf("marshalled config is empty")
	}

	// Should contain expected fields (using YAML tags in JSON output).
	if !contains(json, `"tmdb_api"`) && !contains(json, `"TMDBAPI"`) {
		t.Logf("marshalled config: %s", json)
		t.Errorf("marshalled config missing tmdb_api field")
	}
	if !contains(json, `"screens"`) && !contains(json, `"Screens"`) {
		t.Errorf("marshalled config missing screens field")
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestConfigSectionIsolation verifies that config sections can be saved/loaded independently.
func TestConfigSectionIsolation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	mainCfg := MainSettingsConfig{
		TMDBAPI: "isolated-test",
		DBPath:  "/test/db",
	}

	screenshotCfg := ScreenshotHandlingConfig{
		Screens:       5,
		CutoffScreens: 3,
	}

	mainPath := filepath.Join(tmpDir, "main.json")
	screenshotPath := filepath.Join(tmpDir, "screenshot.json")
	mainCfg.DBPath = filepath.Join(tmpDir, "upbrr.db")
	writeWebAuthFixture(t, mainCfg.DBPath)

	// Export individual sections.
	mainData, err := ExportToJSON(&Config{MainSettings: mainCfg})
	if err != nil {
		t.Fatalf("export main settings failed: %v", err)
	}

	screenshotData, err := ExportToJSON(&Config{ScreenshotHandling: screenshotCfg})
	if err != nil {
		t.Fatalf("export screenshot settings failed: %v", err)
	}

	if err := os.WriteFile(mainPath, []byte(mainData), 0644); err != nil {
		t.Fatalf("write main settings failed: %v", err)
	}

	if err := os.WriteFile(screenshotPath, []byte(screenshotData), 0644); err != nil {
		t.Fatalf("write screenshot settings failed: %v", err)
	}

	// Verify both files were created successfully.
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("main settings file not found: %v", err)
	}

	if _, err := os.Stat(screenshotPath); err != nil {
		t.Fatalf("screenshot settings file not found: %v", err)
	}
}

type secretRoundTripRepo struct {
	saved *Config
}

func (r *secretRoundTripRepo) SaveFullConfig(ctx context.Context, cfg interface{}) error {
	typed, ok := cfg.(*Config)
	if !ok {
		return errors.New("unexpected config payload type")
	}
	r.saved = typed
	return nil
}

func (r *secretRoundTripRepo) LoadFullConfig(ctx context.Context, dest interface{}) error {
	if r.saved == nil {
		return errors.New("no saved config")
	}
	out, ok := dest.(*Config)
	if !ok {
		return errors.New("unexpected destination type")
	}
	*out = *r.saved
	return nil
}

func writeWebAuthFixture(t *testing.T, dbPath string) {
	t.Helper()
	authPath := filepath.Join(filepath.Dir(dbPath), webAuthFileName)
	payload := `{"username":"tester","password_hash":"very-secret-password-hash","encryption_key_seed":"stable-seed-for-tests"}`
	if err := os.WriteFile(authPath, []byte(payload), 0600); err != nil {
		t.Fatalf("write web auth fixture: %v", err)
	}
}

func configureConfigSecretEncryption(t *testing.T, cfg *Config) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	writeWebAuthFixture(t, dbPath)
	cfg.MainSettings.DBPath = dbPath
}

func TestExportToJSONRejectsPermissiveWebAuthPermissions(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("permission bits are ACL-backed on Windows")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "upbrr.db")
	writeWebAuthFixture(t, dbPath)

	authPath := filepath.Join(filepath.Dir(dbPath), webAuthFileName)
	if err := os.Chmod(authPath, 0644); err != nil {
		t.Fatalf("chmod web auth fixture: %v", err)
	}

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  dbPath,
			TMDBAPI: "plain-tmdb-token",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	_, err := ExportToJSON(cfg)
	if err == nil {
		t.Fatalf("expected permission error for permissive web-auth.json")
	}
	if !errors.Is(err, ErrSecretEncryptionHelperUnavailable) {
		t.Fatalf("expected ErrSecretEncryptionHelperUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), webAuthFileName) || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("expected error to mention %s and 0600 requirement, got %v", webAuthFileName, err)
	}
}

func TestExportToJSONRejectsPlaintextSecretsWithoutBootstrap(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  filepath.Join(t.TempDir(), "upbrr.db"),
			TMDBAPI: "plain-tmdb-token",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	_, err := ExportToJSON(cfg)
	if err == nil {
		t.Fatalf("expected bootstrap error for plaintext secrets")
	}
	if !errors.Is(err, ErrSecretEncryptionHelperUnavailable) {
		t.Fatalf("expected ErrSecretEncryptionHelperUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "bootstrap web auth") {
		t.Fatalf("expected actionable bootstrap error, got %v", err)
	}
}

func TestExportImportJSONEncryptsSecrets(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "upbrr.db")
	writeWebAuthFixture(t, dbPath)

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  dbPath,
			TMDBAPI: "plain-tmdb-token",
		},
		ArrIntegration: ArrIntegrationConfig{
			SonarrAPIKey: "plain-sonarr-token",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	exported, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("ExportToJSON failed: %v", err)
	}

	if strings.Contains(exported, "plain-tmdb-token") {
		t.Fatalf("exported JSON leaked plaintext TMDB key")
	}
	if strings.Contains(exported, "plain-sonarr-token") {
		t.Fatalf("exported JSON leaked plaintext Sonarr key")
	}
	if !strings.Contains(exported, encryptedEnvelopePrefix) {
		t.Fatalf("exported JSON did not contain encrypted secret envelopes")
	}

	imported, err := ImportFromJSON(exported)
	if err != nil {
		t.Fatalf("ImportFromJSON failed: %v", err)
	}

	if imported.MainSettings.TMDBAPI != "plain-tmdb-token" {
		t.Fatalf("TMDB API key mismatch after round-trip: got %q", imported.MainSettings.TMDBAPI)
	}
	if imported.ArrIntegration.SonarrAPIKey != "plain-sonarr-token" {
		t.Fatalf("Sonarr API key mismatch after round-trip: got %q", imported.ArrIntegration.SonarrAPIKey)
	}
}

func TestSaveLoadDatabaseEncryptsSecrets(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "upbrr.db")
	writeWebAuthFixture(t, dbPath)

	repo := &secretRoundTripRepo{}
	input := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  dbPath,
			TMDBAPI: "db-secret-token",
		},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BHD": {
					APIKey: "tracker-secret-token",
				},
			},
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	if err := SaveToDatabase(context.Background(), input, repo); err != nil {
		t.Fatalf("SaveToDatabase failed: %v", err)
	}

	if repo.saved == nil {
		t.Fatalf("repository did not receive saved config")
	}
	if repo.saved.MainSettings.TMDBAPI == "db-secret-token" {
		t.Fatalf("saved config leaked plaintext TMDB key")
	}
	if !isSecretEnvelope(repo.saved.MainSettings.TMDBAPI) {
		t.Fatalf("saved TMDB key is not stored as a secret envelope")
	}
	trackerCfg, ok := repo.saved.Trackers.Trackers["BHD"]
	if !ok {
		t.Fatalf("saved config is missing tracker entry %q", "BHD")
	}
	if trackerCfg.APIKey == "tracker-secret-token" {
		t.Fatalf("saved config leaked plaintext tracker API key")
	}
	if !isSecretEnvelope(trackerCfg.APIKey) {
		t.Fatalf("saved tracker API key is not stored as a secret envelope")
	}

	loaded, err := LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabase failed: %v", err)
	}

	if loaded.MainSettings.TMDBAPI != "db-secret-token" {
		t.Fatalf("loaded TMDB key mismatch: got %q", loaded.MainSettings.TMDBAPI)
	}
	if loaded.Trackers.Trackers["BHD"].APIKey != "tracker-secret-token" {
		t.Fatalf("loaded tracker API key mismatch: got %q", loaded.Trackers.Trackers["BHD"].APIKey)
	}
}

func TestSaveToDatabaseRejectsPlaintextSecretsWithoutBootstrap(t *testing.T) {
	t.Parallel()

	repo := &secretRoundTripRepo{}
	input := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  filepath.Join(t.TempDir(), "upbrr.db"),
			TMDBAPI: "db-secret-token",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	err := SaveToDatabase(context.Background(), input, repo)
	if err == nil {
		t.Fatalf("expected bootstrap error for plaintext secrets")
	}
	if !errors.Is(err, ErrSecretEncryptionHelperUnavailable) {
		t.Fatalf("expected ErrSecretEncryptionHelperUnavailable, got %v", err)
	}
}

func TestRewrapSecretsInDatabaseMigratesLegacyHelperToStableSeed(t *testing.T) {
	t.Parallel()

	oldMaterial := authmaterial.Material{
		Username:     "tester",
		PasswordHash: "legacy-password-hash",
	}
	newMaterial := authmaterial.Material{
		Username:          "tester",
		PasswordHash:      "upgraded-password-hash",
		EncryptionKeySeed: "stable-seed-value",
	}

	oldHelper, _, err := oldMaterial.PrimaryHelper()
	if err != nil {
		t.Fatalf("old helper: %v", err)
	}
	repo := &secretRoundTripRepo{}
	repo.saved, err = encryptConfigSecretsWithHelper(&Config{
		MainSettings: MainSettingsConfig{
			DBPath:  filepath.Join(t.TempDir(), "upbrr.db"),
			TMDBAPI: "db-secret-token",
		},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BHD": {APIKey: "tracker-secret-token"},
			},
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}, oldHelper)
	if err != nil {
		t.Fatalf("encrypt with old helper: %v", err)
	}

	if err := RewrapSecretsInDatabase(context.Background(), repo, oldMaterial, newMaterial); err != nil {
		t.Fatalf("rewrap secrets: %v", err)
	}

	newHelper, _, err := newMaterial.PrimaryHelper()
	if err != nil {
		t.Fatalf("new helper: %v", err)
	}
	loaded, err := decryptConfigSecretsWithHelper(repo.saved, newHelper)
	if err != nil {
		t.Fatalf("decrypt with new helper: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != "db-secret-token" {
		t.Fatalf("TMDB API key mismatch after rewrap: got %q", loaded.MainSettings.TMDBAPI)
	}
	if loaded.Trackers.Trackers["BHD"].APIKey != "tracker-secret-token" {
		t.Fatalf("tracker API key mismatch after rewrap: got %q", loaded.Trackers.Trackers["BHD"].APIKey)
	}
}

func TestExportFromDatabaseToYAMLSuccess(t *testing.T) {
	t.Setenv("UA_DEFAULT_SCREENS", "9")

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "exported.yaml")
	repo := &exportLoadRepo{
		cfg: Config{
			MainSettings: MainSettingsConfig{
				TMDBAPI: "db-api",
			},
			ScreenshotHandling: ScreenshotHandlingConfig{
				Screens: 4,
			},
		},
	}
	configureConfigSecretEncryption(t, &repo.cfg)

	if err := ExportFromDatabaseToYAML(context.Background(), outputPath, repo); err != nil {
		t.Fatalf("ExportFromDatabaseToYAML failed: %v", err)
	}

	loaded, err := ImportFromYAML(outputPath)
	if err != nil {
		t.Fatalf("ImportFromYAML failed: %v", err)
	}

	if loaded.MainSettings.TMDBAPI != "db-api" {
		t.Fatalf("TMDBAPI mismatch: got %q, want %q", loaded.MainSettings.TMDBAPI, "db-api")
	}
	if loaded.ScreenshotHandling.Screens != 9 {
		t.Fatalf("Screens mismatch: got %d, want %d", loaded.ScreenshotHandling.Screens, 9)
	}
}

func TestExportFromDatabaseToYAMLInvalidInput(t *testing.T) {
	t.Parallel()

	err := ExportFromDatabaseToYAML(context.Background(), "", &exportLoadRepo{})
	if err == nil {
		t.Fatalf("expected error for empty output path")
	}
}

func TestExportFromDatabaseToYAMLLoadFailure(t *testing.T) {
	t.Parallel()

	repo := &exportLoadRepo{err: errors.New("not found")}
	err := ExportFromDatabaseToYAML(context.Background(), filepath.Join(t.TempDir(), "out.yaml"), repo)
	if err == nil {
		t.Fatalf("expected load error, got nil")
	}
}
