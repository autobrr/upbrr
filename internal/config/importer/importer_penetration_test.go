// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package importer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The importer is the only interface the WebUI upload path uses to
// ingest a user's old config. These tests make sure every bad input is
// rejected with a clean error instead of crashing or partially-loading.

func TestImportFromContentMalformedYAML(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"tab indent":      "main_settings:\n\ttmdb_api: x\n",
		"unclosed quote":  "main_settings:\n  tmdb_api: \"broken\n",
		"array as map":    "main_settings: [1, 2]\n",
		"nested bad type": "screenshot_handling:\n  screens: not-an-int\n",
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, _, err := ImportFromContent("config.yaml", []byte(body))
			if err == nil {
				t.Fatalf("expected error, got cfg=%+v", cfg)
			}
		})
	}
}

func TestImportFromContentMalformedJSON(t *testing.T) {
	t.Parallel()

	// Keep cases structurally malformed for encoding/json rather than relying
	// on semantic config validation.
	cases := map[string]string{
		"unterminated":     `{"MainSettings":{"TMDBAPI":"x"`,
		"mapping in array": `[{"MainSettings":{"TMDBAPI":"x"}}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := ImportFromContent("config.json", []byte(body)); err == nil {
				t.Fatalf("expected JSON parse error")
			}
		})
	}
}

// UTF-8 BOM on YAML imports is common from Windows editors and must be
// accepted.
func TestImportFromContentYAMLWithBOM(t *testing.T) {
	t.Parallel()

	body := []byte("\ufeffmain_settings:\n  tmdb_api: bom-key\n")
	cfg, _, err := ImportFromContent("config.yaml", body)
	if err != nil {
		t.Fatalf("BOM YAML should parse: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "bom-key" {
		t.Fatalf("TMDBAPI: got %q", cfg.MainSettings.TMDBAPI)
	}
}

// An empty byte slice must be treated as "use defaults" so the WebUI can
// import a freshly-created empty file without crashing.
func TestImportFromContentEmptyBytes(t *testing.T) {
	t.Parallel()

	cfg, _, err := ImportFromContent("config.yaml", nil)
	if err != nil {
		t.Fatalf("empty YAML must not error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default-backed config, got nil")
	}
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatal("expected embedded tracker defaults to be present")
	}
}

// Non-text binary data masquerading as a YAML file must be rejected cleanly.
func TestImportFromContentBinaryMasquerade(t *testing.T) {
	t.Parallel()

	body := []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd, 0x80, 0x81}
	if _, _, err := ImportFromContent("evil.yaml", body); err == nil {
		t.Fatalf("expected error for binary masquerade")
	}
}

// Directories are not files — reading them through ImportFromFile must error.
func TestImportFromFileIsDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, _, err := ImportFromFile(dir); err == nil {
		t.Fatalf("expected error when importing a directory")
	}
}

// A YAML file that happens to end in .PY.BAK is not a python file — the
// extension check must key on the final extension only.
func TestIsPythonFileExtensionBoundaries(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"config.py":          true,
		"config.PY":          true,
		"config.Py":          true,
		"CONFIG.PY":          true,
		"config.py.bak":      false,
		"config.yaml":        false,
		"config":             false,
		"py":                 false,
		".py":                true,
		"weird.name.py":      true,
		"weird.name.py.yaml": false,
	}
	for name, want := range cases {
		if got := isPythonFile(name); got != want {
			t.Errorf("isPythonFile(%q) = %v want %v", name, got, want)
		}
	}
}

// The importer must never yield a non-nil config together with an error —
// callers rely on the cfg being safe to use whenever err == nil, and unsafe
// otherwise.
func TestImportFromContentErrorReturnsNilConfig(t *testing.T) {
	t.Parallel()

	cfg, _, err := ImportFromContent("config.txt", []byte("irrelevant"))
	if err == nil {
		t.Fatalf("expected error")
	}
	if cfg != nil {
		t.Fatalf("error path must return nil config, got %+v", cfg)
	}
}

// MaxFileBytes is a security-sensitive constant — a silent bump could push
// through memory-exhausting uploads.
func TestMaxFileBytesConstant(t *testing.T) {
	t.Parallel()

	if MaxFileBytes != 2*1024*1024 {
		t.Fatalf("MaxFileBytes changed unexpectedly: %d", MaxFileBytes)
	}
}

// A YAML overlay must leave fields absent from the overlay at their embedded
// defaults, not at zero — that's the whole point of the overlay flow.
func TestImportFromContentOverlayPreservesDefaults(t *testing.T) {
	t.Parallel()

	overlay := []byte("main_settings:\n  tmdb_api: override\n")
	cfg, _, err := ImportFromContent("config.yaml", overlay)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "override" {
		t.Fatalf("override not applied: got %q", cfg.MainSettings.TMDBAPI)
	}
	// AITHER is present in the embedded defaults — it must survive.
	if _, ok := cfg.Trackers.Trackers["AITHER"]; !ok {
		t.Fatalf("overlay discarded embedded tracker defaults")
	}
}

// A YAML overlay that sets img_rehost=true on a tracker without a policy must
// be automatically disabled and a warning emitted — that warning is how users
// find out they lost that setting.
func TestImportFromContentWarnsOnImgRehostDisable(t *testing.T) {
	t.Parallel()

	overlay := []byte("trackers:\n  trackers:\n    TL:\n      img_rehost: true\n")
	cfg, warnings, err := ImportFromContent("config.yaml", overlay)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if cfg.Trackers.Trackers["TL"].ImgRehost {
		t.Fatalf("TL img_rehost should have been disabled")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TL") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning mentioning TL, got %v", warnings)
	}
}

// Files that live under a symlinked parent directory must still be readable,
// so WebUI users working out of a symlinked workspace aren't locked out.
func TestImportFromFileThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege on Windows requires elevation")
	}
	t.Parallel()

	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.yaml")
	if err := os.WriteFile(realPath, []byte("main_settings:\n  tmdb_api: via-symlink\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	linkPath := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	cfg, _, err := ImportFromFile(linkPath)
	if err != nil {
		t.Fatalf("symlink import: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "via-symlink" {
		t.Fatalf("TMDBAPI: got %q", cfg.MainSettings.TMDBAPI)
	}
}

// An import of a just-at-limit file must succeed; one-over must fail. Pair-
// testing catches off-by-one changes in the size guard.
func TestImportFromContentSizeBoundary(t *testing.T) {
	t.Parallel()

	// MaxFileBytes of pure spaces parses as empty YAML — succeeds.
	body := make([]byte, MaxFileBytes)
	for i := range body {
		body[i] = ' '
	}
	if _, _, err := ImportFromContent("edge.yaml", body); err != nil {
		t.Fatalf("max-size import failed: %v", err)
	}

	// One byte over must fail.
	over := make([]byte, MaxFileBytes+1)
	if _, _, err := ImportFromContent("edge.yaml", over); err == nil {
		t.Fatalf("expected too-large error at MaxFileBytes+1")
	}
}
