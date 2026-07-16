// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
)

func testMergeBaseConfig(t *testing.T) *config.Config {
	t.Helper()

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "merge.db")
	cfg.MainSettings.TMDBAPI = "stored-key"
	cfg.ScreenshotHandling.Screens = 7
	return cfg
}

func TestMergeProvidedConfigUsesProvidedBytes(t *testing.T) {
	formats := map[string]struct {
		filename string
		original []byte
		mutated  []byte
	}{
		"yaml": {
			filename: "config.yaml",
			original: []byte("main_settings:\n  tmdb_api: original-key\nscreenshot_handling:\n  screens: 4\n"),
			mutated:  []byte("main_settings:\n  tmdb_api: mutated-key\nscreenshot_handling:\n  screens: 5\n"),
		},
		"json": {
			filename: "config.json",
			original: []byte(`{"MainSettings":{"TMDBAPI":"original-key"},"ScreenshotHandling":{"Screens":4}}`),
			mutated:  []byte(`{"MainSettings":{"TMDBAPI":"mutated-key"},"ScreenshotHandling":{"Screens":5}}`),
		},
	}

	for name, tc := range formats {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.filename)
			if err := os.WriteFile(path, tc.mutated, 0o600); err != nil {
				t.Fatalf("write mutated config: %v", err)
			}

			merged, err := mergeProvidedConfig(testMergeBaseConfig(t), path, tc.original, nil)
			if err != nil {
				t.Fatalf("merge provided config: %v", err)
			}
			if merged.MainSettings.TMDBAPI != "original-key" {
				t.Fatalf("merge used changed file bytes: got TMDBAPI %q", merged.MainSettings.TMDBAPI)
			}
			if merged.ScreenshotHandling.Screens != 4 {
				t.Fatalf("merge used changed file bytes: got screens %d", merged.ScreenshotHandling.Screens)
			}
		})
	}
}

func TestMergeNativeConfigObjectOverlayMatrix(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		payload []byte
		wantErr bool
	}{
		{
			name:    "yaml-null",
			format:  "yaml",
			payload: []byte("main_settings: null\n"),
		},
		{
			name:    "yaml-empty-map",
			format:  "yaml",
			payload: []byte("main_settings: {}\n"),
		},
		{
			name:    "yaml-empty-array",
			format:  "yaml",
			payload: []byte("main_settings: []\n"),
			wantErr: true,
		},
		{
			name:    "yaml-scalar",
			format:  "yaml",
			payload: []byte("main_settings: nope\n"),
			wantErr: true,
		},
		{
			name:    "json-null",
			format:  "json",
			payload: []byte(`{"MainSettings":null}`),
		},
		{
			name:    "json-empty-map",
			format:  "json",
			payload: []byte(`{"MainSettings":{}}`),
		},
		{
			name:    "json-empty-array",
			format:  "json",
			payload: []byte(`{"MainSettings":[]}`),
			wantErr: true,
		},
		{
			name:    "json-scalar",
			format:  "json",
			payload: []byte(`{"MainSettings":"nope"}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				merged *config.Config
				err    error
			)
			switch tt.format {
			case "yaml":
				merged, err = mergeYAMLConfig(testMergeBaseConfig(t), tt.payload)
			case "json":
				merged, err = mergeJSONConfig(testMergeBaseConfig(t), tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}

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
				t.Fatalf("merge native config: %v", err)
			}
			if merged.MainSettings.TMDBAPI != "stored-key" {
				t.Fatalf("object overlay clobbered TMDBAPI: got %q", merged.MainSettings.TMDBAPI)
			}
			if merged.ScreenshotHandling.Screens != 7 {
				t.Fatalf("object overlay clobbered screens: got %d", merged.ScreenshotHandling.Screens)
			}
		})
	}
}

func TestMergeNativeConfigNilDynamicTorrentClientEntryMatchesImporterSkip(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		filename string
		payload  []byte
	}{
		{
			name:     "yaml",
			format:   "yaml",
			filename: "config.yaml",
			payload:  []byte("torrent_clients:\n  watch-client: null\n"),
		},
		{
			name:     "json",
			format:   "json",
			filename: "config.json",
			payload:  []byte(`{"TorrentClients":{"watch-client":null}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := testMergeBaseConfig(t)
			base.TorrentClients = map[string]config.TorrentClientConfig{
				"watch-client": {
					Type:        "watch",
					WatchFolder: "stored-watch",
				},
			}

			var (
				merged *config.Config
				err    error
			)
			switch tt.format {
			case "yaml":
				merged, err = mergeYAMLConfig(base, tt.payload)
			case "json":
				merged, err = mergeJSONConfig(base, tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err != nil {
				t.Fatalf("merge native config: %v", err)
			}
			got, ok := merged.TorrentClients["watch-client"]
			if !ok {
				t.Fatal("nil dynamic overlay removed stored torrent client")
			}
			if got.WatchFolder != "stored-watch" {
				t.Fatalf("nil dynamic overlay clobbered stored torrent client: got watch_folder %q", got.WatchFolder)
			}

			imported, _, err := importer.ImportFromContent(tt.filename, tt.payload)
			if err != nil {
				t.Fatalf("import native config: %v", err)
			}
			if _, ok := imported.TorrentClients["watch-client"]; ok {
				t.Fatal("importer should skip nil dynamic torrent client entries")
			}
		})
	}
}

func TestMergeNativeConfigTorrentClientCollectionNullKeepsStoredClients(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		payload []byte
	}{
		{
			name:    "yaml",
			format:  "yaml",
			payload: []byte("torrent_clients: null\n"),
		},
		{
			name:    "json",
			format:  "json",
			payload: []byte(`{"TorrentClients":null}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := testMergeBaseConfig(t)
			base.TorrentClients = map[string]config.TorrentClientConfig{
				"watch-client": {
					Type:        "watch",
					WatchFolder: "stored-watch",
				},
			}

			var (
				merged *config.Config
				err    error
			)
			switch tt.format {
			case "yaml":
				merged, err = mergeYAMLConfig(base, tt.payload)
			case "json":
				merged, err = mergeJSONConfig(base, tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err != nil {
				t.Fatalf("merge native config: %v", err)
			}
			if got := merged.TorrentClients["watch-client"].WatchFolder; got != "stored-watch" {
				t.Fatalf("top-level null collection clobbered stored torrent client: got watch_folder %q", got)
			}
		})
	}
}

func TestMergeNativeConfigRejectsInvalidDynamicTorrentClientEntries(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		payload []byte
	}{
		{
			name:    "yaml-scalar",
			format:  "yaml",
			payload: []byte("torrent_clients:\n  watch-client: nope\n"),
		},
		{
			name:    "yaml-array",
			format:  "yaml",
			payload: []byte("torrent_clients:\n  watch-client: []\n"),
		},
		{
			name:    "json-scalar",
			format:  "json",
			payload: []byte(`{"TorrentClients":{"watch-client":"nope"}}`),
		},
		{
			name:    "json-array",
			format:  "json",
			payload: []byte(`{"TorrentClients":{"watch-client":[]}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			switch tt.format {
			case "yaml":
				_, err = mergeYAMLConfig(testMergeBaseConfig(t), tt.payload)
			case "json":
				_, err = mergeJSONConfig(testMergeBaseConfig(t), tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err == nil {
				t.Fatal("expected invalid dynamic torrent client entry to fail")
			}
			if !strings.Contains(err.Error(), "cannot replace config object") {
				t.Fatalf("expected object replacement error, got %v", err)
			}
		})
	}
}

func TestMergeNativeConfigAcceptsValidAndSkipsEmptyDynamicTorrentClientEntries(t *testing.T) {
	tests := []struct {
		name            string
		format          string
		payload         []byte
		wantType        string
		wantWatchFolder string
		wantAbsent      string
	}{
		{
			name:            "yaml-valid-watch",
			format:          "yaml",
			payload:         []byte("torrent_clients:\n  watch-client:\n    type: watch\n    watch_folder: incoming-watch\n"),
			wantType:        "watch",
			wantWatchFolder: "incoming-watch",
		},
		{
			name:       "yaml-empty-object",
			format:     "yaml",
			payload:    []byte("torrent_clients:\n  empty-client: {}\n"),
			wantAbsent: "empty-client",
		},
		{
			name:            "json-valid-watch",
			format:          "json",
			payload:         []byte(`{"TorrentClients":{"watch-client":{"Type":"watch","WatchFolder":"incoming-watch"}}}`),
			wantType:        "watch",
			wantWatchFolder: "incoming-watch",
		},
		{
			name:       "json-empty-object",
			format:     "json",
			payload:    []byte(`{"TorrentClients":{"empty-client":{}}}`),
			wantAbsent: "empty-client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				merged *config.Config
				err    error
			)
			switch tt.format {
			case "yaml":
				merged, err = mergeYAMLConfig(testMergeBaseConfig(t), tt.payload)
			case "json":
				merged, err = mergeJSONConfig(testMergeBaseConfig(t), tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err != nil {
				t.Fatalf("merge native config: %v", err)
			}
			if tt.wantAbsent != "" {
				if _, ok := merged.TorrentClients[tt.wantAbsent]; ok {
					t.Fatalf("empty dynamic torrent client %q should be skipped", tt.wantAbsent)
				}
				return
			}
			clientName := "watch-client"
			client, ok := merged.TorrentClients[clientName]
			if !ok {
				t.Fatalf("expected dynamic torrent client %q", clientName)
			}
			if client.Type != tt.wantType {
				t.Fatalf("type: got %q want %q", client.Type, tt.wantType)
			}
			if client.WatchFolder != tt.wantWatchFolder {
				t.Fatalf("watch_folder: got %q want %q", client.WatchFolder, tt.wantWatchFolder)
			}
		})
	}
}

func TestMergeNativeConfigRejectsUnknownTopLevelSections(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		payload []byte
	}{
		{
			name:    "yaml-scalar",
			format:  "yaml",
			payload: []byte("unknown_section: nope\n"),
		},
		{
			name:    "yaml-array",
			format:  "yaml",
			payload: []byte("unknown_section: []\n"),
		},
		{
			name:    "yaml-object",
			format:  "yaml",
			payload: []byte("unknown_section:\n  nested: true\n"),
		},
		{
			name:    "yaml-null",
			format:  "yaml",
			payload: []byte("unknown_section: null\n"),
		},
		{
			name:    "yaml-case-twin",
			format:  "yaml",
			payload: []byte("MainSettings:\n  TMDBAPI: wrong\n"),
		},
		{
			name:    "json-scalar",
			format:  "json",
			payload: []byte(`{"UnknownSection":"nope"}`),
		},
		{
			name:    "json-array",
			format:  "json",
			payload: []byte(`{"UnknownSection":[]}`),
		},
		{
			name:    "json-object",
			format:  "json",
			payload: []byte(`{"UnknownSection":{"nested":true}}`),
		},
		{
			name:    "json-null",
			format:  "json",
			payload: []byte(`{"UnknownSection":null}`),
		},
		{
			name:    "json-case-twin",
			format:  "json",
			payload: []byte(`{"main_settings":{"tmdb_api":"wrong"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			switch tt.format {
			case "yaml":
				_, err = mergeYAMLConfig(testMergeBaseConfig(t), tt.payload)
			case "json":
				_, err = mergeJSONConfig(testMergeBaseConfig(t), tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err == nil {
				t.Fatal("expected unknown top-level section to fail")
			}
			if !strings.Contains(err.Error(), "unknown config section") {
				t.Fatalf("expected unknown section error, got %v", err)
			}
		})
	}
}

func TestMergeNativeConfigRejectsUnknownNestedFields(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		payload   []byte
		wantField string
	}{
		{
			name:      "yaml-main-settings",
			format:    "yaml",
			payload:   []byte("main_settings:\n  tmdb_api_typo: drop\n"),
			wantField: "main_settings.tmdb_api_typo",
		},
		{
			name:      "json-main-settings",
			format:    "json",
			payload:   []byte(`{"MainSettings":{"TMDBAPITypo":"drop"}}`),
			wantField: "MainSettings.TMDBAPITypo",
		},
		{
			name:      "yaml-torrent-client",
			format:    "yaml",
			payload:   []byte("torrent_clients:\n  qbit:\n    unknown_key: drop\n"),
			wantField: "torrent_clients.qbit.unknown_key",
		},
		{
			name:      "json-torrent-client",
			format:    "json",
			payload:   []byte(`{"TorrentClients":{"qbit":{"UnknownKey":"drop"}}}`),
			wantField: "TorrentClients.qbit.UnknownKey",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			switch tt.format {
			case "yaml":
				_, err = mergeYAMLConfig(testMergeBaseConfig(t), tt.payload)
			case "json":
				_, err = mergeJSONConfig(testMergeBaseConfig(t), tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err == nil {
				t.Fatal("expected unknown nested field to fail")
			}
			if !strings.Contains(err.Error(), "unknown config field") || !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("expected unknown nested field %q, got %v", tt.wantField, err)
			}
		})
	}
}

func TestMergeNativeConfigPreservesTrackerCustomUnknownKeys(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		payload []byte
		key     string
	}{
		{
			name:    "yaml",
			format:  "yaml",
			payload: []byte("trackers:\n  A4K:\n    api_key: tracker-key\n    custom_yaml: keep\n"),
			key:     "custom_yaml",
		},
		{
			name:    "json",
			format:  "json",
			payload: []byte(`{"Trackers":{"Trackers":{"A4K":{"APIKey":"tracker-key","custom_json":"keep"}}}}`),
			key:     "custom_json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				merged *config.Config
				err    error
			)
			switch tt.format {
			case "yaml":
				merged, err = mergeYAMLConfig(testMergeBaseConfig(t), tt.payload)
			case "json":
				merged, err = mergeJSONConfig(testMergeBaseConfig(t), tt.payload)
			default:
				t.Fatalf("unknown format %q", tt.format)
			}
			if err != nil {
				t.Fatalf("merge native config: %v", err)
			}
			tracker := merged.Trackers.Trackers["A4K"]
			if got := tracker.Unknown[tt.key]; got != "keep" {
				t.Fatalf("expected tracker custom key %q to survive, got %#v", tt.key, got)
			}
		})
	}
}
