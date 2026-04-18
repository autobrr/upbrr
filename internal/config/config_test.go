// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

func TestLoadDefaultConfig(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	configPath := filepath.Join(home, ".upbrr", "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("default config file not present at %s", configPath)
		}
		t.Fatalf("stat default config path: %v", err)
	}
	_, err = Load(configPath)
	if err != nil {
		lineInfo := formatConfigLineInfo(t, configPath, err.Error())
		t.Fatalf("default config load failed for %s: %v%s", configPath, err, lineInfo)
	}
}

func formatConfigLineInfo(t *testing.T, path, message string) string {
	re := regexp.MustCompile(`line (\d+)`)
	matches := re.FindStringSubmatch(message)
	if len(matches) < 2 {
		return ""
	}
	line, err := strconv.Atoi(matches[1])
	if err != nil || line <= 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Logf("unable to read config for context: %v", err)
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		return ""
	}
	return "\nconfig line " + matches[1] + ": " + strings.TrimRight(lines[line-1], "\r")
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
					Anon:        true,
					Unknown: map[string]interface{}{
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

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	trackersRoot, ok := root["Trackers"].(map[string]interface{})
	if !ok {
		t.Fatalf("trackers root missing")
	}
	a4kRaw, ok := trackersRoot["Trackers"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested trackers missing")
	}
	a4k, ok := a4kRaw["A4K"].(map[string]interface{})
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
					Anon:        true,
					Unknown: map[string]interface{}{
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
