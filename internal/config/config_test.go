// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
					"qbit": {Type: "qbit", URL: "http://localhost", Username: "user", Password: "pass"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid tracker torrent client",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				TorrentClients: map[string]TorrentClientConfig{
					"qbit": {Type: "qbit", URL: "http://localhost", Username: "user", Password: "pass"},
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
					"qbit": {Type: "qbit", URL: "http://localhost", Username: "user", Password: "pass"},
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
					"qbit": {Type: "qbit", URL: "http://localhost", Username: "user", Password: "pass"},
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
					"none": {Type: "qbit", URL: "http://localhost", Username: "user", Password: "pass"},
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
			name: "invalid img rehost tracker without policy",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"TL": {ImgRehost: true},
					},
				},
			},
			wantErr: true,
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
			name: "invalid tracker image host outside tracker policy",
			cfg: Config{
				MainSettings:       MainSettingsConfig{TMDBAPI: "x"},
				ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
				Trackers: TrackersConfig{
					Trackers: map[string]TrackerConfig{
						"PTP": {ImageHost: "imgbox"},
					},
				},
			},
			wantErr: true,
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
		t.Fatalf("expected example config validation error, got nil")
	}
	if err.Error() != "config: main_settings.tmdb_api is required" {
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

	if err := MergeMissingTrackerDefaults(cfg); err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}

	if _, ok := cfg.Trackers.Trackers["BTN"]; !ok {
		t.Fatalf("expected BTN to be backfilled from embedded defaults")
	}
	if got := cfg.Trackers.Trackers["AITHER"].APIKey; got != "existing" {
		t.Fatalf("expected existing tracker config to be preserved, got %q", got)
	}
	if got := cfg.Trackers.Trackers["AITHER"].URL; got != "https://aither.cc" {
		t.Fatalf("expected AITHER URL to be backfilled, got %q", got)
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

func TestMergeMissingTrackerDefaultsBackfillsLegacyBTNAPIIntoTrackerConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Metadata: MetadataConfig{BTNAPI: "legacy-token"},
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{},
		},
	}

	if err := MergeMissingTrackerDefaults(cfg); err != nil {
		t.Fatalf("merge missing tracker defaults: %v", err)
	}

	if got := cfg.Trackers.Trackers["BTN"].APIKey; got != "legacy-token" {
		t.Fatalf("expected legacy BTN api token to backfill tracker config, got %q", got)
	}
}

func TestTorrentClientConfigExportUsesCanonicalTypeAndQbitSettings(t *testing.T) {
	t.Parallel()

	allowFallback := true
	verifyWebUI := true
	cfg := &Config{
		TorrentClients: map[string]TorrentClientConfig{
			"q": {
				Type:                   "qbit",
				TorrentClient:          "qbit",
				URL:                    "http://legacy",
				WatchFolder:            "D:\\Watch",
				Username:               "legacy-user",
				Password:               "legacy-pass",
				Category:               "legacy-cat",
				Tags:                   []string{"legacy-tag"},
				QbitURL:                "http://qbit",
				QbitPort:               8080,
				QbitUser:               "qbit-user",
				QbitPass:               "qbit-pass",
				QbitCategoryValue:      "qbit-cat",
				QbitTag:                "qbit-tag",
				QbitCrossCategory:      "cross-cat",
				QbitCrossTag:           "cross-tag",
				AllowFallback:          &allowFallback,
				VerifyWebUICertificate: &verifyWebUI,
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
	for _, key := range []string{"Type", "QbitURL", "QbitUser", "QbitPass", "QbitCategoryValue", "QbitTag", "QbitCrossCategory", "QbitCrossTag"} {
		if !strings.Contains(exported, `"`+key+`"`) {
			t.Fatalf("exported torrent client missing qbit key %s: %s", key, exported)
		}
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

func TestDisableUnsupportedTrackerImageRehosts(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"TL":  {ImgRehost: true},
				"HDB": {ImgRehost: true},
			},
		},
	}

	disabled := DisableUnsupportedTrackerImageRehosts(&cfg)
	if len(disabled) != 1 || disabled[0] != "TL" {
		t.Fatalf("expected TL to be disabled, got %v", disabled)
	}
	if cfg.Trackers.Trackers["TL"].ImgRehost {
		t.Fatal("expected TL img_rehost to be disabled")
	}
	if !cfg.Trackers.Trackers["HDB"].ImgRehost {
		t.Fatal("expected HDB img_rehost to remain enabled")
	}
}

func TestResolveTrackerDomain(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Trackers: TrackersConfig{
			Trackers: map[string]TrackerConfig{
				"AITHER": {URL: "https://aither.cc"},
				"BLU":    {URL: "blutopia.cc"}, // no scheme
			},
		},
	}

	cases := []struct {
		name         string
		input        string
		expectedHost string
		expectedURL  string
	}{
		{
			name:         "exact match with scheme",
			input:        "AITHER",
			expectedHost: "aither.cc",
			expectedURL:  "https://aither.cc",
		},
		{
			name:         "case-insensitive match",
			input:        "aither",
			expectedHost: "aither.cc",
			expectedURL:  "https://aither.cc",
		},
		{
			name:         "match without scheme",
			input:        "BLU",
			expectedHost: "blutopia.cc",
			expectedURL:  "blutopia.cc",
		},
		{
			name:         "unconfigured tracker is treated as raw domain",
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
			gotHost, gotURL := ResolveTrackerDomain(cfg, tc.input)
			if gotHost != tc.expectedHost {
				t.Errorf("expected host %q, got %q", tc.expectedHost, gotHost)
			}
			if gotURL != tc.expectedURL {
				t.Errorf("expected URL %q, got %q", tc.expectedURL, gotURL)
			}
		})
	}
}
