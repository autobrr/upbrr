// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
)

type importSecretRepo struct {
	saved *config.Config
}

func (r *importSecretRepo) SaveFullConfig(_ context.Context, cfg any) error {
	typed, ok := cfg.(*config.Config)
	if !ok {
		return errors.New("unexpected config payload type")
	}
	r.saved = typed
	return nil
}

func (r *importSecretRepo) LoadFullConfig(_ context.Context, dest any) error {
	if r.saved == nil {
		return errors.New("no saved config")
	}
	out, ok := dest.(*config.Config)
	if !ok {
		return errors.New("unexpected destination type")
	}
	*out = *r.saved
	return nil
}

func TestImportFromContentYAMLOverlaysDefaults(t *testing.T) {
	yaml := []byte("main_settings:\n  tmdb_api: test-key\n")

	cfg, warnings, err := ImportFromContent("config.yaml", yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(warnings))
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.MainSettings.TMDBAPI != "test-key" {
		t.Fatalf("expected tmdb_api to be overwritten, got %q", cfg.MainSettings.TMDBAPI)
	}
	if cfg.MainSettings.TrackerPassChecks != 1 {
		t.Fatalf("expected tracker_pass_checks default to be preserved, got %d", cfg.MainSettings.TrackerPassChecks)
	}
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatal("expected tracker defaults to be merged in")
	}
}

func TestImportFromContentJSONOverlaysDefaults(t *testing.T) {
	// The export path (ExportToJSON) uses json.MarshalIndent which emits
	// Go field names because the config structs carry no json tags. The
	// import side now uses json.Unmarshal so the round-trip is symmetric.
	json := []byte(`{"MainSettings":{"TMDBAPI":"json-key"}}`)

	cfg, _, err := ImportFromContent("config.json", json)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "json-key" {
		t.Fatalf("expected TMDBAPI to be overwritten, got %q", cfg.MainSettings.TMDBAPI)
	}
	if cfg.MainSettings.TrackerPassChecks != 1 {
		t.Fatalf("expected TrackerPassChecks default to be preserved, got %d", cfg.MainSettings.TrackerPassChecks)
	}
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatal("expected tracker defaults to be merged in")
	}
}

func TestImportFromContentRejectsDestructiveObjectOverlays(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "yaml-null",
			file:    "config.yaml",
			payload: []byte("main_settings: null\n"),
		},
		{
			name:    "yaml-empty-map",
			file:    "config.yaml",
			payload: []byte("main_settings: {}\n"),
		},
		{
			name:    "yaml-empty-array",
			file:    "config.yaml",
			payload: []byte("main_settings: []\n"),
			wantErr: true,
		},
		{
			name:    "yaml-scalar",
			file:    "config.yaml",
			payload: []byte("main_settings: nope\n"),
			wantErr: true,
		},
		{
			name:    "json-null",
			file:    "config.json",
			payload: []byte(`{"MainSettings":null}`),
		},
		{
			name:    "json-empty-map",
			file:    "config.json",
			payload: []byte(`{"MainSettings":{}}`),
		},
		{
			name:    "json-empty-array",
			file:    "config.json",
			payload: []byte(`{"MainSettings":[]}`),
			wantErr: true,
		},
		{
			name:    "json-scalar",
			file:    "config.json",
			payload: []byte(`{"MainSettings":"nope"}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := ImportFromContent(tt.file, tt.payload)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected destructive object replacement to fail")
				}
				if !strings.Contains(err.Error(), "cannot replace config object") {
					t.Fatalf("expected object replacement error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.MainSettings.TrackerPassChecks != 1 {
				t.Fatalf("expected main settings defaults to survive, got tracker_pass_checks=%d", cfg.MainSettings.TrackerPassChecks)
			}
		})
	}
}

func TestImportFromContentRejectsUnknownTopLevelSections(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		payload []byte
	}{
		{
			name:    "yaml-scalar",
			file:    "config.yaml",
			payload: []byte("unknown_section: nope\n"),
		},
		{
			name:    "yaml-array",
			file:    "config.yaml",
			payload: []byte("unknown_section: []\n"),
		},
		{
			name:    "yaml-object",
			file:    "config.yaml",
			payload: []byte("unknown_section:\n  nested: true\n"),
		},
		{
			name:    "yaml-null",
			file:    "config.yaml",
			payload: []byte("unknown_section: null\n"),
		},
		{
			name:    "yaml-case-twin",
			file:    "config.yaml",
			payload: []byte("MainSettings:\n  TMDBAPI: wrong\n"),
		},
		{
			name:    "json-scalar",
			file:    "config.json",
			payload: []byte(`{"UnknownSection":"nope"}`),
		},
		{
			name:    "json-array",
			file:    "config.json",
			payload: []byte(`{"UnknownSection":[]}`),
		},
		{
			name:    "json-object",
			file:    "config.json",
			payload: []byte(`{"UnknownSection":{"nested":true}}`),
		},
		{
			name:    "json-null",
			file:    "config.json",
			payload: []byte(`{"UnknownSection":null}`),
		},
		{
			name:    "json-case-twin",
			file:    "config.json",
			payload: []byte(`{"main_settings":{"tmdb_api":"wrong"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := ImportFromContent(tt.file, tt.payload)
			if err == nil {
				t.Fatal("expected unknown top-level section to fail")
			}
			if cfg != nil {
				t.Fatalf("expected nil config on merge error, got %#v", cfg)
			}
			if !strings.Contains(err.Error(), "unknown config section") {
				t.Fatalf("expected unknown section error, got %v", err)
			}
		})
	}
}

func TestImportFromContentRejectsNestedUnknownKeys(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		payload []byte
		wantKey string
	}{
		{
			name:    "yaml-main-settings-typo",
			file:    "config.yaml",
			payload: []byte("main_settings:\n  tmdb_api_typo: test-key\n"),
			wantKey: "main_settings.tmdb_api_typo",
		},
		{
			name:    "json-main-settings-typo",
			file:    "config.json",
			payload: []byte(`{"MainSettings":{"TMDBAPITypo":"test-key"}}`),
			wantKey: "MainSettings.TMDBAPITypo",
		},
		{
			name:    "json-main-settings-case-twin",
			file:    "config.json",
			payload: []byte(`{"MainSettings":{"tmdbapi":"test-key"}}`),
			wantKey: "MainSettings.tmdbapi",
		},
		{
			name:    "yaml-max-menu-items-noncanonical",
			file:    "config.yaml",
			payload: []byte("screenshot_handling:\n  maxMenuItems: 6\n"),
			wantKey: "screenshot_handling.maxMenuItems",
		},
		{
			name:    "yaml-torrent-client-typo",
			file:    "config.yaml",
			payload: []byte("torrent_clients:\n  watch-client:\n    type: watch\n    watch_folder_typo: C:/Watch\n"),
			wantKey: "torrent_clients.watch-client.watch_folder_typo",
		},
		{
			name:    "json-torrent-client-typo",
			file:    "config.json",
			payload: []byte(`{"TorrentClients":{"watch-client":{"Type":"watch","WatchFolderTypo":"C:/Watch"}}}`),
			wantKey: "TorrentClients.watch-client.WatchFolderTypo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := ImportFromContent(tt.file, tt.payload)
			if err == nil {
				t.Fatal("expected nested unknown key to fail")
			}
			if cfg != nil {
				t.Fatalf("expected nil config on merge error, got %#v", cfg)
			}
			if !strings.Contains(err.Error(), "unknown config key") || !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("expected unknown key %q error, got %v", tt.wantKey, err)
			}
		})
	}
}

func TestImportFromContentPreservesTrackerCustomUnknownKeys(t *testing.T) {
	payloads := map[string][]byte{
		"yaml": []byte("trackers:\n  A4K:\n    api_key: tracker-key\n    custom_yaml: keep\n"),
		"json": []byte(`{
			"Trackers": {
				"Trackers": {
					"A4K": {
						"APIKey": "tracker-key",
						"custom_json": "keep"
					}
				}
			}
		}`),
	}

	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			cfg, _, err := ImportFromContent("config."+name, payload)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tracker := cfg.Trackers.Trackers["A4K"]
			if tracker.APIKey != "tracker-key" {
				t.Fatalf("expected A4K api key overlay, got %q", tracker.APIKey)
			}
			if got := tracker.Unknown["custom_"+name]; got != "keep" {
				t.Fatalf("expected tracker custom key to survive, got %#v", got)
			}
		})
	}
}

func TestImportFromContentEncryptedNativeSecretsRejectPlaintextFallback(t *testing.T) {
	formats := map[string]string{
		"yaml": "config.yaml",
		"json": "config.json",
	}
	for name, filename := range formats {
		t.Run(name, func(t *testing.T) {
			sourceDB := filepath.Join(t.TempDir(), "source.db")
			writeImportWebAuthFixture(t, sourceDB, "source")
			payload := encryptedNativePayload(t, filename, importSecretConfig(sourceDB))

			cfg, _, err := ImportFromContent(filename, payload)
			if err != nil {
				t.Fatalf("import encrypted native config: %v", err)
			}
			assertImportedSecretValues(t, cfg)

			cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "dest.db")
			repo := &importSecretRepo{}
			err = config.SaveToDatabase(context.Background(), cfg, repo)
			if err == nil {
				t.Fatal("expected helper-unavailable save error")
			}
			if !errors.Is(err, config.ErrSecretEncryptionHelperUnavailable) {
				t.Fatalf("expected ErrSecretEncryptionHelperUnavailable, got %v", err)
			}
			if repo.saved != nil {
				t.Fatalf("repository received config despite save error: %+v", repo.saved)
			}
		})
	}
}

func TestImportFromContentEncryptedNativeSecretsReencryptsWithDestinationHelper(t *testing.T) {
	formats := map[string]string{
		"yaml": "config.yaml",
		"json": "config.json",
	}
	for name, filename := range formats {
		t.Run(name, func(t *testing.T) {
			sourceDB := filepath.Join(t.TempDir(), "source.db")
			writeImportWebAuthFixture(t, sourceDB, "source")
			payload := encryptedNativePayload(t, filename, importSecretConfig(sourceDB))

			cfg, _, err := ImportFromContent(filename, payload)
			if err != nil {
				t.Fatalf("import encrypted native config: %v", err)
			}
			assertImportedSecretValues(t, cfg)

			destDB := filepath.Join(t.TempDir(), "dest.db")
			writeImportWebAuthFixture(t, destDB, "destination")
			cfg.MainSettings.DBPath = destDB
			repo := &importSecretRepo{}
			if err := config.SaveToDatabase(context.Background(), cfg, repo); err != nil {
				t.Fatalf("save imported config: %v", err)
			}
			if repo.saved == nil {
				t.Fatal("expected repository to receive saved config")
			}
			assertNoPlaintextSecretValues(t, repo.saved)

			loaded, err := config.LoadFromDatabase(context.Background(), repo)
			if err != nil {
				t.Fatalf("load saved config: %v", err)
			}
			assertImportedSecretValues(t, loaded)
		})
	}
}

func TestImportFromContentYAMLDoesNotKeepTemplateQbit(t *testing.T) {
	yaml := []byte(`
main_settings:
  tmdb_api: test-key
torrent_clients:
  watch-client:
    type: watch
    watch_folder: C:/Watch
`)

	cfg, _, err := ImportFromContent("config.yaml", yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.TorrentClients["qbittorrent"]; ok {
		t.Fatal("did not expect template qbittorrent client to be retained")
	}
	if _, ok := cfg.TorrentClients["watch-client"]; !ok {
		t.Fatal("watch-client not found")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("imported config should validate: %v", err)
	}
}

func TestImportFromContentJSONDoesNotKeepTemplateQbit(t *testing.T) {
	json := []byte(`{
  "MainSettings": {"TMDBAPI": "test-key"},
  "TorrentClients": {
    "watch-client": {
      "Type": "watch",
      "WatchFolder": "C:/Watch"
    }
  }
}`)

	cfg, _, err := ImportFromContent("config.json", json)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.TorrentClients["qbittorrent"]; ok {
		t.Fatal("did not expect template qbittorrent client to be retained")
	}
	if _, ok := cfg.TorrentClients["watch-client"]; !ok {
		t.Fatal("watch-client not found")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("imported config should validate: %v", err)
	}
}

func TestImportFromContentYAMLOmittedTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.yaml", []byte("main_settings:\n  tmdb_api: test-key\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected omitted torrent_clients to import as an empty map")
	}
	if len(cfg.TorrentClients) != 0 {
		t.Fatalf("expected no imported torrent clients, got %d", len(cfg.TorrentClients))
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported struct {
		TorrentClients map[string]config.TorrentClientConfig
	}
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if exported.TorrentClients == nil {
		t.Fatal("expected exported TorrentClients to be {}, got null")
	}
	if len(exported.TorrentClients) != 0 {
		t.Fatalf("expected exported TorrentClients to be empty, got %d", len(exported.TorrentClients))
	}
}

func TestImportFromContentYAMLNullTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.yaml", []byte("main_settings:\n  tmdb_api: test-key\ntorrent_clients: null\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected null torrent_clients to import as an empty map")
	}
	if len(cfg.TorrentClients) != 0 {
		t.Fatalf("expected no imported torrent clients, got %d", len(cfg.TorrentClients))
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if string(exported["TorrentClients"]) != "{}" {
		t.Fatalf("expected exported TorrentClients {}, got %s", exported["TorrentClients"])
	}
}

func TestImportFromContentJSONOmittedTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.json", []byte(`{"MainSettings":{"TMDBAPI":"json-key"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected omitted TorrentClients to import as an empty map")
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if string(exported["TorrentClients"]) != "{}" {
		t.Fatalf("expected exported TorrentClients {}, got %s", exported["TorrentClients"])
	}
}

func TestImportFromContentJSONNullTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.json", []byte(`{"TorrentClients":null}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected null TorrentClients to normalize to an empty map")
	}
	if len(cfg.TorrentClients) != 0 {
		t.Fatalf("expected no imported torrent clients, got %d", len(cfg.TorrentClients))
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if string(exported["TorrentClients"]) != "{}" {
		t.Fatalf("expected exported TorrentClients {}, got %s", exported["TorrentClients"])
	}
}

func TestImportFromContentUnsupportedExtension(t *testing.T) {
	_, _, err := ImportFromContent("config.txt", []byte("irrelevant"))
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("expected unsupported extension error, got %v", err)
	}
}

func TestImportFromContentMissingExtension(t *testing.T) {
	_, _, err := ImportFromContent("config", []byte("irrelevant"))
	if err == nil {
		t.Fatal("expected error when extension is missing")
	}
}

func TestImportFromContentRejectsOversize(t *testing.T) {
	data := make([]byte, MaxFileBytes+1)
	_, _, err := ImportFromContent("big.yaml", data)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}

func TestImportFromContentRoutesPythonToLegacy(t *testing.T) {
	py := []byte(`config = {"DEFAULT": {"tmdb_api": "py-key"}, "TRACKERS": {}, "TORRENT_CLIENTS": {}}`)

	cfg, _, err := ImportFromContent("config.py", py)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestImportFromFileReadsAndDispatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("main_settings:\n  tmdb_api: file-key\n"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cfg, _, err := ImportFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "file-key" {
		t.Fatalf("expected tmdb_api from file, got %q", cfg.MainSettings.TMDBAPI)
	}
}

func TestImportFromFileMissing(t *testing.T) {
	_, _, err := ImportFromFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestImportFromFileRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(path, make([]byte, MaxFileBytes+1), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, _, err := ImportFromFile(path)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}

func TestImportFromContentPreservesTrackerImageRehostForRuntimePolicy(t *testing.T) {
	yaml := []byte("trackers:\n  trackers:\n    TL:\n      img_rehost: true\n")

	cfg, warnings, err := ImportFromContent("config.yaml", yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Trackers.Trackers["TL"].ImgRehost {
		t.Fatal("expected TL img_rehost to remain available for registry policy validation")
	}
	for _, w := range warnings {
		if strings.Contains(w, "TL") && strings.Contains(w, "img_rehost") {
			t.Fatalf("unexpected config-layer tracker policy warning: %v", warnings)
		}
	}
}

func TestImportFromContentRemovesDeprecatedTrackerURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		payload  []byte
		paths    []string
	}{
		{
			name:     "yaml",
			filename: "config.yaml",
			payload:  []byte("trackers:\n  trackers:\n    BTN:\n      Url: https://btn.invalid\n      api_key: token\n    RETIRED:\n      uRL: https://retired.invalid\n      keep_me: retained\n"),
			paths:    []string{"trackers.trackers.BTN.Url", "trackers.trackers.RETIRED.uRL"},
		},
		{
			name:     "json",
			filename: "config.json",
			payload:  []byte(`{"Trackers":{"Trackers":{"BTN":{"uRl":"https://btn.invalid","APIKey":"token"},"RETIRED":{"URL":"https://retired.invalid","keep_me":"retained"}}}}`),
			paths:    []string{"Trackers.Trackers.BTN.uRl", "Trackers.Trackers.RETIRED.URL"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg, warnings, err := ImportFromContent(test.filename, test.payload)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if got := cfg.Trackers.Trackers["BTN"].APIKey; got != "token" {
				t.Fatalf("BTN APIKey = %q", got)
			}
			unsupported := cfg.Trackers.Trackers["RETIRED"]
			if got := unsupported.Unknown["keep_me"]; got != "retained" {
				t.Fatalf("unsupported field = %#v", got)
			}
			for key := range unsupported.Unknown {
				if strings.EqualFold(strings.TrimSpace(key), "url") {
					t.Fatalf("deprecated URL survived in Unknown as %q", key)
				}
			}
			for _, path := range test.paths {
				want := "ignored deprecated tracker URL: " + path
				if !slices.Contains(warnings, want) {
					t.Fatalf("missing warning %q in %#v", want, warnings)
				}
			}
		})
	}
}

// writeImportWebAuthFixture writes auth material whose seed can differ between
// source and destination databases in native encrypted import tests.
func writeImportWebAuthFixture(t *testing.T, dbPath string, seedID string) {
	t.Helper()
	authPath := filepath.Join(filepath.Dir(dbPath), authmaterial.WebAuthFileName)
	payload, err := json.Marshal(map[string]string{
		"username":            "tester",
		"password_hash":       "very-secret-password-hash",
		"encryption_key_seed": "stable-seed-for-import-tests-" + seedID,
	})
	if err != nil {
		t.Fatalf("marshal web auth fixture: %v", err)
	}
	if err := os.WriteFile(authPath, payload, 0o600); err != nil {
		t.Fatalf("write web auth fixture: %v", err)
	}
}

func importSecretConfig(dbPath string) *config.Config {
	return &config.Config{
		MainSettings: config.MainSettingsConfig{
			DBPath:  dbPath,
			TMDBAPI: "plain-tmdb-token",
		},
		ArrIntegration: config.ArrIntegrationConfig{
			SonarrAPIKey: "plain-sonarr-token",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {
					APIKey: "plain-btn-api-key",
				},
			},
		},
		TorrentClients: map[string]config.TorrentClientConfig{
			"qbit": {
				Type:     "qbit",
				QbitPass: "plain-qbit-pass",
			},
		},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
	}
}

func encryptedNativePayload(t *testing.T, filename string, cfg *config.Config) []byte {
	t.Helper()
	switch filepath.Ext(filename) {
	case ".yaml":
		path := filepath.Join(t.TempDir(), filename)
		if err := config.ExportToYAML(cfg, path); err != nil {
			t.Fatalf("export yaml: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read exported yaml: %v", err)
		}
		return data
	case ".json":
		payload, err := config.ExportToJSON(cfg)
		if err != nil {
			t.Fatalf("export json: %v", err)
		}
		return []byte(payload)
	default:
		t.Fatalf("unsupported test filename: %s", filename)
		return nil
	}
}

func assertImportedSecretValues(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg.MainSettings.TMDBAPI != "plain-tmdb-token" {
		t.Fatalf("TMDBAPI: got %q", cfg.MainSettings.TMDBAPI)
	}
	if cfg.ArrIntegration.SonarrAPIKey != "plain-sonarr-token" {
		t.Fatalf("SonarrAPIKey: got %q", cfg.ArrIntegration.SonarrAPIKey)
	}
	if cfg.Trackers.Trackers["BTN"].APIKey != "plain-btn-api-key" {
		t.Fatalf("BTN APIKey: got %q", cfg.Trackers.Trackers["BTN"].APIKey)
	}
	if cfg.TorrentClients["qbit"].QbitPass != "plain-qbit-pass" {
		t.Fatalf("QbitPass: got %q", cfg.TorrentClients["qbit"].QbitPass)
	}
}

func assertNoPlaintextSecretValues(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg.MainSettings.TMDBAPI == "plain-tmdb-token" {
		t.Fatal("saved config leaked plaintext TMDBAPI")
	}
	if cfg.ArrIntegration.SonarrAPIKey == "plain-sonarr-token" {
		t.Fatal("saved config leaked plaintext SonarrAPIKey")
	}
	if cfg.Trackers.Trackers["BTN"].APIKey == "plain-btn-api-key" {
		t.Fatal("saved config leaked plaintext BTN APIKey")
	}
	if cfg.TorrentClients["qbit"].QbitPass == "plain-qbit-pass" {
		t.Fatal("saved config leaked plaintext QbitPass")
	}
}

func TestIsPythonFile(t *testing.T) {
	cases := map[string]bool{
		"config.py":     true,
		"config.PY":     true,
		"config.yaml":   false,
		"config":        false,
		"config.py.bak": false,
	}
	for name, want := range cases {
		if got := isPythonFile(name); got != want {
			t.Errorf("isPythonFile(%q) = %v, want %v", name, got, want)
		}
	}
}
