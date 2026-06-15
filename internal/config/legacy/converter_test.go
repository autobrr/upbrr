// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestCoerceToBool(t *testing.T) {
	tests := []struct {
		input any
		want  bool
	}{
		{true, true},
		{false, false},
		{"true", true},
		{"false", false},
		{"True", true},
		{"1", true},
		{"0", false},
		{"yes", true},
		{"no", false},
		{"", false},
		{1, true},
		{0, false},
	}
	for _, tc := range tests {
		got := coerceToBool(tc.input)
		if got != tc.want {
			t.Errorf("coerceToBool(%v) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestCoerceToInt(t *testing.T) {
	tests := []struct {
		input    any
		fallback int
		want     int
	}{
		{42, 0, 42},
		{3.7, 0, 3},
		{"10", 0, 10},
		{"", 5, 5},
		{true, 0, 1},
		{false, 0, 0},
	}
	for _, tc := range tests {
		got := coerceToInt(tc.input, tc.fallback)
		if got != tc.want {
			t.Errorf("coerceToInt(%v, %d) = %d, want %d", tc.input, tc.fallback, got, tc.want)
		}
	}
}

func TestCoerceToFloat(t *testing.T) {
	tests := []struct {
		input    any
		fallback float64
		want     float64
	}{
		{3.14, 0, 3.14},
		{42, 0, 42.0},
		{"2.5", 0, 2.5},
		{"", 1.0, 1.0},
	}
	for _, tc := range tests {
		got := coerceToFloat(tc.input, tc.fallback)
		if got != tc.want {
			t.Errorf("coerceToFloat(%v, %f) = %f, want %f", tc.input, tc.fallback, got, tc.want)
		}
	}
}

func TestCoerceToStringSlice(t *testing.T) {
	tests := []struct {
		input any
		want  int
	}{
		{"a, b, c", 3},
		{[]any{"x", "y"}, 2},
		{"", 0},
	}
	for _, tc := range tests {
		got := coerceToStringSlice(tc.input)
		if len(got) != tc.want {
			t.Errorf("coerceToStringSlice(%v): len=%d, want %d", tc.input, len(got), tc.want)
		}
	}
}

func TestConvertDefaults(t *testing.T) {
	legacy := &Config{
		Default: map[string]any{
			"tmdb_api":    "test-key",
			"screens":     8,
			"tone_map":    true,
			"img_host_1":  "pixhost",
			"unknown_key": "should-be-skipped",
		},
		Trackers:       make(map[string]any),
		TorrentClients: make(map[string]any),
	}

	cfg, warnings, err := ImportFromContent(marshalLegacyConfig(legacy))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MainSettings.TMDBAPI != "test-key" {
		t.Errorf("TMDBAPI: got %q, want test-key", cfg.MainSettings.TMDBAPI)
	}
	if cfg.ScreenshotHandling.Screens != 8 {
		t.Errorf("Screens: got %d, want 8", cfg.ScreenshotHandling.Screens)
	}
	if cfg.ScreenshotHandling.ToneMap != true {
		t.Errorf("ToneMap: got %v, want true", cfg.ScreenshotHandling.ToneMap)
	}
	if cfg.ImageHosting.Host1 != "pixhost" {
		t.Errorf("Host1: got %q, want pixhost", cfg.ImageHosting.Host1)
	}

	hasUnknownWarning := false
	for _, w := range warnings {
		if w == "skipped unknown DEFAULT key: unknown_key" {
			hasUnknownWarning = true
		}
	}
	if !hasUnknownWarning {
		t.Errorf("expected warning for unknown_key, got: %v", warnings)
	}
}

func TestConvertTorrentClients(t *testing.T) {
	legacy := &Config{
		Default: map[string]any{
			"tmdb_api": "test",
			"screens":  6,
		},
		Trackers: make(map[string]any),
		TorrentClients: map[string]any{
			"qbittorrent": map[string]any{
				"torrent_client": "qbit",
				"qbit_url":       "http://localhost:8080",
				"qbit_user":      "admin",
				"qbit_pass":      "secret",
			},
		},
	}

	cfg, _, err := ImportFromContent(marshalLegacyConfig(legacy))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	qbit, ok := cfg.TorrentClients["qbittorrent"]
	if !ok {
		t.Fatal("qbittorrent client not found")
	}
	if qbit.TorrentClient != "qbit" {
		t.Errorf("TorrentClient: got %q, want qbit", qbit.TorrentClient)
	}
	if qbit.QbitURL != "http://localhost:8080" {
		t.Errorf("QbitURL: got %q, want http://localhost:8080", qbit.QbitURL)
	}
	if qbit.QbitUser != "admin" {
		t.Errorf("QbitUser: got %q, want admin", qbit.QbitUser)
	}
	if qbit.QbitPass != "secret" {
		t.Errorf("QbitPass: got %q, want secret", qbit.QbitPass)
	}
}

func TestConvertClientKeyAliases(t *testing.T) {
	legacy := &Config{
		Default: map[string]any{
			"tmdb_api": "test",
			"screens":  6,
		},
		Trackers: make(map[string]any),
		TorrentClients: map[string]any{
			"rtorrent": map[string]any{
				"torrent_client": "rtorrent",
				"rtorrent_url":   "http://localhost:9000",
				"rtorrent_label": "uploads",
			},
		},
	}

	cfg, _, err := ImportFromContent(marshalLegacyConfig(legacy))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rt, ok := cfg.TorrentClients["rtorrent"]
	if !ok {
		t.Fatal("rtorrent client not found")
	}
	if rt.URL != "http://localhost:9000" {
		t.Errorf("URL: got %q, want http://localhost:9000", rt.URL)
	}
	if rt.Category != "uploads" {
		t.Errorf("Category: got %q, want uploads", rt.Category)
	}
}

func TestConvertTorrentClientsDoesNotKeepTemplateQbit(t *testing.T) {
	input := []byte(`
config = {
    'DEFAULT': {'tmdb_api': 'test', 'screens': 6},
    'TRACKERS': {},
    'TORRENT_CLIENTS': {
        'watch-client': {
            'torrent_client': 'watch',
            'watch_folder': 'C:/Watch',
        },
    },
}
`)

	cfg, _, err := ImportFromContent(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.TorrentClients["qbittorrent"]; ok {
		t.Fatal("did not expect template qbittorrent client to be retained")
	}
	watch, ok := cfg.TorrentClients["watch-client"]
	if !ok {
		t.Fatal("watch-client not found")
	}
	if watch.ClientType() != "watch" {
		t.Fatalf("ClientType: got %q, want watch", watch.ClientType())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("converted config should validate: %v", err)
	}
}

func TestConvertTrackers(t *testing.T) {
	legacy := &Config{
		Default: map[string]any{
			"tmdb_api": "test",
			"screens":  6,
		},
		Trackers: map[string]any{
			"default_trackers": "BHD, PTP",
			"BHD": map[string]any{
				"api_key":     "bhd-key",
				"bhd_rss_key": "bhd-rss",
			},
		},
		TorrentClients: make(map[string]any),
	}

	cfg, _, err := ImportFromContent(marshalLegacyConfig(legacy))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Trackers.DefaultTrackers) != 2 {
		t.Errorf("DefaultTrackers: got %v, want [BHD PTP]", cfg.Trackers.DefaultTrackers)
	}

	bhd, ok := cfg.Trackers.Trackers["BHD"]
	if !ok {
		t.Fatal("BHD tracker not found")
	}
	if bhd.APIKey != "bhd-key" {
		t.Errorf("BHD.APIKey: got %q, want bhd-key", bhd.APIKey)
	}
	if bhd.BhdRSSKey != "bhd-rss" {
		t.Errorf("BHD.BhdRSSKey: got %q, want bhd-rss", bhd.BhdRSSKey)
	}
}

func TestConvertTrackersPreferredTrackerNil(t *testing.T) {
	input := []byte(`
config = {
    'DEFAULT': {
        'tmdb_api': 'abc',
        'screens': 6,
    },
    'TRACKERS': {
        'preferred_tracker': None,
        'default_trackers': None,
    },
    'TORRENT_CLIENTS': {},
}
`)
	cfg, _, err := ImportFromContent(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Trackers.PreferredTracker != "" {
		t.Errorf("PreferredTracker: got %q, want empty", cfg.Trackers.PreferredTracker)
	}
	if cfg.Trackers.DefaultTrackers == nil {
		t.Errorf("DefaultTrackers: expected non-nil (template default), got nil")
	}
}

func TestConvertUnknownTrackerWarns(t *testing.T) {
	input := []byte(`
config = {
    'DEFAULT': {'tmdb_api': 'x', 'screens': 4},
    'TRACKERS': {
        'NOT_A_TRACKER': {'api_key': 'x'},
    },
    'TORRENT_CLIENTS': {},
}
`)
	_, warnings, err := ImportFromContent(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "NOT_A_TRACKER") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning for unknown tracker, got: %v", warnings)
	}
}

func TestConvertPointerTorrentClientField(t *testing.T) {
	input := []byte(`
config = {
    'DEFAULT': {'tmdb_api': 'x', 'screens': 4},
    'TRACKERS': {},
    'TORRENT_CLIENTS': {
        'qbittorrent': {
            'torrent_client': 'qbit',
            'qbit_url': 'http://localhost:8080',
            'qbit_user': 'admin',
            'qbit_pass': 'admin',
            'VERIFY_WEBUI_CERTIFICATE': False,
        },
    },
}
`)
	cfg, _, err := ImportFromContent(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	qbit := cfg.TorrentClients["qbittorrent"]
	if qbit.VerifyWebUICertificate == nil {
		t.Fatalf("VerifyWebUICertificate: expected non-nil *bool")
	}
	if *qbit.VerifyWebUICertificate != false {
		t.Errorf("VerifyWebUICertificate: got %v, want false", *qbit.VerifyWebUICertificate)
	}
}

func TestImportFromContentFullLegacy(t *testing.T) {
	input := []byte(`
config = {
    'DEFAULT': {
        'tmdb_api': 'abc123',
        'screens': 6,
        'img_host_1': 'pixhost',
        'img_host_2': 'imgbb',
        'tone_map': True,
        'default_torrent_client': 'qbit',
        'use_sonarr': True,
        'sonarr_url': 'http://localhost:8989',
        'sonarr_api_key': 'sonarr-key',
    },
    'TRACKERS': {
        'default_trackers': 'BHD, PTP',
        'BHD': {
            'api_key': 'bhd-key',
            'anon': True,
        },
    },
    'TORRENT_CLIENTS': {
        'qbittorrent': {
            'torrent_client': 'qbit',
            'qbit_url': 'http://localhost:8080',
            'qbit_user': 'admin',
            'qbit_pass': 'password',
            'qbit_tag': 'upbrr',
        },
    },
}
`)

	cfg, warnings, err := ImportFromContent(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check defaults were migrated.
	if cfg.MainSettings.TMDBAPI != "abc123" {
		t.Errorf("TMDBAPI: got %q", cfg.MainSettings.TMDBAPI)
	}
	if cfg.ScreenshotHandling.Screens != 6 {
		t.Errorf("Screens: got %d", cfg.ScreenshotHandling.Screens)
	}
	if cfg.ImageHosting.Host1 != "pixhost" {
		t.Errorf("Host1: got %q", cfg.ImageHosting.Host1)
	}
	if cfg.ScreenshotHandling.ToneMap != true {
		t.Errorf("ToneMap: got %v", cfg.ScreenshotHandling.ToneMap)
	}
	if cfg.ClientSetup.DefaultClient != "qbit" {
		t.Errorf("DefaultClient: got %q", cfg.ClientSetup.DefaultClient)
	}
	if cfg.ArrIntegration.UseSonarr != true {
		t.Errorf("UseSonarr: got %v", cfg.ArrIntegration.UseSonarr)
	}

	// Check trackers.
	bhd, ok := cfg.Trackers.Trackers["BHD"]
	if !ok {
		t.Fatal("BHD tracker not found")
	}
	if bhd.APIKey != "bhd-key" {
		t.Errorf("BHD.APIKey: got %q", bhd.APIKey)
	}
	if bhd.Anon != true {
		t.Errorf("BHD.Anon: got %v", bhd.Anon)
	}

	// Check torrent clients.
	qbit, ok := cfg.TorrentClients["qbittorrent"]
	if !ok {
		t.Fatal("qbittorrent not found")
	}
	if qbit.QbitURL != "http://localhost:8080" {
		t.Errorf("QbitURL: got %q", qbit.QbitURL)
	}
	if qbit.QbitTag != "upbrr" {
		t.Errorf("QbitTag: got %q", qbit.QbitTag)
	}

	// Warnings should not include errors.
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}
}

// marshalLegacyConfig creates a Python config.py format string from a
// Config for testing purposes.
func marshalLegacyConfig(lc *Config) []byte {
	var sb []byte
	sb = append(sb, "config = {\n"...)
	sb = appendPythonSection(sb, "DEFAULT", lc.Default)
	sb = appendPythonSection(sb, "TRACKERS", lc.Trackers)
	sb = appendPythonSection(sb, "TORRENT_CLIENTS", lc.TorrentClients)
	sb = append(sb, "}\n"...)
	return sb
}

func appendPythonSection(sb []byte, name string, data map[string]any) []byte {
	sb = append(sb, "    '"...)
	sb = append(sb, name...)
	sb = append(sb, "': {\n"...)
	for key, value := range data {
		sb = append(sb, "        '"...)
		sb = append(sb, key...)
		sb = append(sb, "': "...)
		sb = appendPythonValue(sb, value)
		sb = append(sb, ",\n"...)
	}
	sb = append(sb, "    },\n"...)
	return sb
}

func appendPythonValue(sb []byte, value any) []byte {
	switch v := value.(type) {
	case string:
		sb = append(sb, '\'')
		sb = append(sb, v...)
		sb = append(sb, '\'')
	case bool:
		if v {
			sb = append(sb, "True"...)
		} else {
			sb = append(sb, "False"...)
		}
	case int:
		sb = append(sb, []byte(itoa(v))...)
	case float64:
		sb = append(sb, []byte(ftoa(v))...)
	case map[string]any:
		sb = append(sb, "{\n"...)
		for k, val := range v {
			sb = append(sb, "            '"...)
			sb = append(sb, k...)
			sb = append(sb, "': "...)
			sb = appendPythonValue(sb, val)
			sb = append(sb, ",\n"...)
		}
		sb = append(sb, "        }"...)
	case []any:
		sb = append(sb, "["...)
		for i, item := range v {
			if i > 0 {
				sb = append(sb, ", "...)
			}
			sb = appendPythonValue(sb, item)
		}
		sb = append(sb, "]"...)
	default:
		sb = append(sb, "None"...)
	}
	return sb
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func ftoa(f float64) string {
	return fmt.Sprintf("%g", f)
}
