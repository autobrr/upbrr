// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

// The persistence penetration suite intentionally pushes export/import past
// the happy path: malformed data, hostile encodings, file-system boundaries,
// concurrent callers, and bad wiring. Each test documents the invariant it
// protects so regressions show the "why" immediately.

func validMinimalConfig() *Config {
	return &Config{
		MainSettings:       MainSettingsConfig{},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 3},
	}
}

// ImportFromYAML must surface yaml parse errors instead of panicking or
// returning a zero config.
func TestImportFromYAMLMalformed(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unclosed quote":         "main_settings:\n  tmdb_api: \"broken\n",
		"tab indent":             "main_settings:\n\ttmdb_api: x\n",
		"duplicate top key":      "main_settings:\n  tmdb_api: a\nmain_settings:\n  tmdb_api: b\n",
		"mapping instead scalar": "main_settings: [1, 2, 3]\n",
		"invalid unicode escape": "main_settings:\n  tmdb_api: \"\\u\"\n",
		"binary control bytes":   "main_settings:\n  tmdb_api: \x00\x01\x02\n",
	}

	dir := t.TempDir()
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			cfg, err := ImportFromYAML(path)
			if err == nil {
				t.Fatalf("expected parse error for %q, got cfg=%+v", name, cfg)
			}
		})
	}
}

// ImportFromJSON should not accept garbage payloads silently.
func TestImportFromJSONMalformed(t *testing.T) {
	t.Parallel()

	// ImportFromJSON is dispatched through yaml.v3 which is more permissive
	// than encoding/json: it allows trailing commas in flow maps and will not
	// reject invalid-UTF-8 bytes inside string values. We only pin the cases
	// that are malformed even under the yaml flow-style grammar.
	cases := map[string]string{
		"unquoted key":     `{MainSettings: {"TMDBAPI":"x"}}`,
		"unterminated":     `{"MainSettings": {"TMDBAPI": "x"`,
		"mapping as array": `[{"TMDBAPI":"x"}]`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ImportFromJSON(payload); err == nil {
				t.Fatalf("expected JSON parse error for %q", name)
			}
		})
	}
}

// UTF-8 BOM is a common Windows artifact. yaml.v3 tolerates it at the start of
// a document; we regress-test that the loader does too.
func TestImportFromYAMLWithBOM(t *testing.T) {
	t.Parallel()

	body := "\ufeffmain_settings:\n  tmdb_api: bom-key\nscreenshot_handling:\n  screens: 2\n"
	path := filepath.Join(t.TempDir(), "bom.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := ImportFromYAML(path)
	if err != nil {
		t.Fatalf("BOM-prefixed YAML must be accepted, got: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "bom-key" {
		t.Fatalf("TMDBAPI: got %q want bom-key", cfg.MainSettings.TMDBAPI)
	}
}

// CRLF line endings must parse identically to LF.
func TestImportFromYAMLWithCRLF(t *testing.T) {
	t.Parallel()

	body := "main_settings:\r\n  tmdb_api: crlf-key\r\nscreenshot_handling:\r\n  screens: 1\r\n"
	path := filepath.Join(t.TempDir(), "crlf.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := ImportFromYAML(path)
	if err != nil {
		t.Fatalf("CRLF YAML must parse: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "crlf-key" {
		t.Fatalf("TMDBAPI: got %q", cfg.MainSettings.TMDBAPI)
	}
}

// UTF-16 is not a supported YAML encoding for yaml.v3. We must reject it with a
// clear error instead of decoding garbage into the config.
func TestImportFromYAMLUTF16Rejected(t *testing.T) {
	t.Parallel()

	// UTF-16LE BOM + "main_settings:" bytes — will contain embedded NULs, which
	// yaml.v3 treats as a parse error.
	body := append([]byte{0xff, 0xfe}, []byte{'m', 0, 'a', 0, 'i', 0, 'n', 0, '_', 0, 's', 0, 'e', 0, 't', 0}...)
	path := filepath.Join(t.TempDir(), "utf16.yaml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ImportFromYAML(path); err == nil {
		t.Fatalf("UTF-16 YAML must be rejected")
	}
}

// A zero-byte YAML is a legal empty document in YAML terms; Unmarshal leaves
// the target untouched. The importer should return a usable (empty) Config,
// not a nil pointer — callers rely on this to layer defaults on top.
func TestImportFromYAMLZeroBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := ImportFromYAML(path)
	if err != nil {
		t.Fatalf("empty YAML should not error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("empty YAML must return zero-value Config pointer, got nil")
	}
	if cfg.MainSettings.TMDBAPI != "" {
		t.Fatalf("expected zero-value TMDBAPI, got %q", cfg.MainSettings.TMDBAPI)
	}
}

// A directory supplied where a file is expected must return an error, not
// panic or silently use an empty config.
func TestImportFromYAMLOnDirectory(t *testing.T) {
	t.Parallel()

	if _, err := ImportFromYAML(t.TempDir()); err == nil {
		t.Fatalf("expected error when path is a directory")
	}
}

// Export/Import round-trip must survive arbitrary unicode, quotes, newlines,
// and null-ish characters in string fields.
func TestExportImportYAMLUnicodeAndSpecialChars(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MainSettings: MainSettingsConfig{
			TMDBAPI: "émoji-✨-key\nwith newline",
			DBPath:  "/tmp/日本語/db.sqlite",
		},
		ScreenshotHandling: ScreenshotHandlingConfig{
			Screens:          4,
			TonemapAlgorithm: "\"quoted\" algorithm",
		},
	}
	configureConfigSecretEncryption(t, cfg)
	path := filepath.Join(t.TempDir(), "unicode.yaml")
	if err := ExportToYAML(cfg, path); err != nil {
		t.Fatalf("export: %v", err)
	}
	loaded, err := ImportFromYAML(path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != cfg.MainSettings.TMDBAPI {
		t.Fatalf("TMDBAPI round-trip: got %q want %q", loaded.MainSettings.TMDBAPI, cfg.MainSettings.TMDBAPI)
	}
	if loaded.MainSettings.DBPath != cfg.MainSettings.DBPath {
		t.Fatalf("DBPath round-trip: got %q want %q", loaded.MainSettings.DBPath, cfg.MainSettings.DBPath)
	}
	if loaded.ScreenshotHandling.TonemapAlgorithm != cfg.ScreenshotHandling.TonemapAlgorithm {
		t.Fatalf("Algorithm round-trip: got %q", loaded.ScreenshotHandling.TonemapAlgorithm)
	}
}

// Concurrent exporters writing the same path must not produce a corrupt file:
// every successful write must still be parseable.
func TestExportToYAMLConcurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.yaml")
	cfg := validMinimalConfig()

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			if err := ExportToYAML(cfg, path); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent export failed: %v", err)
	}
	if _, err := ImportFromYAML(path); err != nil {
		t.Fatalf("final file unreadable after concurrent writes: %v", err)
	}
}

// Concurrent export and import on the same path must not panic or race.
func TestExportImportYAMLConcurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "rw.yaml")
	cfg := validMinimalConfig()
	if err := ExportToYAML(cfg, path); err != nil {
		t.Fatalf("initial export: %v", err)
	}

	var wg sync.WaitGroup
	done := make(chan struct{})
	writerErrCh := make(chan error, 1)
	importErrCh := make(chan error, 100)
	wg.Go(func() {
		defer close(writerErrCh)
		for {
			select {
			case <-done:
				return
			default:
				if err := ExportToYAML(cfg, path); err != nil {
					writerErrCh <- err
					return
				}
			}
		}
	})
	wg.Go(func() {
		defer close(importErrCh)
		for i := range 100 {
			if _, err := ImportFromYAML(path); err != nil {
				importErrCh <- errors.New("import iteration " + strconv.Itoa(i) + ": " + err.Error())
			}
		}
		close(done)
	})
	wg.Wait()

	for err := range writerErrCh {
		if err != nil {
			t.Fatalf("concurrent export failed: %v", err)
		}
	}

	for err := range importErrCh {
		if err == nil {
			continue
		}
		msg := err.Error()
		if strings.Contains(msg, "cannot unmarshal") || strings.Contains(msg, "did not find expected") || strings.Contains(msg, "EOF") {
			continue
		}
		t.Fatalf("unexpected concurrent import error: %v", err)
	}

	if _, err := ImportFromYAML(path); err != nil {
		t.Fatalf("final file unreadable after concurrent export/import: %v", err)
	}
}

// ExportToYAML should create the full parent directory chain, not just the
// immediate parent.
func TestExportToYAMLCreatesDeepParents(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a", "b", "c", "d", "config.yaml")
	if err := ExportToYAML(validMinimalConfig(), path); err != nil {
		t.Fatalf("deep export: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

// On Unix we can exercise a read-only parent directory. Skipped on Windows
// because perms there don't affect a child file write the same way.
func TestExportToYAMLParentNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("parent-directory permissions behave differently on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	t.Parallel()

	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	err := ExportToYAML(validMinimalConfig(), filepath.Join(parent, "sub", "config.yaml"))
	if err == nil {
		t.Fatalf("expected permission error writing into read-only parent")
	}
}

// ImportFromYAML should forward read errors on Unix when the file is not
// readable by the current user.
func TestImportFromYAMLUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission model")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unreadable.yaml")
	if err := os.WriteFile(path, []byte("main_settings:\n  tmdb_api: x\n"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := ImportFromYAML(path)
	if err == nil {
		t.Fatalf("expected permission error reading file")
	}
	if errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("permission error should not be mapped to ErrNotFound: %v", err)
	}
}

// A YAML field typed as int but supplied as a string must fail the parser so
// callers don't operate on zero-valued integer fields.
func TestImportFromYAMLTypeMismatch(t *testing.T) {
	t.Parallel()

	body := "screenshot_handling:\n  screens: not-a-number\n"
	path := filepath.Join(t.TempDir(), "badtype.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ImportFromYAML(path); err == nil {
		t.Fatalf("expected parse error for string-in-int field")
	}
}

// JSON-typed mismatches must also surface errors, not silently coerce.
func TestImportFromJSONTypeMismatch(t *testing.T) {
	t.Parallel()

	payload := `{"ScreenshotHandling":{"Screens":"not-a-number"}}`
	if _, err := ImportFromJSON(payload); err == nil {
		t.Fatalf("expected json type mismatch error")
	}
}

// ExportToJSON must succeed on a fully-zero Config — the GUI calls this on
// first launch before the user has set anything.
func TestExportToJSONZeroConfig(t *testing.T) {
	t.Parallel()

	payload, err := ExportToJSON(&Config{})
	if err != nil {
		t.Fatalf("export zero: %v", err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &back); err != nil {
		t.Fatalf("zero config JSON is not valid JSON: %v", err)
	}
}

// BackupToYAML must refuse to write when config is nil or baseDir empty,
// and must not leak a partially-created file on error.
func TestBackupToYAMLInvalidInputs(t *testing.T) {
	t.Parallel()

	if _, err := BackupToYAML(nil, t.TempDir()); err == nil {
		t.Fatalf("expected error for nil cfg")
	}
	if _, err := BackupToYAML(validMinimalConfig(), ""); err == nil {
		t.Fatalf("expected error for empty baseDir")
	}
}

// BackupToYAML writes a fixed filename under backups/. Calling twice should
// overwrite, not error. The produced file must remain loadable.
func TestBackupToYAMLOverwrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := validMinimalConfig()
	configureConfigSecretEncryption(t, first)
	first.MainSettings.TMDBAPI = "first"
	second := validMinimalConfig()
	second.MainSettings.DBPath = first.MainSettings.DBPath
	second.MainSettings.TMDBAPI = "second"

	pathA, err := BackupToYAML(first, dir)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	pathB, err := BackupToYAML(second, dir)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if pathA != pathB {
		t.Fatalf("expected fixed backup path, got %q and %q", pathA, pathB)
	}
	loaded, err := ImportFromYAML(pathB)
	if err != nil {
		t.Fatalf("load final backup: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != "second" {
		t.Fatalf("overwrite did not take effect: got %q", loaded.MainSettings.TMDBAPI)
	}
}

// SaveSectionToDatabase / LoadSectionFromDatabase must reject invalid inputs
// with specific errors rather than silently no-oping.
type recordingRepo struct {
	savedSection string
	loadedDest   interface{}
	saveErr      error
	loadErr      error
}

func (r *recordingRepo) SaveConfigSection(_ context.Context, section string, _ interface{}) error {
	r.savedSection = section
	return r.saveErr
}
func (r *recordingRepo) LoadConfigSection(_ context.Context, _ string, dest interface{}) error {
	r.loadedDest = dest
	return r.loadErr
}

func TestSaveSectionToDatabaseInvalidInputs(t *testing.T) {
	t.Parallel()

	repo := &recordingRepo{}
	if err := SaveSectionToDatabase(context.Background(), "", map[string]string{"k": "v"}, repo); err == nil {
		t.Fatalf("expected error for empty section")
	}
	if err := SaveSectionToDatabase(context.Background(), "logging", nil, repo); err == nil {
		t.Fatalf("expected error for nil data")
	}
}

func TestLoadSectionFromDatabaseInvalidInputs(t *testing.T) {
	t.Parallel()

	var dest map[string]string
	repo := &recordingRepo{}
	if err := LoadSectionFromDatabase(context.Background(), "", &dest, repo); err == nil {
		t.Fatalf("expected error for empty section")
	}
	if err := LoadSectionFromDatabase(context.Background(), "logging", nil, repo); err == nil {
		t.Fatalf("expected error for nil dest")
	}
}

// LoadFromDatabase must not panic on a nil repository; it should return a
// typed error so the caller can distinguish missing wiring from a DB outage.
func TestLoadFromDatabaseNilRepo(t *testing.T) {
	t.Parallel()

	type nilRepo interface {
		LoadFullConfig(ctx context.Context, dest interface{}) error
	}
	var repo nilRepo
	_, err := LoadFromDatabase(context.Background(), repo)
	if err == nil {
		t.Fatalf("expected error for nil repo")
	}
}

// SaveToDatabase likewise must reject nil inputs.
func TestSaveToDatabaseInvalidInputs(t *testing.T) {
	t.Parallel()

	type saveRepo interface {
		SaveFullConfig(ctx context.Context, cfg interface{}) error
	}
	var repo saveRepo
	if err := SaveToDatabase(context.Background(), validMinimalConfig(), repo); err == nil {
		t.Fatalf("expected error for nil repo")
	}
	// nil cfg is a programmer error and must be rejected explicitly.
	realRepo := &saveCaptureRepo{}
	if err := SaveToDatabase(context.Background(), nil, realRepo); err == nil {
		t.Fatalf("expected error for nil cfg")
	}
}

type saveCaptureRepo struct {
	saved *Config
	err   error
}

func (s *saveCaptureRepo) SaveFullConfig(_ context.Context, cfg interface{}) error {
	if s.err != nil {
		return s.err
	}
	if c, ok := cfg.(*Config); ok {
		s.saved = c
	}
	return nil
}

// ExportFromDatabaseToYAML must apply env overrides to the exported file so
// "configstore export" reflects the running process's effective config.
func TestExportFromDatabaseToYAMLAppliesEnv(t *testing.T) {
	t.Setenv("UA_DEFAULT_TMDB_API", "from-env")

	out := filepath.Join(t.TempDir(), "out.yaml")
	repo := &exportLoadRepo{cfg: Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "from-db"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}}
	configureConfigSecretEncryption(t, &repo.cfg)
	if err := ExportFromDatabaseToYAML(context.Background(), out, repo); err != nil {
		t.Fatalf("export: %v", err)
	}
	loaded, err := ImportFromYAML(out)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.MainSettings.TMDBAPI != "from-env" {
		t.Fatalf("env override missing: got %q", loaded.MainSettings.TMDBAPI)
	}
}

// A whitespace-only output path must be rejected. The original code trimmed
// spaces explicitly; this locks in that contract.
func TestExportFromDatabaseToYAMLWhitespacePath(t *testing.T) {
	t.Parallel()

	err := ExportFromDatabaseToYAML(context.Background(), "   ", &exportLoadRepo{})
	if err == nil {
		t.Fatalf("expected error for whitespace path")
	}
}
