// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"reflect"
	"strings"
	"testing"
)

// ApplyEnvOverrides is the runtime-only knob set: these tests pin down which
// env vars are recognized, how they parse, and — critically — which bad values
// are tolerated vs. which silently overwrite good config.

func TestApplyEnvOverridesNilConfig(t *testing.T) {
	t.Parallel()

	// Must not panic on nil.
	ApplyEnvOverrides(nil)
}

func TestApplyEnvOverridesEmptyValuesLeaveConfig(t *testing.T) {
	// Empty env values must be treated as "not set" so the YAML value survives.
	t.Setenv("UA_DEFAULT_TMDB_API", "")
	t.Setenv("UA_DEFAULT_SCREENS", "")
	t.Setenv("UA_DEFAULT_ONLY_ID", "")
	t.Setenv("UA_DEFAULT_KEEP_IMAGES", "")
	t.Setenv("UA_TRACKERS_DEFAULT", "")
	t.Setenv("UA_TRACKERS_PREFERRED", "")
	t.Setenv("UA_DEFAULT_DB_PATH", "")

	cfg := Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "yaml"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 7},
		Metadata:           MetadataConfig{OnlyID: true, KeepImages: true},
		Trackers: TrackersConfig{
			DefaultTrackers:  CSVList{"AITHER"},
			PreferredTracker: "AITHER",
		},
	}
	ApplyEnvOverrides(&cfg)

	if cfg.MainSettings.TMDBAPI != "yaml" {
		t.Fatalf("empty env should not clear TMDBAPI")
	}
	if cfg.ScreenshotHandling.Screens != 7 {
		t.Fatalf("empty env should not clear Screens")
	}
	if !cfg.Metadata.OnlyID || !cfg.Metadata.KeepImages {
		t.Fatalf("empty env should not flip bools")
	}
	if cfg.Trackers.PreferredTracker != "AITHER" {
		t.Fatalf("empty env should not clear PreferredTracker, got %q", cfg.Trackers.PreferredTracker)
	}
}

// Every string env override must trim surrounding whitespace so a stray \r
// from a Windows-edited .env file doesn't silently break URL matching.
func TestApplyEnvOverridesTrimsWhitespace(t *testing.T) {
	t.Setenv("UA_TRACKERS_PREFERRED", "  AITHER\n")
	t.Setenv("UA_DEFAULT_DB_PATH", " /tmp/upbrr.db \r")
	t.Setenv("UA_DEFAULT_TORRENT_CLIENT", "\tqbit")
	t.Setenv("UA_SONARR_URL", " http://sonarr.local \n")
	t.Setenv("UA_RADARR_URL", "\thttp://radarr.local\t")

	var cfg Config
	ApplyEnvOverrides(&cfg)

	if cfg.Trackers.PreferredTracker != "AITHER" {
		t.Fatalf("PreferredTracker: got %q", cfg.Trackers.PreferredTracker)
	}
	if cfg.MainSettings.DBPath != "/tmp/upbrr.db" {
		t.Fatalf("DBPath: got %q", cfg.MainSettings.DBPath)
	}
	if cfg.ClientSetup.DefaultClient != "qbit" {
		t.Fatalf("DefaultClient: got %q", cfg.ClientSetup.DefaultClient)
	}
	if cfg.ArrIntegration.SonarrURL != "http://sonarr.local" {
		t.Fatalf("SonarrURL: got %q", cfg.ArrIntegration.SonarrURL)
	}
	if cfg.ArrIntegration.RadarrURL != "http://radarr.local" {
		t.Fatalf("RadarrURL: got %q", cfg.ArrIntegration.RadarrURL)
	}
}

// CSV-valued env overrides must split, trim, and drop empty items.
func TestApplyEnvOverridesCSVSplitting(t *testing.T) {
	t.Setenv("UA_TRACKERS_DEFAULT", " AITHER , , BLU ,")
	t.Setenv("UA_DEFAULT_SEARCHING_CLIENT_LIST", "qbit , , deluge")

	var cfg Config
	ApplyEnvOverrides(&cfg)

	wantDefault := CSVList{"AITHER", "BLU"}
	if !reflect.DeepEqual(cfg.Trackers.DefaultTrackers, wantDefault) {
		t.Fatalf("DefaultTrackers: got %v want %v", cfg.Trackers.DefaultTrackers, wantDefault)
	}
	wantSearch := CSVList{"qbit", "deluge"}
	if !reflect.DeepEqual(cfg.ClientSetup.SearchClients, wantSearch) {
		t.Fatalf("SearchClients: got %v want %v", cfg.ClientSetup.SearchClients, wantSearch)
	}
}

// A CSV with only commas must collapse to an empty list (never a list of
// empty strings — those break downstream tracker lookups).
func TestSplitCSVEmptyItems(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"":          {},
		",":         {},
		" , , ":     {},
		"a":         {"a"},
		"a,b,c":     {"a", "b", "c"},
		" a , b , ": {"a", "b"},
		"a,,b":      {"a", "b"},
	}
	for input, want := range cases {
		got := splitCSV(input)
		if len(got) != len(want) {
			t.Errorf("splitCSV(%q): len %d want %d (got %v)", input, len(got), len(want), got)
			continue
		}
		for i, v := range got {
			if v != want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", input, i, v, want[i])
			}
		}
	}
}

// Every integer env var must silently ignore non-numeric garbage so a typo
// cannot flip the config into zero-valued land.
func TestApplyEnvOverridesBadInt(t *testing.T) {
	t.Setenv("UA_DEFAULT_SCREENS", "abc")

	cfg := Config{ScreenshotHandling: ScreenshotHandlingConfig{Screens: 4}}
	ApplyEnvOverrides(&cfg)

	if cfg.ScreenshotHandling.Screens != 4 {
		t.Fatalf("Screens: got %d want 4", cfg.ScreenshotHandling.Screens)
	}
}

// Every bool env var must silently ignore non-bool garbage.
func TestApplyEnvOverridesBadBool(t *testing.T) {
	boolVars := []struct {
		env    string
		access func(Config) bool
		seed   func(*Config)
	}{
		{"UA_DEFAULT_ONLY_ID", func(c Config) bool { return c.Metadata.OnlyID }, func(c *Config) { c.Metadata.OnlyID = true }},
		{"UA_DEFAULT_KEEP_IMAGES", func(c Config) bool { return c.Metadata.KeepImages }, func(c *Config) { c.Metadata.KeepImages = true }},
		{"UA_DEFAULT_PREFER_MAX_16_TORRENT", func(c Config) bool { return c.TorrentCreation.PreferMax16 }, func(c *Config) { c.TorrentCreation.PreferMax16 = true }},
		{"UA_SONARR_USE", func(c Config) bool { return c.ArrIntegration.UseSonarr }, func(c *Config) { c.ArrIntegration.UseSonarr = true }},
		{"UA_RADARR_USE", func(c Config) bool { return c.ArrIntegration.UseRadarr }, func(c *Config) { c.ArrIntegration.UseRadarr = true }},
	}

	for _, bv := range boolVars {
		t.Run(bv.env, func(t *testing.T) {
			t.Setenv(bv.env, "maybe")
			var cfg Config
			bv.seed(&cfg)
			ApplyEnvOverrides(&cfg)
			if !bv.access(cfg) {
				t.Fatalf("%s: expected true (seeded) to survive bad env; got false", bv.env)
			}
		})
	}
}

// Every Sonarr/Radarr URL/API env override must be wired up — missing one is
// a very common regression when new *arr slots are added to the config.
func TestApplyEnvOverridesAllSonarrRadarrSlots(t *testing.T) {
	t.Setenv("UA_SONARR_URL", "s0")
	t.Setenv("UA_SONARR_API_KEY", "sk0")
	t.Setenv("UA_SONARR_URL_1", "s1")
	t.Setenv("UA_SONARR_API_KEY_1", "sk1")
	t.Setenv("UA_SONARR_URL_2", "s2")
	t.Setenv("UA_SONARR_API_KEY_2", "sk2")
	t.Setenv("UA_SONARR_URL_3", "s3")
	t.Setenv("UA_SONARR_API_KEY_3", "sk3")
	t.Setenv("UA_RADARR_URL", "r0")
	t.Setenv("UA_RADARR_API_KEY", "rk0")
	t.Setenv("UA_RADARR_URL_1", "r1")
	t.Setenv("UA_RADARR_API_KEY_1", "rk1")
	t.Setenv("UA_RADARR_URL_2", "r2")
	t.Setenv("UA_RADARR_API_KEY_2", "rk2")
	t.Setenv("UA_RADARR_URL_3", "r3")
	t.Setenv("UA_RADARR_API_KEY_3", "rk3")

	var cfg Config
	ApplyEnvOverrides(&cfg)

	cases := map[string]string{
		"SonarrURL":     cfg.ArrIntegration.SonarrURL,
		"SonarrAPIKey":  cfg.ArrIntegration.SonarrAPIKey,
		"SonarrURL1":    cfg.ArrIntegration.SonarrURL1,
		"SonarrAPIKey1": cfg.ArrIntegration.SonarrAPIKey1,
		"SonarrURL2":    cfg.ArrIntegration.SonarrURL2,
		"SonarrAPIKey2": cfg.ArrIntegration.SonarrAPIKey2,
		"SonarrURL3":    cfg.ArrIntegration.SonarrURL3,
		"SonarrAPIKey3": cfg.ArrIntegration.SonarrAPIKey3,
		"RadarrURL":     cfg.ArrIntegration.RadarrURL,
		"RadarrAPIKey":  cfg.ArrIntegration.RadarrAPIKey,
		"RadarrURL1":    cfg.ArrIntegration.RadarrURL1,
		"RadarrAPIKey1": cfg.ArrIntegration.RadarrAPIKey1,
		"RadarrURL2":    cfg.ArrIntegration.RadarrURL2,
		"RadarrAPIKey2": cfg.ArrIntegration.RadarrAPIKey2,
		"RadarrURL3":    cfg.ArrIntegration.RadarrURL3,
		"RadarrAPIKey3": cfg.ArrIntegration.RadarrAPIKey3,
	}
	for name, value := range cases {
		if value == "" {
			t.Errorf("%s: env var was not wired through", name)
		}
	}
}

// API keys must not be trimmed — some hosts legitimately have leading or
// trailing whitespace as part of a generated key. URL fields must be trimmed.
// This test locks in the current split: URLs trim, API keys don't.
func TestApplyEnvOverridesAPIKeysPreserveWhitespace(t *testing.T) {
	t.Setenv("UA_SONARR_API_KEY", "  padded-key  ")

	var cfg Config
	ApplyEnvOverrides(&cfg)

	if cfg.ArrIntegration.SonarrAPIKey != "  padded-key  " {
		t.Fatalf("expected API key to preserve whitespace, got %q", cfg.ArrIntegration.SonarrAPIKey)
	}
}

// Huge env values (>64KB) must be accepted — paths can legitimately be long
// on Windows, and we shouldn't truncate.
func TestApplyEnvOverridesHugeValue(t *testing.T) {
	huge := strings.Repeat("a", 128*1024)
	t.Setenv("UA_DEFAULT_TMDB_API", huge)

	var cfg Config
	ApplyEnvOverrides(&cfg)

	if cfg.MainSettings.TMDBAPI != huge {
		t.Fatalf("expected full-length env value to be applied, got %d chars", len(cfg.MainSettings.TMDBAPI))
	}
}

// envPrefix must be exactly "UA_" — any drift breaks every deployment that
// configures upbrr through environment variables.
func TestEnvPrefixConstant(t *testing.T) {
	t.Parallel()

	if envPrefix != "UA_" {
		t.Fatalf("envPrefix changed to %q — this is a breaking change for every UA_* env var", envPrefix)
	}
}
