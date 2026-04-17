// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Load is the CLI's read-path: YAML → Unmarshal → ApplyEnvOverrides → Validate.
// These tests exercise each step's failure mode so the CLI can fail fast with a
// useful error instead of booting with silently-defaulted state.

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// Missing file must return an error (not a zero-value Config).
func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Fatalf("error should identify the load stage, got: %v", err)
	}
}

// Malformed YAML must surface a parse error identifying the parse stage.
func TestLoadParseError(t *testing.T) {
	t.Parallel()

	path := writeTemp(t, "main_settings:\n\ttmdb_api: x\n")
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error should identify parse stage: %v", err)
	}
}

// A file that parses but fails Validate() must surface the validate error
// unchanged, so CLI messaging can point at the exact field.
func TestLoadValidateError(t *testing.T) {
	t.Parallel()

	path := writeTemp(t, "main_settings:\n  tmdb_api: \"\"\n")
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected validate error")
	}
	if !strings.Contains(err.Error(), "tmdb_api") {
		t.Fatalf("validate error should mention tmdb_api, got: %v", err)
	}
}

// Env overrides must be applied before Validate so an env-supplied TMDB_API
// rescues a file with an empty tmdb_api.
func TestLoadEnvOverrideRescuesValidation(t *testing.T) {
	t.Setenv("UA_DEFAULT_TMDB_API", "rescued")

	path := writeTemp(t, "main_settings:\n  tmdb_api: \"\"\nscreenshot_handling:\n  screens: 2\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected rescue, got: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "rescued" {
		t.Fatalf("TMDBAPI: got %q", cfg.MainSettings.TMDBAPI)
	}
}

// Env overrides must win over the YAML value (not merge or ignore).
func TestLoadEnvOverrideWinsOverYAML(t *testing.T) {
	t.Setenv("UA_DEFAULT_TMDB_API", "env-wins")

	path := writeTemp(t, "main_settings:\n  tmdb_api: from-yaml\nscreenshot_handling:\n  screens: 1\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "env-wins" {
		t.Fatalf("expected env override, got %q", cfg.MainSettings.TMDBAPI)
	}
}

// An invalid env int must be silently ignored so a typo in the environment
// doesn't crash the CLI; the YAML value must remain in effect.
func TestLoadInvalidEnvIntFallsBackToYAML(t *testing.T) {
	t.Setenv("UA_DEFAULT_SCREENS", "not-a-number")

	path := writeTemp(t, "main_settings:\n  tmdb_api: x\nscreenshot_handling:\n  screens: 5\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ScreenshotHandling.Screens != 5 {
		t.Fatalf("expected yaml screens=5, got %d", cfg.ScreenshotHandling.Screens)
	}
}

// An invalid env bool must also fall back to the YAML value; the existing
// strconv.ParseBool call is silently tolerant, which is the intended behavior.
func TestLoadInvalidEnvBoolFallsBackToYAML(t *testing.T) {
	t.Setenv("UA_DEFAULT_ONLY_ID", "maybe")

	path := writeTemp(t, "main_settings:\n  tmdb_api: x\nmetadata:\n  only_id: true\nscreenshot_handling:\n  screens: 1\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Metadata.OnlyID {
		t.Fatalf("expected only_id to remain true from YAML")
	}
}

// Loading the embedded example.yaml is the CLI's smoke test for template
// integrity. It currently fails validate (no TMDB key). We pin that behavior
// here so anyone who removes the validation must update this test too.
func TestLoadExampleYAMLFailsValidate(t *testing.T) {
	t.Parallel()

	_, err := Load(filepath.Join("defaults", "example.yaml"))
	if err == nil {
		t.Fatalf("embedded example must still fail validate (template has no TMDB key)")
	}
}
