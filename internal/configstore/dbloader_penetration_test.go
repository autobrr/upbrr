// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
)

// The configstore is the single entry point every surface (CLI, GUI, web)
// uses to materialize config at startup. These tests drive the Bootstrap,
// ResolveYAMLPath, LoadFromPathOrEmbedded, and SaveToDBPath contracts through
// edge cases each surface relies on.

func writeWebAuthFixture(t *testing.T, dbPath string) {
	t.Helper()
	authPath := filepath.Join(filepath.Dir(dbPath), authmaterial.WebAuthFileName)
	payload := `{"username":"tester","password_hash":"very-secret-password-hash","encryption_key_seed":"stable-seed-for-tests"}`
	if err := os.WriteFile(authPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write web auth fixture: %v", err)
	}
}

// ResolveYAMLPath must reject empty strings when configProvided is true.
func TestResolveYAMLPathProvidedEmpty(t *testing.T) {
	t.Parallel()

	_, err := configstore.ResolveYAMLPath("", true)
	if err == nil {
		t.Fatalf("expected error on empty provided path")
	}
}

// ResolveYAMLPath must pass through a provided path verbatim.
func TestResolveYAMLPathProvidedPassthrough(t *testing.T) {
	t.Parallel()

	got, err := configstore.ResolveYAMLPath("/custom/path.yaml", true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/custom/path.yaml" {
		t.Fatalf("got %q", got)
	}
}

// ResolveYAMLPath with provided=false must produce the same location as the
// default db directory + config.yaml.
func TestResolveYAMLPathDefault(t *testing.T) {
	t.Parallel()

	def, err := configstore.DefaultYAMLPath()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	got, err := configstore.ResolveYAMLPath("", false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != def {
		t.Fatalf("got %q want %q", got, def)
	}
}

// LoadFromPathOrEmbedded must return the embedded config when the path is
// missing, not an error — this is how the GUI handles fresh installs.
func TestLoadFromPathOrEmbeddedMissingFallsBack(t *testing.T) {
	t.Parallel()

	cfg, err := configstore.LoadFromPathOrEmbedded(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected embedded fallback, got nil")
	}
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatal("expected embedded trackers to be present")
	}
}

// LoadFromPathOrEmbedded must propagate parse errors from a corrupt file —
// silently swallowing them would boot the app with a defaulted config while
// the user's real config is broken.
func TestLoadFromPathOrEmbeddedCorruptFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrupt.yaml")
	if err := os.WriteFile(path, []byte("main_settings:\n\ttmdb_api: x\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := configstore.LoadFromPathOrEmbedded(path)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
}

// An empty path skips the file lookup and goes straight to embedded defaults.
func TestLoadFromPathOrEmbeddedEmptyPath(t *testing.T) {
	t.Parallel()

	cfg, err := configstore.LoadFromPathOrEmbedded("")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected embedded defaults")
	}
}

// LoadFromDBPath on a missing DB should return ErrNotFound so Bootstrap can
// distinguish a virgin install from a real error. This is the trigger for the
// YAML fallback path.
func TestLoadFromDBPathMissingReturnsNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.db")

	_, err := configstore.LoadFromDBPath(ctx, path)
	// Either ErrNotFound or no-error-with-defaults is acceptable depending on
	// whether db.Open auto-creates — but a crash or unrelated error is not.
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("load missing DB: expected nil or ErrNotFound, got: %v", err)
	}
}

// SaveToDBPath → LoadFromDBPath round-trip must preserve every field we
// explicitly set.
func TestSaveLoadDBRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "roundtrip.db")

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	cfg.MainSettings.TMDBAPI = "roundtrip-key"
	cfg.MainSettings.DBPath = path
	cfg.ScreenshotHandling.Screens = 9
	writeWebAuthFixture(t, path)

	if err := configstore.SaveToDBPath(ctx, cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := configstore.LoadFromDBPath(ctx, path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != "roundtrip-key" {
		t.Fatalf("TMDBAPI: got %q", loaded.MainSettings.TMDBAPI)
	}
	if loaded.ScreenshotHandling.Screens != 9 {
		t.Fatalf("Screens: got %d", loaded.ScreenshotHandling.Screens)
	}
}

// Env overrides must be applied to the runtime config returned from
// LoadFromDBPath, but not persisted back to the DB. If the env var goes away,
// the saved value must reappear.
func TestLoadFromDBPathEnvOverrideNotPersisted(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "env.db")

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	cfg.MainSettings.TMDBAPI = "persisted"
	cfg.MainSettings.DBPath = path
	cfg.ScreenshotHandling.Screens = 3
	writeWebAuthFixture(t, path)
	if err := configstore.SaveToDBPath(ctx, cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}

	t.Setenv("UA_DEFAULT_TMDB_API", "from-env")
	loaded, err := configstore.LoadFromDBPath(ctx, path)
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != "from-env" {
		t.Fatalf("expected runtime env override, got %q", loaded.MainSettings.TMDBAPI)
	}

	// Unset and reload — persisted config must still read "persisted".
	t.Setenv("UA_DEFAULT_TMDB_API", "")
	loaded2, err := configstore.LoadFromDBPath(ctx, path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded2.MainSettings.TMDBAPI != "persisted" {
		t.Fatalf("persisted value lost: got %q", loaded2.MainSettings.TMDBAPI)
	}
}

func TestBootstrapDefaultDBPathRepairDoesNotPersistEnvOverrides(t *testing.T) {
	ctx := context.Background()

	for name, storedDBPath := range map[string]string{
		"blank":      "",
		"mismatched": "other.db",
	} {
		t.Run(name, func(t *testing.T) {
			xdgRoot := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", xdgRoot)
			defaultDBPath, err := db.DefaultPath()
			if err != nil {
				t.Fatalf("default db path: %v", err)
			}

			cfg, err := config.LoadEmbeddedDefaultConfig()
			if err != nil {
				t.Fatalf("embed: %v", err)
			}
			cfg.MainSettings.DBPath = storedDBPath
			cfg.MainSettings.TMDBAPI = "persisted"
			cfg.ScreenshotHandling.Screens = 2
			if storedDBPath == "other.db" {
				cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), storedDBPath)
			}
			if err := configstore.SaveToDBPath(ctx, cfg, defaultDBPath); err != nil {
				t.Fatalf("seed default db: %v", err)
			}

			t.Setenv("UA_DEFAULT_TMDB_API", "from-env")
			t.Setenv("UA_DEFAULT_SCREENS", "8")
			runtime, resolvedDB, err := configstore.BootstrapWithValidator(ctx, "", false, false, nil)
			if err != nil {
				t.Fatalf("bootstrap: %v", err)
			}
			if resolvedDB != defaultDBPath {
				t.Fatalf("resolved DB path: got %q want %q", resolvedDB, defaultDBPath)
			}
			if runtime.MainSettings.DBPath != defaultDBPath {
				t.Fatalf("runtime DB path: got %q want %q", runtime.MainSettings.DBPath, defaultDBPath)
			}
			if runtime.MainSettings.TMDBAPI != "from-env" {
				t.Fatalf("runtime env TMDBAPI missing: got %q", runtime.MainSettings.TMDBAPI)
			}
			if runtime.ScreenshotHandling.Screens != 8 {
				t.Fatalf("runtime env screens missing: got %d", runtime.ScreenshotHandling.Screens)
			}

			t.Setenv("UA_DEFAULT_TMDB_API", "")
			t.Setenv("UA_DEFAULT_SCREENS", "")
			stored, err := configstore.LoadFromDBPath(ctx, defaultDBPath)
			if err != nil {
				t.Fatalf("reload default db: %v", err)
			}
			if stored.MainSettings.DBPath != defaultDBPath {
				t.Fatalf("stored DB path repair missing: got %q want %q", stored.MainSettings.DBPath, defaultDBPath)
			}
			if stored.MainSettings.TMDBAPI != "persisted" {
				t.Fatalf("env TMDBAPI leaked into stored config: got %q", stored.MainSettings.TMDBAPI)
			}
			if stored.ScreenshotHandling.Screens != 2 {
				t.Fatalf("env screens leaked into stored config: got %d", stored.ScreenshotHandling.Screens)
			}
		})
	}
}

// Bootstrap with a provided config path and persistYAML=true must import the
// YAML, persist it to the DB, and return a runtime config with env overrides
// applied.
func TestBootstrapProvidedYAMLPersistsToDB(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "bootstrap.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  tmdb_api: provided\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 2\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	writeWebAuthFixture(t, dbPath)

	t.Setenv("UA_DEFAULT_SCREENS", "8")
	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "provided" {
		t.Fatalf("TMDBAPI: got %q", runtime.MainSettings.TMDBAPI)
	}
	if runtime.ScreenshotHandling.Screens != 8 {
		t.Fatalf("env override missing from runtime: got %d", runtime.ScreenshotHandling.Screens)
	}

	// Persisted config in the DB must NOT contain the env override.
	t.Setenv("UA_DEFAULT_SCREENS", "")
	stored, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if stored.ScreenshotHandling.Screens != 2 {
		t.Fatalf("persisted screens: got %d want 2 (env override leaked into DB)", stored.ScreenshotHandling.Screens)
	}
	if stored.MainSettings.TrackerPassChecks != 1 {
		t.Fatalf("persisted tracker_pass_checks default: got %d want 1", stored.MainSettings.TrackerPassChecks)
	}
}

func TestBootstrapWithValidatorRejectsInvalidProvidedYAMLBeforePersist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "invalid.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  tmdb_api: provided\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 0\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, _, err := configstore.BootstrapWithValidator(ctx, yamlPath, true, true, func(cfg *config.Config) error {
		return cfg.Validate()
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "screenshot_handling.screens") {
		t.Fatalf("expected screens validation error, got %v", err)
	}
	if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid provided config should not create database file, stat err=%v", statErr)
	}
}

func TestBootstrapWithValidatorAcceptsMissingOptionalTMDBAPI(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "env-rescue.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 2\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	t.Setenv("UA_DEFAULT_TMDB_API", "from-env")
	runtime, resolvedDB, err := configstore.BootstrapWithValidator(ctx, yamlPath, true, true, func(cfg *config.Config) error {
		return cfg.Validate()
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "from-env" {
		t.Fatalf("runtime TMDBAPI: got %q want env override", runtime.MainSettings.TMDBAPI)
	}
	stored, loadErr := configstore.LoadFromDBPath(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("load stored: %v", loadErr)
	}
	if stored.MainSettings.TMDBAPI != "from-env" {
		t.Fatalf("stored TMDBAPI: got %q want env override", stored.MainSettings.TMDBAPI)
	}
}

func TestBootstrapStoredEncryptedNativeMergeRejectsPlaintextFallback(t *testing.T) {
	ctx := context.Background()
	formats := map[string]string{
		"yaml": "config.yaml",
		"json": "config.json",
	}

	for name, filename := range formats {
		t.Run(name, func(t *testing.T) {
			sourceDir := t.TempDir()
			destDir := t.TempDir()
			sourceDB := filepath.Join(sourceDir, "source.db")
			destDB := filepath.Join(destDir, "dest.db")
			writeWebAuthFixture(t, sourceDB)

			sourceCfg, err := config.LoadEmbeddedDefaultConfig()
			if err != nil {
				t.Fatalf("load source config: %v", err)
			}
			sourceCfg.MainSettings.DBPath = sourceDB
			sourceCfg.MainSettings.TMDBAPI = "encrypted-source-token"
			sourceCfg.ScreenshotHandling.Screens = 4
			sourceCfg.TorrentClients = map[string]config.TorrentClientConfig{}
			if err := sourceCfg.Validate(); err != nil {
				t.Fatalf("source config should validate: %v", err)
			}

			configPath := filepath.Join(sourceDir, filename)
			switch filepath.Ext(filename) {
			case ".yaml":
				if err := config.ExportToYAML(sourceCfg, configPath); err != nil {
					t.Fatalf("export yaml: %v", err)
				}
			case ".json":
				payload, err := config.ExportToJSON(sourceCfg)
				if err != nil {
					t.Fatalf("export json: %v", err)
				}
				if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
					t.Fatalf("write json: %v", err)
				}
			default:
				t.Fatalf("unsupported test filename %q", filename)
			}
			storedCfg, err := config.LoadEmbeddedDefaultConfig()
			if err != nil {
				t.Fatalf("load stored config: %v", err)
			}
			storedCfg.MainSettings.DBPath = destDB
			storedCfg.MainSettings.TMDBAPI = "stored-token"
			storedCfg.TorrentClients = map[string]config.TorrentClientConfig{}
			if err := configstore.SaveToDBPath(ctx, storedCfg, destDB); err != nil {
				t.Fatalf("seed destination DB: %v", err)
			}

			t.Setenv("UA_DEFAULT_DB_PATH", destDB)
			_, _, err = configstore.Bootstrap(ctx, configPath, true, true)
			if err == nil {
				t.Fatal("expected helper-unavailable error for encrypted stored merge")
			}
			if !errors.Is(err, config.ErrSecretEncryptionHelperUnavailable) {
				t.Fatalf("expected ErrSecretEncryptionHelperUnavailable, got %v", err)
			}

			reloaded, err := configstore.LoadFromDBPath(ctx, destDB)
			if err != nil {
				t.Fatalf("reload destination DB: %v", err)
			}
			if reloaded.MainSettings.TMDBAPI != "stored-token" {
				t.Fatalf("stored config changed after failed encrypted merge: got %q", reloaded.MainSettings.TMDBAPI)
			}
		})
	}
}

func TestBootstrapProvidedYAMLPersistsPlaintextSecretsWithoutWebAuth(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "bootstrap-plaintext.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  tmdb_api: provided-secret\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 2\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "provided-secret" {
		t.Fatalf("TMDBAPI: got %q", runtime.MainSettings.TMDBAPI)
	}

	stored, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if stored.MainSettings.TMDBAPI != "provided-secret" {
		t.Fatalf("expected plaintext secret to round-trip without web auth, got %q", stored.MainSettings.TMDBAPI)
	}
}

// Bootstrap with persistYAML=false (webserver mode) must NOT write the YAML
// import to the DB — this keeps zero-valued field defaults from clobbering
// valid DB state.
func TestBootstrapProvidedYAMLNoPersist(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "nopersist.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  tmdb_api: webserver-overlay\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 2\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, _, err := configstore.Bootstrap(ctx, yamlPath, true, false)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if runtime.MainSettings.TMDBAPI != "webserver-overlay" {
		t.Fatalf("runtime TMDBAPI: got %q", runtime.MainSettings.TMDBAPI)
	}

	// The DB file should either not exist (nothing written) or exist but be
	// empty of config rows — LoadFromDBPath should return ErrNotFound or an
	// empty config.
	loaded, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err == nil {
		if loaded.MainSettings.TMDBAPI == "webserver-overlay" {
			t.Fatalf("web bootstrap must not persist YAML to DB")
		}
	}
}

func TestBootstrapProvidedYAMLMergePreservesStoredConfig(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "merge.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	storedCfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	storedCfg.MainSettings.DBPath = dbPath
	storedCfg.MainSettings.TMDBAPI = "stored-key"
	storedCfg.ScreenshotHandling.Screens = 7
	storedCfg.TorrentClients = map[string]config.TorrentClientConfig{}
	if err := configstore.SaveToDBPath(ctx, storedCfg, dbPath); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	body := "main_settings:\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 4\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "stored-key" {
		t.Fatalf("runtime TMDBAPI clobbered: got %q", runtime.MainSettings.TMDBAPI)
	}
	if runtime.ScreenshotHandling.Screens != 4 {
		t.Fatalf("runtime screens: got %d want 4", runtime.ScreenshotHandling.Screens)
	}

	reloaded, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if reloaded.MainSettings.TMDBAPI != "stored-key" {
		t.Fatalf("stored TMDBAPI clobbered: got %q", reloaded.MainSettings.TMDBAPI)
	}
	if reloaded.ScreenshotHandling.Screens != 4 {
		t.Fatalf("stored screens: got %d want 4", reloaded.ScreenshotHandling.Screens)
	}
}

func TestBootstrapProvidedYAMLMergeSkipsNilDynamicTorrentClientEntry(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "merge-nil-client.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	storedCfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	storedCfg.MainSettings.DBPath = dbPath
	storedCfg.MainSettings.TMDBAPI = "stored-key"
	storedCfg.ScreenshotHandling.Screens = 7
	storedCfg.TorrentClients = map[string]config.TorrentClientConfig{
		"watch-client": {
			Type:        "watch",
			WatchFolder: "stored-watch",
		},
	}
	if err := configstore.SaveToDBPath(ctx, storedCfg, dbPath); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	body := "main_settings:\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 4\ntorrent_clients:\n  watch-client: null\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if got := runtime.TorrentClients["watch-client"].WatchFolder; got != "stored-watch" {
		t.Fatalf("runtime nil dynamic overlay clobbered stored torrent client: got watch_folder %q", got)
	}
	if runtime.ScreenshotHandling.Screens != 4 {
		t.Fatalf("runtime screens: got %d want 4", runtime.ScreenshotHandling.Screens)
	}

	reloaded, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if got := reloaded.TorrentClients["watch-client"].WatchFolder; got != "stored-watch" {
		t.Fatalf("stored nil dynamic overlay clobbered torrent client: got watch_folder %q", got)
	}
	if reloaded.ScreenshotHandling.Screens != 4 {
		t.Fatalf("stored screens: got %d want 4", reloaded.ScreenshotHandling.Screens)
	}
}

func TestBootstrapProvidedYAMLEmptyTMDBAPIOverwritesStoredConfig(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "invalid-merge.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	storedCfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	storedCfg.MainSettings.DBPath = dbPath
	storedCfg.MainSettings.TMDBAPI = "stored-key"
	storedCfg.ScreenshotHandling.Screens = 7
	storedCfg.TorrentClients = map[string]config.TorrentClientConfig{}
	if err := configstore.SaveToDBPath(ctx, storedCfg, dbPath); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	body := "main_settings:\n  tmdb_api: \"\"\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 4\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "" || runtime.ScreenshotHandling.Screens != 4 {
		t.Fatalf("runtime should use provided config, got tmdb=%q screens=%d", runtime.MainSettings.TMDBAPI, runtime.ScreenshotHandling.Screens)
	}

	reloaded, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if reloaded.MainSettings.TMDBAPI != "" || reloaded.ScreenshotHandling.Screens != 4 {
		t.Fatalf("provided config not persisted: tmdb=%q screens=%d", reloaded.MainSettings.TMDBAPI, reloaded.ScreenshotHandling.Screens)
	}
}

func TestBootstrapProvidedYAMLEmptyTMDBAPICreatesDB(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "fresh-invalid.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  tmdb_api: \"\"\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 4\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "" {
		t.Fatalf("runtime TMDBAPI: got %q want empty setup value", runtime.MainSettings.TMDBAPI)
	}
	stored, loadErr := configstore.LoadFromDBPath(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("load stored: %v", loadErr)
	}
	if stored.MainSettings.TMDBAPI != "" || stored.ScreenshotHandling.Screens != 4 {
		t.Fatalf("stored config: tmdb=%q screens=%d", stored.MainSettings.TMDBAPI, stored.ScreenshotHandling.Screens)
	}
}

func TestBootstrapProvidedYAMLInvalidMergeDoesNotPersistStoredSanitization(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "invalid-merge-sanitize.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	storedCfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	storedCfg.MainSettings.DBPath = dbPath
	storedCfg.MainSettings.TMDBAPI = "stored-key"
	storedCfg.ScreenshotHandling.Screens = 7
	storedCfg.Trackers.Trackers["TL"] = config.TrackerConfig{ImgRehost: true}
	if err := configstore.SaveToDBPath(ctx, storedCfg, dbPath); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	body := "main_settings:\n  tmdb_api: \"\"\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 4\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	runtime, resolvedDB, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resolvedDB != dbPath {
		t.Fatalf("DB path: got %q want %q", resolvedDB, dbPath)
	}
	if runtime.MainSettings.TMDBAPI != "stored-key" || runtime.ScreenshotHandling.Screens != 7 {
		t.Fatalf("runtime should fall back to stored config, got tmdb=%q screens=%d", runtime.MainSettings.TMDBAPI, runtime.ScreenshotHandling.Screens)
	}

	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer repo.Close()

	reloaded, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		t.Fatalf("load raw stored: %v", err)
	}
	if !reloaded.Trackers.Trackers["TL"].ImgRehost {
		t.Fatalf("invalid provided config persisted unsupported TL img_rehost sanitization")
	}
}

// Bootstrap with an unreadable YAML path must return an error, not fall back
// to embedded — the user asked for a specific file, we can't substitute.
func TestBootstrapProvidedYAMLMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, _, err := configstore.Bootstrap(ctx, filepath.Join(t.TempDir(), "nope.yaml"), true, true)
	if err == nil {
		t.Fatalf("expected error for missing provided config")
	}
}

// Bootstrap with configProvided=true but empty path is an error: the user
// said --config but didn't supply a value.
func TestBootstrapProvidedEmptyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, _, err := configstore.Bootstrap(ctx, "", true, true)
	if err == nil {
		t.Fatalf("expected error for empty --config")
	}
}

// Invariant: the DBPath in the returned runtime config must equal the second
// return value. Drift here silently breaks every feature that computes
// subpaths from cfg.MainSettings.DBPath.
func TestBootstrapDBPathInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "invariant.db")
	yamlPath := filepath.Join(tmp, "config.yaml")

	body := "main_settings:\n  tmdb_api: x\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeWebAuthFixture(t, dbPath)

	runtime, returned, err := configstore.Bootstrap(ctx, yamlPath, true, true)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if runtime.MainSettings.DBPath != returned {
		t.Fatalf("drift: runtime.DBPath=%q returned=%q", runtime.MainSettings.DBPath, returned)
	}
}

// SaveToDBPath on a bad path (parent can't be created because a file exists
// with the same name as an intermediate directory) must error.
func TestSaveToDBPathBadParent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	bad := filepath.Join(blocker, "sub", "child.db")

	cfg, _ := config.LoadEmbeddedDefaultConfig()
	err := configstore.SaveToDBPath(ctx, cfg, bad)
	if err == nil {
		t.Fatalf("expected error for path under a file")
	}
}

// DefaultYAMLPath must return a file under the same directory as the default
// DB so CLI --export-config and --import-config round-trip by convention.
func TestDefaultYAMLPathLocation(t *testing.T) {
	t.Parallel()

	dbPath, err := db.DefaultPath()
	if err != nil {
		t.Fatalf("db default: %v", err)
	}
	yamlPath, err := configstore.DefaultYAMLPath()
	if err != nil {
		t.Fatalf("yaml default: %v", err)
	}
	if filepath.Dir(dbPath) != filepath.Dir(yamlPath) {
		t.Fatalf("DefaultYAMLPath %q must live next to default DB %q", yamlPath, dbPath)
	}
	if !strings.HasSuffix(yamlPath, "config.yaml") {
		t.Fatalf("unexpected yaml path suffix: %q", yamlPath)
	}
}
