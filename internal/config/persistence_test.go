// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

type exportLoadRepo struct {
	cfg Config
	err error
}

func (r *exportLoadRepo) LoadFullConfig(_ context.Context, dest any) error {
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
	loaded, err := ImportFromJSONEncrypted(json)
	if err != nil {
		t.Fatalf("ImportFromJSONEncrypted failed: %v", err)
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

func TestExportPlaintextJSONIncludesSecrets(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "test-api-key",
			DBPath:  "/test/db",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	exported, err := ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("ExportToPlaintextJSON failed: %v", err)
	}
	if !strings.Contains(exported, "test-api-key") {
		t.Fatalf("expected plaintext secret in JSON export, got %s", exported)
	}
	if strings.Contains(exported, encryptedEnvelopePrefix) {
		t.Fatalf("expected plaintext JSON export without encrypted envelopes, got %s", exported)
	}
}

func TestExportPlaintextYAMLIncludesSecrets(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "test-api-key",
			DBPath:  "/test/db",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	if err := ExportToPlaintextYAML(cfg, configPath); err != nil {
		t.Fatalf("ExportToPlaintextYAML failed: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read plaintext YAML export: %v", err)
	}
	exported := string(raw)
	if !strings.Contains(exported, "test-api-key") {
		t.Fatalf("expected plaintext secret in YAML export, got %s", exported)
	}
	if strings.Contains(exported, encryptedEnvelopePrefix) {
		t.Fatalf("expected plaintext YAML export without encrypted envelopes, got %s", exported)
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

type jsonRoundTripRepo struct {
	saved *Config
}

type rawConfigRepo struct {
	raw map[string]any
}

type fallbackConfigRepo struct {
	cfg Config
}

type sectionCaptureRepo struct {
	saved []string
	err   error
}

type sectionBatchPreserveRepo struct {
	raw map[string]any
}

func (r *secretRoundTripRepo) SaveFullConfig(_ context.Context, cfg any) error {
	typed, ok := cfg.(*Config)
	if !ok {
		return errors.New("unexpected config payload type")
	}
	r.saved = typed
	return nil
}

func (r *secretRoundTripRepo) LoadFullConfig(_ context.Context, dest any) error {
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

func (r *jsonRoundTripRepo) SaveFullConfig(_ context.Context, cfg any) error {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal saved config: %w", err)
	}
	var saved Config
	if err := json.Unmarshal(payload, &saved); err != nil {
		return fmt.Errorf("unmarshal saved config: %w", err)
	}
	r.saved = &saved
	return nil
}

func (r *jsonRoundTripRepo) LoadFullConfig(_ context.Context, dest any) error {
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

func (r *rawConfigRepo) LoadFullConfig(_ context.Context, dest any) error {
	payload, err := json.Marshal(r.raw)
	if err != nil {
		return fmt.Errorf("marshal raw config: %w", err)
	}
	if err := json.Unmarshal(payload, dest); err != nil {
		return fmt.Errorf("unmarshal raw config: %w", err)
	}
	return nil
}

func (r *fallbackConfigRepo) LoadFullConfig(_ context.Context, dest any) error {
	switch out := dest.(type) {
	case *map[string]any:
		return errors.New("raw map load failed")
	case *Config:
		*out = r.cfg
		return nil
	default:
		return errors.New("unexpected destination type")
	}
}

func (r *sectionCaptureRepo) SaveConfigSection(_ context.Context, section string, _ any) error {
	if r.err != nil {
		return r.err
	}
	r.saved = append(r.saved, section)
	return nil
}

func (r *sectionBatchPreserveRepo) LoadFullConfig(_ context.Context, dest any) error {
	payload, err := json.Marshal(r.raw)
	if err != nil {
		return fmt.Errorf("marshal raw config: %w", err)
	}
	if err := json.Unmarshal(payload, dest); err != nil {
		return fmt.Errorf("unmarshal raw config: %w", err)
	}
	return nil
}

func (r *sectionBatchPreserveRepo) SaveConfigSection(ctx context.Context, section string, data any) error {
	return r.SaveConfigSections(ctx, map[string]any{section: data})
}

func (r *sectionBatchPreserveRepo) SaveConfigSections(_ context.Context, sections map[string]any) error {
	if r.raw == nil {
		r.raw = map[string]any{}
	}
	for section, data := range sections {
		payload, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal section %s: %w", section, err)
		}
		var stored any
		if err := json.Unmarshal(payload, &stored); err != nil {
			return fmt.Errorf("unmarshal section %s: %w", section, err)
		}
		r.raw[section] = stored
	}
	return nil
}

func defaultDatabaseConfigMap(t *testing.T) map[string]any {
	t.Helper()

	defaults, err := LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("LoadEmbeddedDefaultConfig failed: %v", err)
	}
	defaults.TorrentClients = map[string]TorrentClientConfig{}

	raw, err := configJSONMap(defaults)
	if err != nil {
		t.Fatalf("configJSONMap failed: %v", err)
	}
	return raw
}

func writeWebAuthFixture(t *testing.T, dbPath string) {
	t.Helper()
	writeWebAuthFixtureWithSeed(t, dbPath, "stable-seed-for-tests")
}

func writeWebAuthFixtureWithSeed(t *testing.T, dbPath string, seed string) {
	t.Helper()
	authPath := filepath.Join(filepath.Dir(dbPath), webAuthFileName)
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatalf("mkdir web auth fixture: %v", err)
	}
	payload := fmt.Sprintf(`{"username":"tester","password_hash":"very-secret-password-hash","encryption_key_seed":%q}`, seed)
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

func TestLoadFromDatabaseWithDefaultBackfillReportsMissingZeroDefaultKey(t *testing.T) {
	t.Parallel()

	raw := defaultDatabaseConfigMap(t)
	torrentCreation, ok := raw["TorrentCreation"].(map[string]any)
	if !ok {
		t.Fatalf("expected TorrentCreation defaults map")
	}
	delete(torrentCreation, "RehashCooldown")

	loaded, backfilledDefaults, err := LoadFromDatabaseWithDefaultBackfill(context.Background(), &rawConfigRepo{raw: raw})
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithDefaultBackfill failed: %v", err)
	}
	if !backfilledDefaults {
		t.Fatalf("expected missing zero-value default key to report backfilled defaults")
	}
	if loaded.TorrentCreation.RehashCooldown != 0 {
		t.Fatalf("expected zero default rehash cooldown, got %d", loaded.TorrentCreation.RehashCooldown)
	}
}

func TestLoadFromDatabaseWithDefaultBackfillIgnoresExplicitZeroDefaultKey(t *testing.T) {
	t.Parallel()

	raw := defaultDatabaseConfigMap(t)
	torrentCreation, ok := raw["TorrentCreation"].(map[string]any)
	if !ok {
		t.Fatalf("expected TorrentCreation defaults map")
	}
	torrentCreation["RehashCooldown"] = float64(0)

	_, backfilledDefaults, err := LoadFromDatabaseWithDefaultBackfill(context.Background(), &rawConfigRepo{raw: raw})
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithDefaultBackfill failed: %v", err)
	}
	if backfilledDefaults {
		t.Fatalf("expected explicit zero-value key to avoid default backfill")
	}
}

func TestLoadFromDatabaseWithRepairReportDoesNotPersistInvalidObjectRoots(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]any{
		"null":   nil,
		"scalar": "not-an-object",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := defaultDatabaseConfigMap(t)
			raw["Trackers"] = value

			loaded, report, err := LoadFromDatabaseWithRepairReport(context.Background(), &rawConfigRepo{raw: raw})
			if err != nil {
				t.Fatalf("LoadFromDatabaseWithRepairReport failed: %v", err)
			}
			if loaded.Trackers.Trackers == nil {
				t.Fatalf("expected runtime config to retain default Trackers object")
			}
			if !report.BackfilledDefaults {
				t.Fatalf("expected invalid object to report repaired defaults")
			}
			if !slices.Contains(report.InvalidPaths, "Trackers") {
				t.Fatalf("expected invalid Trackers path, got %#v", report.InvalidPaths)
			}
			if slices.Contains(report.ChangedSections, "Trackers") {
				t.Fatalf("expected invalid object root not to be scheduled for persistence, got %#v", report.ChangedSections)
			}
		})
	}
}

func TestLoadFromDatabaseWithRepairReportFallbackExposesRepairContext(t *testing.T) {
	t.Parallel()

	cfg := Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "fallback-token"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 3},
	}
	loaded, report, err := LoadFromDatabaseWithRepairReport(context.Background(), &fallbackConfigRepo{cfg: cfg})
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithRepairReport failed: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != "fallback-token" {
		t.Fatalf("expected struct fallback config to load, got %q", loaded.MainSettings.TMDBAPI)
	}
	if !report.BackfilledDefaults {
		t.Fatalf("expected fallback report to expose repair context")
	}
	if !slices.Contains(report.InvalidPaths, "raw-config") {
		t.Fatalf("expected raw-config diagnostic, got %#v", report.InvalidPaths)
	}
	if len(report.ChangedSections) != 0 {
		t.Fatalf("expected fallback compatibility without section persistence, got %#v", report.ChangedSections)
	}
}

func TestSaveSectionsToDatabasePrevalidatesSectionNamesBeforeWrites(t *testing.T) {
	t.Parallel()

	repo := &sectionCaptureRepo{}
	cfg := validMinimalConfig()

	err := SaveSectionsToDatabase(context.Background(), cfg, []string{"Logging", "unknown_section"}, repo)
	if err == nil {
		t.Fatalf("expected unknown section error")
	}
	if len(repo.saved) != 0 {
		t.Fatalf("expected no sections saved before unknown section error, got %#v", repo.saved)
	}
}

func TestSaveSectionsToDatabaseMapsYAMLRootAliases(t *testing.T) {
	t.Parallel()

	repo := &sectionCaptureRepo{}
	cfg := validMinimalConfig()

	if err := SaveSectionsToDatabase(context.Background(), cfg, []string{" main_settings ", "MainSettings"}, repo); err != nil {
		t.Fatalf("SaveSectionsToDatabase failed: %v", err)
	}
	if got, want := repo.saved, []string{"MainSettings"}; !slices.Equal(got, want) {
		t.Fatalf("saved sections mismatch: got %#v want %#v", got, want)
	}
}

func TestSaveSectionsToDatabaseIgnoresUnrelatedEncryptedSecrets(t *testing.T) {
	t.Parallel()

	repo := &sectionCaptureRepo{}
	cfg := validMinimalConfig()
	cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "upbrr.db")
	cfg.MainSettings.TMDBAPI = encryptedEnvelopePrefix + "opaque"

	if err := SaveSectionsToDatabase(context.Background(), cfg, []string{"Logging"}, repo); err != nil {
		t.Fatalf("SaveSectionsToDatabase failed for unrelated encrypted secret: %v", err)
	}
	if got, want := repo.saved, []string{"Logging"}; !slices.Equal(got, want) {
		t.Fatalf("saved sections mismatch: got %#v want %#v", got, want)
	}
}

func TestSaveSectionsToDatabaseRequiresHelperForSelectedEncryptedSecret(t *testing.T) {
	t.Parallel()

	repo := &sectionCaptureRepo{}
	cfg := validMinimalConfig()
	cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "upbrr.db")
	cfg.MainSettings.TMDBAPI = encryptedEnvelopePrefix + "opaque"

	err := SaveSectionsToDatabase(context.Background(), cfg, []string{"MainSettings"}, repo)
	if !errors.Is(err, ErrSecretEncryptionHelperUnavailable) {
		t.Fatalf("expected helper unavailable error, got %v", err)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("expected no sections saved after selected encrypted secret failure, got %#v", repo.saved)
	}
}

func TestSaveSectionsToDatabaseRejectsMultiSectionFallbackBeforeWrites(t *testing.T) {
	t.Parallel()

	repo := &sectionCaptureRepo{}
	cfg := validMinimalConfig()

	err := SaveSectionsToDatabase(context.Background(), cfg, []string{"Logging", "PostUpload"}, repo)
	if err == nil {
		t.Fatal("expected multi-section fallback error")
	}
	if !strings.Contains(err.Error(), "requires batch section save") {
		t.Fatalf("expected batch requirement error, got %v", err)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("expected no fallback sections saved, got %#v", repo.saved)
	}
}

func TestSaveSectionsToDatabasePreservesStoredUnknownSameRootFields(t *testing.T) {
	t.Parallel()

	raw := defaultDatabaseConfigMap(t)
	postUpload, ok := raw["PostUpload"].(map[string]any)
	if !ok {
		t.Fatalf("expected PostUpload defaults map")
	}
	postUpload["FutureOption"] = map[string]any{"Nested": "keep"}
	delete(postUpload, "MaxConcurrentTrackers")
	repo := &sectionBatchPreserveRepo{raw: raw}

	loaded, report, err := LoadFromDatabaseWithRepairReport(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithRepairReport failed: %v", err)
	}
	if !slices.Contains(report.ChangedSections, "PostUpload") {
		t.Fatalf("expected PostUpload repair section, got %#v", report.ChangedSections)
	}
	if err := SaveSectionsToDatabase(context.Background(), loaded, report.ChangedSections, repo); err != nil {
		t.Fatalf("SaveSectionsToDatabase failed: %v", err)
	}

	persisted, ok := repo.raw["PostUpload"].(map[string]any)
	if !ok {
		t.Fatalf("expected persisted PostUpload map")
	}
	future, ok := persisted["FutureOption"].(map[string]any)
	if !ok || future["Nested"] != "keep" {
		t.Fatalf("expected unknown same-root field to survive, got %#v", persisted["FutureOption"])
	}
	if got, ok := persisted["MaxConcurrentTrackers"].(float64); !ok || got != 4 {
		t.Fatalf("expected repaired max concurrent tracker uploads, got %#v", persisted["MaxConcurrentTrackers"])
	}
}

func TestSaveSectionsToDatabaseDoesNotPreserveRepairedTrackerAliases(t *testing.T) {
	t.Parallel()

	raw := defaultDatabaseConfigMap(t)
	trackerSection, ok := raw["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("expected Trackers defaults map")
	}
	trackers, ok := trackerSection["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("expected Trackers.Trackers defaults map")
	}
	delete(trackers, "BTN")
	trackers["btn"] = map[string]any{
		"apikey":     "stored-token",
		"FutureFlag": true,
	}
	repo := &sectionBatchPreserveRepo{raw: raw}

	loaded, report, err := LoadFromDatabaseWithRepairReport(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithRepairReport failed: %v", err)
	}
	if !slices.Contains(report.ChangedSections, "Trackers") {
		t.Fatalf("expected Trackers repair section, got %#v", report.ChangedSections)
	}
	if err := SaveSectionsToDatabase(context.Background(), loaded, report.ChangedSections, repo); err != nil {
		t.Fatalf("SaveSectionsToDatabase failed: %v", err)
	}

	persistedTrackers, ok := repo.raw["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("expected persisted Trackers map")
	}
	persistedEntries, ok := persistedTrackers["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("expected persisted Trackers.Trackers map")
	}
	if _, ok := persistedEntries["btn"]; ok {
		t.Fatalf("expected folded tracker alias not to be preserved, got %#v", persistedEntries["btn"])
	}
	btn, ok := persistedEntries["BTN"].(map[string]any)
	if !ok {
		t.Fatalf("expected canonical BTN entry, got %#v", persistedEntries["BTN"])
	}
	if _, ok := btn["apikey"]; ok {
		t.Fatalf("expected folded tracker field alias not to be preserved, got %#v", btn["apikey"])
	}
	if got := btn["APIKey"]; got != "stored-token" {
		t.Fatalf("expected canonical APIKey to keep stored value, got %#v", got)
	}
	if got := btn["FutureFlag"]; got != true {
		t.Fatalf("expected distinct unknown tracker field to be preserved, got %#v", got)
	}
}

// TestLoadFromDatabaseMergesCaseInsensitiveStoredTrackerName verifies stored
// tracker names fold into canonical defaults and still report backfilled keys.
func TestLoadFromDatabaseMergesCaseInsensitiveStoredTrackerName(t *testing.T) {
	t.Parallel()

	raw := defaultDatabaseConfigMap(t)
	trackerSection, ok := raw["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("expected Trackers defaults map")
	}
	trackers, ok := trackerSection["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("expected Trackers.Trackers defaults map")
	}
	delete(trackers, "BTN")
	trackers["btn"] = map[string]any{
		"APIKey": "stored-token",
		"URL":    "https://stored.btn.example",
	}

	loaded, backfilledDefaults, err := LoadFromDatabaseWithDefaultBackfill(context.Background(), &rawConfigRepo{raw: raw})
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithDefaultBackfill failed: %v", err)
	}
	if !backfilledDefaults {
		t.Fatalf("expected case-normalized tracker entry to report backfilled defaults")
	}
	if _, ok := loaded.Trackers.Trackers["btn"]; ok {
		t.Fatalf("expected lowercase stored tracker to merge into canonical BTN entry")
	}
	btn := loaded.Trackers.Trackers["BTN"]
	if btn.APIKey != "stored-token" {
		t.Fatalf("expected stored BTN API key, got %q", btn.APIKey)
	}
	if btn.URL != "https://stored.btn.example" {
		t.Fatalf("expected stored BTN URL, got %q", btn.URL)
	}
}

func TestMergeStoredDynamicConfigValueSkipsNonObjectCaseFoldedTrackerEntry(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"BTN": map[string]any{
			"APIKey": "",
		},
	}

	report, err := mergeStoredDynamicConfigValue(base, "btn", "not-an-object", "Trackers.Trackers")
	if err != nil {
		t.Fatalf("merge stored dynamic config value: %v", err)
	}

	btn, ok := base["BTN"].(map[string]any)
	if !ok {
		t.Fatalf("expected canonical BTN entry to remain an object, got %T", base["BTN"])
	}
	if got := btn["APIKey"]; got != "" {
		t.Fatalf("expected canonical BTN entry to remain unchanged, got APIKey %#v", got)
	}
	if changed := report.changedRootSections(); len(changed) != 0 {
		t.Fatalf("expected skipped scalar entry not to mark changes, got %#v", changed)
	}
}

func TestMergeStoredConfigMapSkipsNonObjectDynamicEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		key          string
		overlayValue any
	}{
		{
			name:         "tracker entry scalar",
			path:         "Trackers.Trackers",
			key:          "NEW",
			overlayValue: "not-an-object",
		},
		{
			name:         "torrent client array",
			path:         "TorrentClients",
			key:          "client",
			overlayValue: []any{"not-an-object"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			base := map[string]any{}
			overlay := map[string]any{
				test.key: test.overlayValue,
			}

			report, err := mergeStoredConfigMapWithReport(base, overlay, test.path)
			if err != nil {
				t.Fatalf("merge stored config map: %v", err)
			}
			if _, ok := base[test.key]; ok {
				t.Fatalf("expected non-object dynamic entry %q not to be inserted", test.key)
			}
			if changed := report.changedRootSections(); len(changed) != 0 {
				t.Fatalf("expected skipped dynamic entry not to mark changes, got %#v", changed)
			}
		})
	}
}

// TestMergeStoredConfigMapMergesCaseInsensitiveStoredTrackerField verifies
// case-normalized tracker fields preserve the missing canonical default path.
func TestMergeStoredConfigMapMergesCaseInsensitiveStoredTrackerField(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"APIKey": "",
		"URL":    "https://default.example",
	}
	overlay := map[string]any{
		"apikey": "stored-token",
	}

	report, err := mergeStoredConfigMapWithReport(base, overlay, "Trackers.Trackers.BTN")
	if err != nil {
		t.Fatalf("merge stored config map: %v", err)
	}
	missingDefaultPaths := report.missingDefaultPaths

	if _, ok := base["apikey"]; ok {
		t.Fatalf("expected lowercase tracker field to merge into canonical APIKey")
	}
	if got := base["APIKey"]; got != "stored-token" {
		t.Fatalf("expected stored API key on canonical field, got %#v", got)
	}
	if !slices.Contains(missingDefaultPaths, "Trackers.Trackers.BTN.APIKey") {
		t.Fatalf("expected case-normalized tracker field to report missing canonical default, got %#v", missingDefaultPaths)
	}
}

// TestMergeStoredConfigMapRejectsDuplicateTrackerCaseVariants verifies duplicate
// tracker names differing only by case fold are not resolved by lexical order.
func TestMergeStoredConfigMapRejectsDuplicateTrackerCaseVariants(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"BTN": map[string]any{
			"APIKey": "",
		},
	}
	overlay := map[string]any{
		"btn": map[string]any{
			"apikey": "lower-token",
		},
		"BTN": map[string]any{
			"APIKey": "upper-token",
		},
	}

	if _, err := mergeStoredConfigMapWithReport(base, overlay, "Trackers.Trackers"); err == nil {
		t.Fatalf("expected duplicate case-folded tracker keys to fail")
	}
}

func TestMergeStoredConfigMapRejectsAmbiguousTrackerField(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"APIKey": "",
		"ApiKey": "",
	}
	overlay := map[string]any{
		"apikey": "stored-token",
	}

	if _, err := mergeStoredConfigMapWithReport(base, overlay, "Trackers.Trackers.BTN"); err == nil {
		t.Fatalf("expected ambiguous tracker field to fail")
	}
}

func TestMergeStoredConfigMapDoesNotUnicodeFoldTrackerField(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"APIKey": "",
	}
	overlay := map[string]any{
		"API\u212Aey": "stored-token",
	}

	if _, err := mergeStoredConfigMapWithReport(base, overlay, "Trackers.Trackers.BTN"); err != nil {
		t.Fatalf("merge stored config map: %v", err)
	}
	if got := base["APIKey"]; got != "" {
		t.Fatalf("expected unicode folded field not to overwrite APIKey, got %#v", got)
	}
	if got := base["API\u212Aey"]; got != "stored-token" {
		t.Fatalf("expected unicode field to remain distinct, got %#v", got)
	}
}

func TestExportToJSONFallsBackToPlaintextWithPermissiveWebAuthPermissions(t *testing.T) {
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

	exported, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("expected plaintext fallback, got %v", err)
	}
	if !strings.Contains(exported, "plain-tmdb-token") {
		t.Fatalf("expected plaintext secret when auth helper is unusable, got %s", exported)
	}
}

func TestExportImportJSONPreservesWatchTorrentClientFields(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "test-api-key"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		TorrentClients: map[string]TorrentClientConfig{
			"watch": {
				Type:        "watch",
				WatchFolder: `D:\Watch`,
				StorageDir:  `D:\Storage`,
			},
		},
	}

	exported, err := ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("ExportToPlaintextJSON failed: %v", err)
	}

	imported, err := ImportFromJSON(exported)
	if err != nil {
		t.Fatalf("ImportFromJSON failed: %v", err)
	}

	watch := imported.TorrentClients["watch"]
	if watch.ClientType() != "watch" {
		t.Fatalf("expected watch client type after roundtrip, got %q", watch.ClientType())
	}
	if watch.WatchFolder != `D:\Watch` {
		t.Fatalf("expected watch folder to roundtrip, got %q", watch.WatchFolder)
	}
	if watch.StorageDir != `D:\Storage` {
		t.Fatalf("expected storage dir to roundtrip, got %q", watch.StorageDir)
	}
}

func TestSaveLoadDatabaseRoundTripPreservesWatchTorrentClient(t *testing.T) {
	t.Parallel()

	repo := &jsonRoundTripRepo{}
	input := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "db-secret-token"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		TorrentClients: map[string]TorrentClientConfig{
			"watch": {
				Type:        "watch",
				WatchFolder: `D:\Watch`,
				StorageDir:  `D:\Storage`,
			},
		},
	}
	configureConfigSecretEncryption(t, input)

	if err := SaveToDatabase(context.Background(), input, repo); err != nil {
		t.Fatalf("SaveToDatabase failed: %v", err)
	}

	loaded, err := LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabase failed: %v", err)
	}

	watch := loaded.TorrentClients["watch"]
	if watch.ClientType() != "watch" {
		t.Fatalf("expected watch client type after database roundtrip, got %q", watch.ClientType())
	}
	if watch.WatchFolder != `D:\Watch` {
		t.Fatalf("expected watch folder to roundtrip, got %q", watch.WatchFolder)
	}
	if watch.StorageDir != `D:\Storage` {
		t.Fatalf("expected storage dir to roundtrip, got %q", watch.StorageDir)
	}
}

func TestExportToJSONFallsBackToPlaintextWithoutBootstrap(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  filepath.Join(t.TempDir(), "upbrr.db"),
			TMDBAPI: "plain-tmdb-token",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	exported, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("expected plaintext fallback, got %v", err)
	}
	if !strings.Contains(exported, "plain-tmdb-token") {
		t.Fatalf("expected plaintext secret without bootstrap, got %s", exported)
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
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BTN": {URL: "https://secret.btn.example"},
				"HDT": {URL: "https://public.hdt.example"},
			},
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
	if strings.Contains(exported, "https://secret.btn.example") {
		t.Fatalf("exported JSON leaked plaintext BTN URL")
	}
	if !strings.Contains(exported, "https://public.hdt.example") {
		t.Fatalf("exported JSON should keep non-BTN tracker URL plaintext")
	}
	if !strings.Contains(exported, encryptedEnvelopePrefix) {
		t.Fatalf("exported JSON did not contain encrypted secret envelopes")
	}

	imported, err := ImportFromJSONEncrypted(exported)
	if err != nil {
		t.Fatalf("ImportFromJSONEncrypted failed: %v", err)
	}

	if imported.MainSettings.TMDBAPI != "plain-tmdb-token" {
		t.Fatalf("TMDB API key mismatch after round-trip: got %q", imported.MainSettings.TMDBAPI)
	}
	if imported.ArrIntegration.SonarrAPIKey != "plain-sonarr-token" {
		t.Fatalf("Sonarr API key mismatch after round-trip: got %q", imported.ArrIntegration.SonarrAPIKey)
	}
	if imported.Trackers.Trackers["BTN"].URL != "https://secret.btn.example" {
		t.Fatalf("BTN URL mismatch after round-trip: got %q", imported.Trackers.Trackers["BTN"].URL)
	}
	if imported.Trackers.Trackers["HDT"].URL != "https://public.hdt.example" {
		t.Fatalf("HDT URL mismatch after round-trip: got %q", imported.Trackers.Trackers["HDT"].URL)
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

func TestSaveToDatabaseFallsBackToPlaintextWithoutBootstrap(t *testing.T) {
	t.Parallel()

	repo := &secretRoundTripRepo{}
	input := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  filepath.Join(t.TempDir(), "upbrr.db"),
			TMDBAPI: "db-secret-token",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	if err := SaveToDatabase(context.Background(), input, repo); err != nil {
		t.Fatalf("expected plaintext fallback, got %v", err)
	}
	if repo.saved == nil {
		t.Fatal("expected repository to receive saved config")
	}
	if repo.saved.MainSettings.TMDBAPI != "db-secret-token" {
		t.Fatalf("expected plaintext TMDB key to be preserved, got %q", repo.saved.MainSettings.TMDBAPI)
	}
}

func TestExportToJSONRejectsEncryptedSecretsWhenHelperUnavailable(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			DBPath:  filepath.Join(t.TempDir(), "upbrr.db"),
			TMDBAPI: encryptedEnvelopePrefix + "opaque",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}

	_, err := ExportToJSON(cfg)
	if err == nil {
		t.Fatalf("expected helper error for encrypted secrets")
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

func TestExportFromDatabaseToPlaintextYAMLSuccess(t *testing.T) {
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

	if err := ExportFromDatabaseToPlaintextYAML(context.Background(), outputPath, repo); err != nil {
		t.Fatalf("ExportFromDatabaseToPlaintextYAML failed: %v", err)
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read plaintext export: %v", err)
	}
	exported := string(raw)
	if !strings.Contains(exported, "db-api") {
		t.Fatalf("expected plaintext secret in YAML export, got %s", exported)
	}
	if strings.Contains(exported, encryptedEnvelopePrefix) {
		t.Fatalf("expected plaintext YAML export without encrypted envelopes, got %s", exported)
	}
	if !strings.Contains(exported, "screens: 9") {
		t.Fatalf("expected env override in plaintext export, got %s", exported)
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

type runtimePathRepo struct {
	dbPath string
	cfg    Config
}

func (r *runtimePathRepo) LoadFullConfig(_ context.Context, dest any) error {
	payload, err := json.Marshal(r.cfg)
	if err != nil {
		return fmt.Errorf("marshal runtime path config: %w", err)
	}
	if err := json.Unmarshal(payload, dest); err != nil {
		return fmt.Errorf("unmarshal runtime path config: %w", err)
	}
	return nil
}

func (r *runtimePathRepo) DBPath() string { return r.dbPath }

func TestLoadFromDatabaseDecryptsViaRuntimeDBDir(t *testing.T) {
	// web-auth.json (the secret-encryption helper) lives next to the *runtime*
	// database. Encrypt a secret against that helper.
	runtimeDBPath := filepath.Join(t.TempDir(), "db.sqlite")
	writeWebAuthFixture(t, runtimeDBPath)
	plain := &Config{MainSettings: MainSettingsConfig{DBPath: runtimeDBPath, TMDBAPI: "super-secret-token"}}
	encrypted, err := EncryptConfigSecrets(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted.MainSettings.TMDBAPI == "super-secret-token" {
		t.Fatalf("expected TMDBAPI to be encrypted, got plaintext")
	}

	// Simulate a stored config whose db_path points at a pre-upgrade dir that no
	// longer holds web-auth.json. Decryption must still succeed by anchoring to
	// the repo's runtime DB dir, and the stored db_path must be preserved.
	staleDBPath := filepath.Join(t.TempDir(), "old", "db.sqlite")
	encrypted.MainSettings.DBPath = staleDBPath

	repo := &runtimePathRepo{dbPath: runtimeDBPath, cfg: *encrypted}
	got, err := LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabase: %v", err)
	}
	if got.MainSettings.TMDBAPI != "super-secret-token" {
		t.Fatalf("secret not decrypted via runtime DB dir: got %q", got.MainSettings.TMDBAPI)
	}
	if got.MainSettings.DBPath != staleDBPath {
		t.Fatalf("stored db_path not preserved: got %q want %q", got.MainSettings.DBPath, staleDBPath)
	}
}

func TestLoadFromDatabaseWithRepairReportDecryptsViaRuntimeDBDir(t *testing.T) {
	runtimeDBPath := filepath.Join(t.TempDir(), "db.sqlite")
	writeWebAuthFixture(t, runtimeDBPath)
	plain := &Config{MainSettings: MainSettingsConfig{DBPath: runtimeDBPath, TMDBAPI: "super-secret-token"}}
	encrypted, err := EncryptConfigSecrets(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	staleDBPath := filepath.Join(t.TempDir(), "old", "db.sqlite")
	encrypted.MainSettings.DBPath = staleDBPath

	repo := &runtimePathRepo{dbPath: runtimeDBPath, cfg: *encrypted}
	got, _, err := LoadFromDatabaseWithRepairReport(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabaseWithRepairReport: %v", err)
	}
	if got.MainSettings.TMDBAPI != "super-secret-token" {
		t.Fatalf("secret not decrypted via runtime DB dir: got %q", got.MainSettings.TMDBAPI)
	}
	if got.MainSettings.DBPath != staleDBPath {
		t.Fatalf("stored db_path not preserved: got %q want %q", got.MainSettings.DBPath, staleDBPath)
	}
}

func TestLoadFromDatabaseRetriesRuntimeDBDirAfterWrongStoredHelper(t *testing.T) {
	runtimeDBPath := filepath.Join(t.TempDir(), "runtime", "db.sqlite")
	writeWebAuthFixtureWithSeed(t, runtimeDBPath, "runtime-seed")
	plain := &Config{MainSettings: MainSettingsConfig{DBPath: runtimeDBPath, TMDBAPI: "super-secret-token"}}
	encrypted, err := EncryptConfigSecrets(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	staleDBPath := filepath.Join(t.TempDir(), "old", "db.sqlite")
	writeWebAuthFixtureWithSeed(t, staleDBPath, "wrong-seed")
	encrypted.MainSettings.DBPath = staleDBPath

	repo := &runtimePathRepo{dbPath: runtimeDBPath, cfg: *encrypted}
	got, err := LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("LoadFromDatabase: %v", err)
	}
	if got.MainSettings.TMDBAPI != "super-secret-token" {
		t.Fatalf("secret not decrypted via runtime DB dir: got %q", got.MainSettings.TMDBAPI)
	}
}

func TestDecryptConfigSecretsWithDBDirReportsRuntimeRetryError(t *testing.T) {
	goodDBPath := filepath.Join(t.TempDir(), "good", "db.sqlite")
	writeWebAuthFixtureWithSeed(t, goodDBPath, "good-seed")
	plain := &Config{MainSettings: MainSettingsConfig{DBPath: goodDBPath, TMDBAPI: "super-secret-token"}}
	encrypted, err := EncryptConfigSecrets(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	staleDBPath := filepath.Join(t.TempDir(), "missing", "db.sqlite")
	runtimeDBPath := filepath.Join(t.TempDir(), "runtime", "db.sqlite")
	writeWebAuthFixtureWithSeed(t, runtimeDBPath, "wrong-seed")
	encrypted.MainSettings.DBPath = staleDBPath

	_, err = decryptConfigSecretsWithDBDir(encrypted, runtimeDBPath)
	if err == nil {
		t.Fatalf("expected decrypt error")
	}
	if !errors.Is(err, ErrSecretEncryptionHelperUnavailable) {
		t.Fatalf("expected original helper unavailable error to be preserved, got %v", err)
	}
	if !strings.Contains(err.Error(), "runtime db path decrypt retry") {
		t.Fatalf("expected runtime retry error context, got %v", err)
	}
}

func TestSaveSectionsToDatabaseEncryptsViaRuntimeDBDir(t *testing.T) {
	runtimeDBPath := filepath.Join(t.TempDir(), "runtime", "db.sqlite")
	writeWebAuthFixture(t, runtimeDBPath)
	staleDBPath := filepath.Join(t.TempDir(), "old", "db.sqlite")

	repo := &runtimeSectionRepo{dbPath: runtimeDBPath}
	cfg := validMinimalConfig()
	cfg.MainSettings.DBPath = staleDBPath
	cfg.MainSettings.TMDBAPI = "super-secret-token"

	if err := SaveSectionsToDatabase(context.Background(), cfg, []string{"MainSettings"}, repo); err != nil {
		t.Fatalf("SaveSectionsToDatabase: %v", err)
	}
	mainSettings, ok := repo.sections["MainSettings"].(map[string]any)
	if !ok {
		t.Fatalf("expected MainSettings map, got %#v", repo.sections["MainSettings"])
	}
	if got := mainSettings["DBPath"]; got != staleDBPath {
		t.Fatalf("stored DBPath got %#v want %q", got, staleDBPath)
	}
	token, ok := mainSettings["TMDBAPI"].(string)
	if !ok || !strings.HasPrefix(token, encryptedEnvelopePrefix) {
		t.Fatalf("expected TMDBAPI to be encrypted via runtime helper, got %#v", mainSettings["TMDBAPI"])
	}
}

type runtimeSectionRepo struct {
	dbPath   string
	sections map[string]any
}

func (r *runtimeSectionRepo) DBPath() string { return r.dbPath }

func (r *runtimeSectionRepo) SaveConfigSection(_ context.Context, section string, data any) error {
	if r.sections == nil {
		r.sections = map[string]any{}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal section %s: %w", section, err)
	}
	var stored any
	if err := json.Unmarshal(payload, &stored); err != nil {
		return fmt.Errorf("unmarshal section %s: %w", section, err)
	}
	r.sections[section] = stored
	return nil
}

func TestRepoDBPath(t *testing.T) {
	if got := repoDBPath(&runtimePathRepo{dbPath: "  /a/b  "}); got != "/a/b" {
		t.Fatalf("repoDBPath got %q, want /a/b", got)
	}
	if got := repoDBPath(struct{}{}); got != "" {
		t.Fatalf("repoDBPath for repo without DBPath got %q, want empty", got)
	}
}
