// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "missing tmdb api",
			cfg:     Config{},
			wantErr: true,
		},
		{
			name: "watch client missing folder",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"watch": {Type: "watch"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid watch client",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"watch": {Type: "watch", WatchFolder: "/tmp/watch"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid qbit client",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid global torrent client refs",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				ClientSetup: ClientSetupConfig{
					DefaultClient: "qbit",
					InjectClients: CSVList{"qbit"},
					SearchClients: CSVList{"QBIT"},
				},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "global torrent client none selectors allowed",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				ClientSetup: ClientSetupConfig{
					DefaultClient: "none",
					InjectClients: CSVList{"none"},
					SearchClients: CSVList{"none"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid default torrent client ref",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				ClientSetup:        ClientSetupConfig{DefaultClient: "missing"},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid searching torrent client ref",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				ClientSetup:        ClientSetupConfig{SearchClients: CSVList{"missing"}},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid injecting torrent client ref",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				ClientSetup:        ClientSetupConfig{InjectClients: CSVList{"missing"}},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid global torrent client ambiguous folded duplicate",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				ClientSetup:        ClientSetupConfig{DefaultClient: "QBIT"},
				TorrentClients: map[string]TorrentClientConfig{
					"Qbit": {Type: "watch", WatchFolder: "/tmp/watch1"},
					"qbit": {Type: "watch", WatchFolder: "/tmp/watch2"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid tracker torrent client",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {TorrentClient: "qbit"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid tracker torrent client exact match with folded duplicate",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"Qbit": {Type: "watch", WatchFolder: "/tmp/watch1"},
					"qbit": {Type: "watch", WatchFolder: "/tmp/watch2"},
				},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {TorrentClient: "qbit"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid tracker torrent client ambiguous folded duplicate",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"Qbit": {Type: "watch", WatchFolder: "/tmp/watch1"},
					"qbit": {Type: "watch", WatchFolder: "/tmp/watch2"},
				},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {TorrentClient: "QBIT"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid tracker torrent client",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {TorrentClient: "missing"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "tracker torrent client none is not a sentinel",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {TorrentClient: "none"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "tracker torrent client can reference client named none",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"none": {
						Type:     "qbit",
						URL:      "http://localhost",
						Username: "user",
						Password: "pass",
					},
				},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {TorrentClient: "none"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "qbit qui proxy",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {Type: "qbit", QuiProxyURL: "http://localhost:7476/proxy/abc"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid screens",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 0},
			},
			wantErr: true,
		},
		{
			name: "invalid input history limit",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x", InputHistoryLimit: -1},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
			},
			wantErr: true,
		},
		{
			name: "invalid tracker upload concurrency",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				PostUpload:         PostUploadConfig{MaxConcurrentTrackers: -1},
			},
			wantErr: true,
		},
		{
			name: "img rehost policy is validated at runtime",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"TL": {ImgRehost: true},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid img rehost tracker with policy",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"HDB": {ImgRehost: true},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid tracker image host",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"PTP": {ImageHost: "pixhost"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid unsupported tracker image host",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"AITHER": {ImageHost: "imgur"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "tracker image host policy is validated at runtime",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"PTP": {ImageHost: "imgbox"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid owned RF tracker image host",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"RF": {ImageHost: "reelflix"},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestScreenshotHandlingMaxMenuItemsDefaultsAndValidation(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"yaml omitted": "screens: 1\n",
		"yaml zero":    "screens: 1\nmax_menu_items: 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			var cfg ScreenshotHandlingConfig
			if err := yaml.Unmarshal([]byte(payload), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if cfg.MaxMenuItems != DefaultDVDMenuItems {
				t.Fatalf("MaxMenuItems = %d, want %d", cfg.MaxMenuItems, DefaultDVDMenuItems)
			}
		})
	}

	var jsonCfg ScreenshotHandlingConfig
	if err := json.Unmarshal([]byte(`{"Screens":1}`), &jsonCfg); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if jsonCfg.MaxMenuItems != DefaultDVDMenuItems {
		t.Fatalf("JSON MaxMenuItems = %d, want %d", jsonCfg.MaxMenuItems, DefaultDVDMenuItems)
	}

	base := Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "test-key"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("zero legacy value should resolve to default: %v", err)
	}
	if got := base.ScreenshotHandling.ResolvedMaxMenuItems(); got != DefaultDVDMenuItems {
		t.Fatalf("resolved zero = %d, want %d", got, DefaultDVDMenuItems)
	}
	base.ScreenshotHandling.MaxMenuItems = -1
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "max_menu_items") {
		t.Fatalf("negative validation error = %v", err)
	}
	base.ScreenshotHandling.MaxMenuItems = MaxDVDMenuItems + 1
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "max_menu_items") {
		t.Fatalf("over-limit validation error = %v", err)
	}
}

func TestEmbeddedDefaultMaxMenuItems(t *testing.T) {
	t.Parallel()

	cfg, err := LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("LoadEmbeddedDefaultConfig: %v", err)
	}
	if cfg.ScreenshotHandling.MaxMenuItems != DefaultDVDMenuItems {
		t.Fatalf("MaxMenuItems = %d, want %d", cfg.ScreenshotHandling.MaxMenuItems, DefaultDVDMenuItems)
	}
}

func TestMainSettingsInputHistoryLimitDefaultsWhenMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`main_settings:
  tmdb_api: x
screenshot_handling:
  screens: 1
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.MainSettings.InputHistoryLimit != DefaultInputHistoryLimit {
		t.Fatalf("yaml default: got %d want %d", loaded.MainSettings.InputHistoryLimit, DefaultInputHistoryLimit)
	}

	var fromJSON Config
	if err := json.Unmarshal([]byte(`{"MainSettings":{"TMDBAPI":"x"}}`), &fromJSON); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if fromJSON.MainSettings.InputHistoryLimit != DefaultInputHistoryLimit {
		t.Fatalf("json default: got %d want %d", fromJSON.MainSettings.InputHistoryLimit, DefaultInputHistoryLimit)
	}
}

func TestMainSettingsInputHistoryLimitAllowsZero(t *testing.T) {
	t.Parallel()

	var cfg Config
	if err := json.Unmarshal([]byte(`{"MainSettings":{"InputHistoryLimit":0}}`), &cfg); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if cfg.MainSettings.InputHistoryLimit != 0 {
		t.Fatalf("json explicit zero: got %d want 0", cfg.MainSettings.InputHistoryLimit)
	}
}

func TestLoadExampleConfig(t *testing.T) {
	path := filepath.Join("defaults", "example.yaml")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected example config validation error")
	}
	if err.Error() != "config: torrent_clients.qbittorrent.url or qbit_url is required" {
		t.Fatalf("unexpected example config error: %v", err)
	}
}

func TestTrackersConfigJSONFiltersToTrackerSchema(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		Trackers: TrackersConfig{
			DefaultTrackers: CSVList{"A4K"},
			Trackers: map[string]TrackerConfig{
				"A4K": {
					LinkDirName: "",
					APIKey:      "abc",
					AnnounceURL: "https://should-not-be-here",
					Username:    "should-not-be-here",
					ImageHost:   "pixhost",
					FaviconURL:  "https://icons.example/a4k.png",
					Anon:        true,
					Unknown: map[string]any{
						"CustomFlag": "keep",
					},
				},
			},
		},
	}
	configureConfigSecretEncryption(t, cfg)

	payload, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	trackersRoot, ok := root["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("trackers root missing")
	}
	a4kRaw, ok := trackersRoot["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("nested trackers missing")
	}
	a4k, ok := a4kRaw["A4K"].(map[string]any)
	if !ok {
		t.Fatalf("A4K tracker missing")
	}

	if _, exists := a4k["AnnounceURL"]; exists {
		t.Fatalf("A4K should not include AnnounceURL")
	}
	if _, exists := a4k["Username"]; exists {
		t.Fatalf("A4K should not include Username")
	}
	if _, exists := a4k["APIKey"]; !exists {
		t.Fatalf("A4K should include APIKey")
	}
	if _, exists := a4k["ModQ"]; !exists {
		t.Fatalf("A4K should include ModQ from schema defaults")
	}
	if got := a4k["ImageHost"]; got != "pixhost" {
		t.Fatalf("A4K should include ImageHost, got %v", got)
	}
	if got := a4k["FaviconURL"]; got != "https://icons.example/a4k.png" {
		t.Fatalf("A4K should include FaviconURL, got %v", got)
	}
	if got := a4k["CustomFlag"]; got != "keep" {
		t.Fatalf("custom key not preserved, got %v", got)
	}
}

func TestTrackersConfigYAMLFiltersToTrackerSchema(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		Trackers: TrackersConfig{
			DefaultTrackers: CSVList{"A4K"},
			Trackers: map[string]TrackerConfig{
				"A4K": {
					APIKey:      "abc",
					AnnounceURL: "https://should-not-be-here",
					ImageHost:   "pixhost",
					FaviconURL:  "https://icons.example/a4k.png",
					Anon:        true,
					Unknown: map[string]any{
						"custom_yaml": "keep",
					},
				},
			},
		},
	}
	configureConfigSecretEncryption(t, cfg)

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")
	if err := ExportToYAML(cfg, path); err != nil {
		t.Fatalf("export yaml: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	text := string(data)

	if strings.Contains(text, "announce_url: https://should-not-be-here") {
		t.Fatalf("A4K should not include announce_url in yaml export")
	}
	if !regexp.MustCompile(`(?m)^\s*api_key:\s*\S+\s*$`).MatchString(text) {
		t.Fatalf("A4K should include api_key with non-empty value in yaml export")
	}
	if !strings.Contains(text, "image_host: pixhost") {
		t.Fatalf("A4K should include image_host in yaml export")
	}
	if !strings.Contains(text, "favicon_url: https://icons.example/a4k.png") {
		t.Fatalf("A4K should include favicon_url in yaml export")
	}
	if !strings.Contains(text, "custom_yaml: keep") {
		t.Fatalf("unknown custom key should be preserved in yaml export")
	}
}

func TestTrackersConfigCZTPasskeyFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		Trackers: TrackersConfig{
			DefaultTrackers: CSVList{"CZT"},
			Trackers: map[string]TrackerConfig{
				"CZT": {
					APIKey:      "service-token",
					Passkey:     "user-passkey",
					AnnounceURL: "https://should-not-be-here",
				},
			},
		},
	}

	jsonPayload, err := ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}
	jsonRoundTrip, err := ImportFromJSON(jsonPayload)
	if err != nil {
		t.Fatalf("import json: %v", err)
	}
	jsonCZT := jsonRoundTrip.Trackers.Trackers["CZT"]
	if jsonCZT.APIKey != "" {
		t.Fatalf("json CZT should not include APIKey, got %q", jsonCZT.APIKey)
	}
	if jsonCZT.Passkey != "user-passkey" {
		t.Fatalf("json CZT Passkey mismatch: got %q", jsonCZT.Passkey)
	}
	if jsonCZT.AnnounceURL != "" {
		t.Fatalf("json CZT should not include AnnounceURL, got %q", jsonCZT.AnnounceURL)
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")
	if err := ExportToPlaintextYAML(cfg, path); err != nil {
		t.Fatalf("export yaml: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "passkey: user-passkey") {
		t.Fatal("yaml export missing CZT passkey")
	}
	if strings.Contains(text, "url: https://czteam.example") {
		t.Fatalf("yaml CZT should not include url")
	}
	if strings.Contains(text, "api_key: service-token") {
		t.Fatalf("yaml CZT should not include api_key")
	}
	if strings.Contains(text, "announce_url: https://should-not-be-here") {
		t.Fatalf("yaml CZT should not include announce_url")
	}

	yamlRoundTrip, err := ImportFromYAML(path)
	if err != nil {
		t.Fatalf("import yaml: %v", err)
	}
	yamlCZT := yamlRoundTrip.Trackers.Trackers["CZT"]
	if yamlCZT.APIKey != "" {
		t.Fatalf("yaml CZT should not include APIKey, got %q", yamlCZT.APIKey)
	}
	if yamlCZT.Passkey != "user-passkey" {
		t.Fatalf("yaml CZT Passkey mismatch: got %q", yamlCZT.Passkey)
	}
	if yamlCZT.AnnounceURL != "" {
		t.Fatalf("yaml CZT should not include AnnounceURL, got %q", yamlCZT.AnnounceURL)
	}
}

func TestTrackersConfigPreferredTrackerRoundTripJSON(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		Trackers: TrackersConfig{
			DefaultTrackers:  CSVList{"AITHER", "BLU"},
			PreferredTracker: "BLU",
			Trackers: map[string]TrackerConfig{
				"AITHER": {APIKey: "a"},
				"BLU":    {APIKey: "b"},
			},
		},
	}
	configureConfigSecretEncryption(t, cfg)

	payload, err := ExportToJSON(cfg)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}

	imported, err := ImportFromJSONEncrypted(payload)
	if err != nil {
		t.Fatalf("import json: %v", err)
	}

	if imported.Trackers.PreferredTracker != "BLU" {
		t.Fatalf("expected preferred tracker BLU, got %q", imported.Trackers.PreferredTracker)
	}
}

func TestTrackersConfigPreferredTrackerRoundTripYAML(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
		Trackers: TrackersConfig{
			DefaultTrackers:  CSVList{"AITHER", "BLU"},
			PreferredTracker: "AITHER",
			Trackers: map[string]TrackerConfig{
				"AITHER": {APIKey: "a"},
				"BLU":    {APIKey: "b"},
			},
		},
	}
	configureConfigSecretEncryption(t, cfg)

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")
	if err := ExportToYAML(cfg, path); err != nil {
		t.Fatalf("export yaml: %v", err)
	}

	imported, err := Load(path)
	if err != nil {
		t.Fatalf("load yaml: %v", err)
	}

	if imported.Trackers.PreferredTracker != "AITHER" {
		t.Fatalf("expected preferred tracker AITHER, got %q", imported.Trackers.PreferredTracker)
	}
}

func TestLoadEmbeddedDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg, err := LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded default config: %v", err)
	}

	if cfg == nil {
		t.Fatalf("embedded default config is nil")
	}
	if cfg.Trackers.Trackers == nil {
		t.Fatalf("embedded default trackers are missing")
	}
	if _, ok := cfg.Trackers.Trackers["AITHER"]; !ok {
		t.Fatalf("embedded default trackers should include AITHER")
	}
	if _, ok := cfg.Trackers.Trackers["BTN"]; !ok {
		t.Fatalf("embedded default trackers should include BTN")
	}
}

func TestMergeMissingTrackerDefaults(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"AITHER": {APIKey: "existing"},
			},
		},
	}

	changed, err := MergeMissingTrackerDefaults(cfg)
	if err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}
	if !changed {
		t.Fatalf("expected merge missing tracker defaults to report changes")
	}

	if _, ok := cfg.Trackers.Trackers["BTN"]; !ok {
		t.Fatalf("expected BTN to be backfilled from embedded defaults")
	}
	if got := cfg.Trackers.Trackers["AITHER"].APIKey; got != "existing" {
		t.Fatalf("expected existing tracker config to be preserved, got %q", got)
	}
}

func TestResolveBTNAPITokenPrefersTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BTN": {APIKey: "tracker-token"},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "tracker-token" {
		t.Fatalf("expected tracker token, got %q", got)
	}
}

func TestResolveBTNAPITokenUsesLowercaseTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"btn": {APIKey: " lowercase-token "},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "lowercase-token" {
		t.Fatalf("expected lowercase tracker token, got %q", got)
	}
}

func TestResolveBTNAPITokenPrefersExactBTNOverFoldedTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BTN": {APIKey: "canonical-token"},
				"btn": {APIKey: "lowercase-token"},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "canonical-token" {
		t.Fatalf("expected canonical tracker token, got %q", got)
	}
}

func TestResolveBTNAPITokenUsesLowercaseTrackerWhenExactBTNEmpty(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BTN": {APIKey: " "},
				"btn": {APIKey: "lowercase-token"},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "lowercase-token" {
		t.Fatalf("expected lowercase tracker token, got %q", got)
	}
}

func TestResolveBTNAPITokenUsesDeterministicASCIICaseAliasPrecedence(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BTN": {APIKey: " "},
				"bTN": {APIKey: "third-token"},
				"btn": {APIKey: "second-token"},
				"BtN": {APIKey: "first-token"},
			},
		},
	}

	for range 25 {
		if got := ResolveBTNAPIToken(cfg); got != "first-token" {
			t.Fatalf("expected deterministic folded tracker token, got %q", got)
		}
	}
}

func TestResolveBTNAPITokenMatchesASCIICaseTrackerNames(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BTN": {APIKey: " "},
				"BtN": {APIKey: "mixed-token"},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "mixed-token" {
		t.Fatalf("expected mixed-case tracker token, got %q", got)
	}
}

func TestResolveBTNAPITokenDoesNotMatchUnicodeEquivalentTrackerNames(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"\uFF22\uFF34\uFF2E": {APIKey: "fullwidth-token"},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "legacy-token" {
		t.Fatalf("expected legacy token fallback, got %q", got)
	}
}

func TestASCIIEqualFoldComparesEveryByte(t *testing.T) {
	t.Parallel()

	if asciiEqualFold("é", "è") {
		t.Fatal("expected non-ASCII strings with different continuation bytes not to match")
	}
}

func TestResolveBTNAPITokenDoesNotMatchFuzzyTrackerNames(t *testing.T) {
	t.Parallel()

	spacedBTNTrackerName := " BTN "
	cfg := Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				spacedBTNTrackerName: {APIKey: "spaced-token"},
				"BTNx":               {APIKey: "suffix-token"},
				"b-t-n":              {APIKey: "separator-token"},
				"xBTN":               {APIKey: "prefix-token"},
			},
		},
	}

	if got := ResolveBTNAPIToken(cfg); got != "legacy-token" {
		t.Fatalf("expected legacy token fallback, got %q", got)
	}
}

func TestResolveBTNAPITokenUsesLowercaseTrackerConfigFromJSONAndYAML(t *testing.T) {
	t.Parallel()

	var jsonCfg Config
	if err := json.Unmarshal([]byte(`{
		"metadata": {"btn_api": "legacy-token"},
		"trackers": {"Trackers": {"btn": {"APIKey": "json-token"}}}
	}`), &jsonCfg); err != nil {
		t.Fatalf("unmarshal json config: %v", err)
	}
	if got := ResolveBTNAPIToken(jsonCfg); got != "json-token" {
		t.Fatalf("expected json tracker token, got %q", got)
	}

	var yamlCfg Config
	if err := yaml.Unmarshal([]byte(`
metadata:
  btn_api: legacy-token
trackers:
  trackers:
    btn:
      api_key: yaml-token
`), &yamlCfg); err != nil {
		t.Fatalf("unmarshal yaml config: %v", err)
	}
	if got := ResolveBTNAPIToken(yamlCfg); got != "yaml-token" {
		t.Fatalf("expected yaml tracker token, got %q", got)
	}
}

func TestMergeMissingTrackerDefaultsBackfillsLegacyBTNAPIIntoTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{},
		},
	}

	changed, err := MergeMissingTrackerDefaults(cfg)
	if err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}
	if !changed {
		t.Fatalf("expected merge missing tracker defaults to report changes")
	}

	if got := cfg.Trackers.Trackers["BTN"].APIKey; got != "legacy-token" {
		t.Fatalf("expected legacy BTN api token to backfill tracker config, got %q", got)
	}
}

func TestMergeMissingTrackerDefaultsDoesNotBackfillLegacyBTNAPIOverLowercaseTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"btn": {APIKey: "lowercase-token"},
			},
		},
	}

	changed, err := MergeMissingTrackerDefaults(cfg)
	if err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}
	if !changed {
		t.Fatalf("expected merge missing tracker defaults to report changes")
	}

	if got := ResolveBTNAPIToken(*cfg); got != "lowercase-token" {
		t.Fatalf("expected lowercase tracker token after merge, got %q", got)
	}
	if _, ok := cfg.Trackers.Trackers["BTN"]; ok {
		t.Fatalf("expected canonical BTN default not to duplicate lowercase btn tracker")
	}
}

func TestMergeMissingTrackerDefaultsReconcilesLowercaseBTNWithoutToken(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"btn": {Username: "user", Password: "pass"},
			},
		},
	}

	changed, err := MergeMissingTrackerDefaults(cfg)
	if err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}
	if !changed {
		t.Fatalf("expected merge missing tracker defaults to report changes")
	}

	if _, ok := cfg.Trackers.Trackers["BTN"]; ok {
		t.Fatalf("expected canonical BTN default not to duplicate lowercase btn tracker")
	}
	btn := cfg.Trackers.Trackers["btn"]
	if got := btn.APIKey; got != "legacy-token" {
		t.Fatalf("expected legacy BTN api token to backfill lowercase tracker config, got %q", got)
	}
	if got := ResolveBTNAPIToken(*cfg); got != "legacy-token" {
		t.Fatalf("expected legacy tracker token after merge, got %q", got)
	}
	if btn.Username != "user" || btn.Password != "pass" {
		t.Fatalf("expected lowercase btn non-token fields to be preserved, got username=%q password=%q", btn.Username, btn.Password)
	}
}

func TestMergeMissingTrackerDefaultsUsesASCIICaseBTNTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"BtN": {Username: "user"},
			},
		},
	}

	changed, err := MergeMissingTrackerDefaults(cfg)
	if err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}
	if !changed {
		t.Fatalf("expected merge missing tracker defaults to report changes")
	}

	if got := ResolveBTNAPIToken(*cfg); got != "legacy-token" {
		t.Fatalf("expected legacy token on mixed-case tracker after merge, got %q", got)
	}
	if _, ok := cfg.Trackers.Trackers["BTN"]; ok {
		t.Fatalf("expected canonical BTN default not to duplicate mixed-case BTN tracker")
	}
	if got := cfg.Trackers.Trackers["BtN"].APIKey; got != "legacy-token" {
		t.Fatalf("expected mixed-case BTN token to receive legacy token, got %q", got)
	}
}

func TestMergeMissingTrackerDefaultsClearsCZTSensitiveFields(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"CZT": {
					APIKey:      "stale-token",
					AnnounceURL: "https://czteam.me/announce.php?passkey=stale",
					Passkey:     "passkey",
				},
				"czt":                {AnnounceURL: "https://czteam.me/announce.php?passkey=lowercase"},
				"\uFF23\uFF3A\uFF34": {AnnounceURL: "https://czteam.me/announce.php?passkey=fullwidth"},
			},
		},
	}

	changed, err := MergeMissingTrackerDefaults(cfg)
	if err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}
	if !changed {
		t.Fatalf("expected merge missing tracker defaults to report changes")
	}
	czt := cfg.Trackers.Trackers["CZT"]
	if czt.APIKey != "" {
		t.Fatalf("expected CZT APIKey to be cleared, got %q", czt.APIKey)
	}
	if czt.AnnounceURL != "" {
		t.Fatalf("expected CZT AnnounceURL to be cleared, got %q", czt.AnnounceURL)
	}
	if czt.Passkey != "passkey" {
		t.Fatalf("expected CZT passkey preserved, got %q", czt.Passkey)
	}
	if lower := cfg.Trackers.Trackers["czt"]; lower.AnnounceURL != "" {
		t.Fatalf("expected lowercase CZT AnnounceURL to be cleared, got %q", lower.AnnounceURL)
	}
	if folded := cfg.Trackers.Trackers["\uFF23\uFF3A\uFF34"]; folded.AnnounceURL == "" {
		t.Fatalf("expected non-ASCII CZT equivalent AnnounceURL to be preserved")
	}
}

func TestTorrentClientConfigExportUsesCanonicalTypeAndQbitSettings(t *testing.T) {
	t.Parallel()

	allowFallback := true
	verifyWebUI := true
	cfg := &Config{
		TorrentClients: map[string]TorrentClientConfig{
			"q": {
				Type:                     "qbit",
				TorrentClient:            "qbit",
				URL:                      "http://legacy",
				WatchFolder:              "D:\\Watch",
				Username:                 "legacy-user",
				Password:                 "legacy-pass",
				Category:                 "legacy-cat",
				Tags:                     []string{"legacy-tag"},
				QbitURL:                  "http://qbit",
				QbitPort:                 8080,
				QbitUser:                 "qbit-user",
				QbitPass:                 "qbit-pass",
				QbitCategoryValue:        "qbit-cat",
				QbitTag:                  "qbit-tag",
				QbitCrossCategory:        "cross-cat",
				QbitCrossTag:             "cross-tag",
				AutomaticManagementPaths: StringList{"media-root", "archive-root"},
				AllowFallback:            &allowFallback,
				VerifyWebUICertificate:   &verifyWebUI,
			},
		},
	}
	configureConfigSecretEncryption(t, cfg)

	exported, err := ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("ExportToPlaintextJSON failed: %v", err)
	}
	for _, key := range []string{"TorrentClient", "URL", "WatchFolder", "Username", "Password", "Category", "Tags"} {
		if strings.Contains(exported, `"`+key+`"`) {
			t.Fatalf("exported torrent client should not contain legacy key %s: %s", key, exported)
		}
	}
	for _, key := range []string{"Type", "QbitURL", "QbitUser", "QbitPass", "QbitCategoryValue", "QbitTag", "QbitCrossCategory", "QbitCrossTag", "AutomaticManagementPaths"} {
		if !strings.Contains(exported, `"`+key+`"`) {
			t.Fatalf("exported torrent client missing qbit key %s: %s", key, exported)
		}
	}
	var exportShape struct {
		TorrentClients map[string]struct {
			AutomaticManagementPaths []string `json:"AutomaticManagementPaths"`
		} `json:"TorrentClients"`
	}
	if err := json.Unmarshal([]byte(exported), &exportShape); err != nil {
		t.Fatalf("decode exported config: %v", err)
	}
	wantPaths := []string{"media-root", "archive-root"}
	gotPaths := exportShape.TorrentClients["q"].AutomaticManagementPaths
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("AutomaticManagementPaths = %q, want %q", gotPaths, wantPaths)
	}
}

func TestTorrentClientConfigAutomaticManagementPathsAcceptsStringOrList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		expected StringList
	}{
		{
			name:     "string",
			value:    `media-root`,
			expected: StringList{"media-root"},
		},
		{
			name:     "list",
			value:    "[media-root, archive-root]",
			expected: StringList{"media-root", "archive-root"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			payload := "torrent_clients:\n  qbit:\n    type: qbit\n    automatic_management_paths: " + tt.value
			var cfg Config
			if err := yaml.Unmarshal([]byte(payload), &cfg); err != nil {
				t.Fatalf("unmarshal config: %v", err)
			}
			client := cfg.TorrentClients["qbit"]
			if strings.Join(client.AutomaticManagementPaths, "|") != strings.Join(tt.expected, "|") {
				t.Fatalf("unexpected automatic management paths")
			}
		})
	}
}

func TestTorrentClientConfigQbitHostPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		client TorrentClientConfig
		want   string
	}{
		{
			name: "qui proxy stays highest precedence",
			client: TorrentClientConfig{
				QuiProxyURL: "http://proxy.local:7476/proxy/abc",
				QbitURL:     "http://qbit.local",
				URL:         "http://legacy.local",
			},
			want: "http://proxy.local:7476/proxy/abc",
		},
		{
			name: "canonical qbit url beats legacy url",
			client: TorrentClientConfig{
				QbitURL:  "http://qbit.local",
				URL:      "http://legacy.local",
				QbitPort: 8080,
			},
			want: "http://qbit.local:8080",
		},
		{
			name: "legacy url remains fallback",
			client: TorrentClientConfig{
				URL:      "http://legacy.local",
				QbitPort: 8080,
			},
			want: "http://legacy.local:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.client.QbitHost(); got != tt.want {
				t.Fatalf("QbitHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTrackerDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		input        string
		expectedHost string
		expectedURL  string
	}{
		{
			name:         "input is treated as raw domain",
			input:        "my-random-domain.com",
			expectedHost: "my-random-domain.com",
			expectedURL:  "",
		},
		{
			name:         "empty input",
			input:        "",
			expectedHost: "",
			expectedURL:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotHost, gotURL := ResolveTrackerDomain(nil, tc.input)
			if gotHost != tc.expectedHost {
				t.Errorf("expected host %q, got %q", tc.expectedHost, gotHost)
			}
			if gotURL != tc.expectedURL {
				t.Errorf("expected URL %q, got %q", tc.expectedURL, gotURL)
			}
		})
	}
}
