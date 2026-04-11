// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

// Convert is the legacy → new-schema bridge. These tests pin down its contract
// around nil inputs, missing sections, unknown keys, type coercion failures,
// and the torrent-client key-alias table.

func TestConvertNilLegacy(t *testing.T) {
	t.Parallel()

	cfg, _ := config.LoadEmbeddedDefaultConfig()
	if _, _, err := Convert(nil, cfg); err == nil {
		t.Fatalf("expected error for nil legacy")
	}
}

func TestConvertNilTemplate(t *testing.T) {
	t.Parallel()

	legacy := &LegacyConfig{Default: map[string]any{}}
	if _, _, err := Convert(legacy, nil); err == nil {
		t.Fatalf("expected error for nil template")
	}
}

// coerceToBool must treat the Python-y truthiness aliases the converter
// documentation promises.
func TestCoerceToBoolAllAliases(t *testing.T) {
	t.Parallel()

	truthy := []any{true, "true", "TRUE", "True", "1", "yes", "YES", "on", "ON", 1, 2, -1, 3.14, -0.001}
	falsy := []any{false, "false", "False", "0", "no", "off", "maybe", "", 0, 0.0, nil}

	for _, v := range truthy {
		if !coerceToBool(v) {
			t.Errorf("coerceToBool(%v) = false, want true", v)
		}
	}
	for _, v := range falsy {
		if coerceToBool(v) {
			t.Errorf("coerceToBool(%v) = true, want false", v)
		}
	}
}

// coerceToInt must fall back to the template value for unparseable strings
// and unsupported types, not panic or return zero.
func TestCoerceToIntFallback(t *testing.T) {
	t.Parallel()

	if got := coerceToInt("abc", 42); got != 42 {
		t.Errorf("abc: got %d want 42 fallback", got)
	}
	if got := coerceToInt(nil, 7); got != 7 {
		t.Errorf("nil: got %d want 7 fallback", got)
	}
	if got := coerceToInt([]any{1, 2}, 5); got != 5 {
		t.Errorf("slice: got %d want 5 fallback", got)
	}
}

// Floats coerce to int via truncation; the test makes sure we don't round.
func TestCoerceToIntTruncation(t *testing.T) {
	t.Parallel()

	cases := map[float64]int{
		1.9:  1,
		-1.9: -1,
		0.999: 0,
	}
	for in, want := range cases {
		if got := coerceToInt(in, 0); got != want {
			t.Errorf("%f: got %d want %d", in, got, want)
		}
	}
}

// An unknown tracker key must emit a warning but not abort the whole import.
func TestConvertUnknownTrackerKeyWarns(t *testing.T) {
	t.Parallel()

	src := []byte(`
config = {
    'DEFAULT': {'tmdb_api': 'x', 'screens': 4},
    'TRACKERS': {
        'AITHER': {'api_key': 'ok', 'made_up_field': 'nope'},
    },
    'TORRENT_CLIENTS': {},
}
`)
	cfg, warnings, err := ImportFromContent(src)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Trackers.Trackers["AITHER"].APIKey != "ok" {
		t.Fatalf("APIKey: got %q", cfg.Trackers.Trackers["AITHER"].APIKey)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "AITHER") && strings.Contains(w, "made_up_field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for made_up_field, got %v", warnings)
	}
}

// An unknown torrent client key must warn but still register the client.
func TestConvertUnknownTorrentClientKeyWarns(t *testing.T) {
	t.Parallel()

	src := []byte(`
config = {
    'DEFAULT': {'tmdb_api': 'x', 'screens': 4},
    'TRACKERS': {},
    'TORRENT_CLIENTS': {
        'qbit': {'torrent_client': 'qbit', 'qbit_url': 'http://x', 'qbit_user': 'u', 'qbit_pass': 'p', 'made_up': 'nope'},
    },
}
`)
	cfg, warnings, err := ImportFromContent(src)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, ok := cfg.TorrentClients["qbit"]; !ok {
		t.Fatalf("qbit client not registered")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "qbit.made_up") || (strings.Contains(w, "qbit") && strings.Contains(w, "made_up")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for made_up, got %v", warnings)
	}
}

// torrentClientKeyAliases remaps rtorrent_url → url and
// VERIFY_WEBUI_CERTIFICATE → verify_webui_certificate. A regression here
// silently drops rtorrent users' URLs.
func TestConvertTorrentClientKeyAliasesFull(t *testing.T) {
	t.Parallel()

	src := []byte(`
config = {
    'DEFAULT': {'tmdb_api': 'x', 'screens': 4},
    'TRACKERS': {},
    'TORRENT_CLIENTS': {
        'rt': {'torrent_client': 'rtorrent', 'rtorrent_url': 'http://rt', 'rtorrent_label': 'stuff'},
        'q': {'torrent_client': 'qbit', 'qbit_url': 'http://q', 'qbit_user': 'u', 'qbit_pass': 'p', 'VERIFY_WEBUI_CERTIFICATE': True},
    },
}
`)
	cfg, _, err := ImportFromContent(src)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	rt := cfg.TorrentClients["rt"]
	if rt.URL != "http://rt" {
		t.Fatalf("rtorrent_url not aliased: got %q", rt.URL)
	}
	if rt.Category != "stuff" {
		t.Fatalf("rtorrent_label not aliased: got %q", rt.Category)
	}
	q := cfg.TorrentClients["q"]
	if q.VerifyWebUICertificate == nil || !*q.VerifyWebUICertificate {
		t.Fatalf("VerifyWebUICertificate not aliased to true")
	}
}

// Every key in legacyDefaultSectionByKey must round-trip into a field that
// actually exists on the target section. A typo here silently loses user
// settings — we reflect-check via the template config so a field rename can
// still be caught at test time.
func TestConvertAllLegacyDefaultKeysMapToRealField(t *testing.T) {
	t.Parallel()

	template, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	templateMap, err := configToSectionMaps(template)
	if err != nil {
		t.Fatalf("configToSectionMaps: %v", err)
	}

	for key, section := range legacyDefaultSectionByKey {
		sec, ok := templateMap[section]
		if !ok {
			t.Errorf("legacyDefaultSectionByKey[%q] -> %q, but template has no such section", key, section)
			continue
		}
		if _, ok := sec[key]; !ok {
			t.Errorf("legacyDefaultSectionByKey[%q] -> %q: field missing from template", key, section)
		}
	}
}

// Coercing a value that is structurally wrong (string into a bool field) must
// not panic and must produce a plausible default.
func TestConvertTypeMismatchesCoerce(t *testing.T) {
	t.Parallel()

	src := []byte(`
config = {
    'DEFAULT': {
        'tmdb_api': 'x',
        'screens': 'not-a-number',
        'frame_overlay': 'maybe',
        'desat': 'broken',
        'tone_map': 1,
    },
    'TRACKERS': {},
    'TORRENT_CLIENTS': {},
}
`)
	cfg, _, err := ImportFromContent(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Screens falls back to template default (which is non-zero in example.yaml).
	if cfg.ScreenshotHandling.Screens == 0 {
		t.Fatalf("screens should fall back to template default, got 0")
	}
	// maybe → false
	if cfg.ScreenshotHandling.FrameOverlay {
		t.Fatalf("frame_overlay=maybe should coerce to false")
	}
	// tone_map=1 → true
	if !cfg.ScreenshotHandling.ToneMap {
		t.Fatalf("tone_map=1 should coerce to true")
	}
}

// A config with no DEFAULT/TRACKERS/TORRENT_CLIENTS sections must still
// produce a usable template-backed config (with warnings, not errors).
func TestConvertMissingSections(t *testing.T) {
	t.Parallel()

	src := []byte(`config = {}`)
	cfg, _, err := ImportFromContent(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected template-backed config, got nil")
	}
	// Embedded trackers should still be there.
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatalf("expected embedded tracker defaults")
	}
}
