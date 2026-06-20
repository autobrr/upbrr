// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestImportFromContentYAMLOverlaysDefaults(t *testing.T) {
	yaml := []byte("main_settings:\n  tmdb_api: test-key\n")

	cfg, warnings, err := ImportFromContent("config.yaml", yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(warnings))
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.MainSettings.TMDBAPI != "test-key" {
		t.Fatalf("expected tmdb_api to be overwritten, got %q", cfg.MainSettings.TMDBAPI)
	}
	if cfg.MainSettings.TrackerPassChecks != 1 {
		t.Fatalf("expected tracker_pass_checks default to be preserved, got %d", cfg.MainSettings.TrackerPassChecks)
	}
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatal("expected tracker defaults to be merged in")
	}
}

func TestImportFromContentJSONOverlaysDefaults(t *testing.T) {
	// The export path (ExportToJSON) uses json.MarshalIndent which emits
	// Go field names because the config structs carry no json tags. The
	// import side now uses json.Unmarshal so the round-trip is symmetric.
	json := []byte(`{"MainSettings":{"TMDBAPI":"json-key"}}`)

	cfg, _, err := ImportFromContent("config.json", json)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "json-key" {
		t.Fatalf("expected TMDBAPI to be overwritten, got %q", cfg.MainSettings.TMDBAPI)
	}
	if cfg.MainSettings.TrackerPassChecks != 1 {
		t.Fatalf("expected TrackerPassChecks default to be preserved, got %d", cfg.MainSettings.TrackerPassChecks)
	}
	if len(cfg.Trackers.Trackers) == 0 {
		t.Fatal("expected tracker defaults to be merged in")
	}
}

func TestImportFromContentYAMLDoesNotKeepTemplateQbit(t *testing.T) {
	yaml := []byte(`
main_settings:
  tmdb_api: test-key
torrent_clients:
  watch-client:
    type: watch
    watch_folder: C:/Watch
`)

	cfg, _, err := ImportFromContent("config.yaml", yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.TorrentClients["qbittorrent"]; ok {
		t.Fatal("did not expect template qbittorrent client to be retained")
	}
	if _, ok := cfg.TorrentClients["watch-client"]; !ok {
		t.Fatal("watch-client not found")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("imported config should validate: %v", err)
	}
}

func TestImportFromContentJSONDoesNotKeepTemplateQbit(t *testing.T) {
	json := []byte(`{
  "MainSettings": {"TMDBAPI": "test-key"},
  "TorrentClients": {
    "watch-client": {
      "Type": "watch",
      "WatchFolder": "C:/Watch"
    }
  }
}`)

	cfg, _, err := ImportFromContent("config.json", json)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.TorrentClients["qbittorrent"]; ok {
		t.Fatal("did not expect template qbittorrent client to be retained")
	}
	if _, ok := cfg.TorrentClients["watch-client"]; !ok {
		t.Fatal("watch-client not found")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("imported config should validate: %v", err)
	}
}

func TestImportFromContentYAMLOmittedTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.yaml", []byte("main_settings:\n  tmdb_api: test-key\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected omitted torrent_clients to import as an empty map")
	}
	if len(cfg.TorrentClients) != 0 {
		t.Fatalf("expected no imported torrent clients, got %d", len(cfg.TorrentClients))
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported struct {
		TorrentClients map[string]config.TorrentClientConfig
	}
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if exported.TorrentClients == nil {
		t.Fatal("expected exported TorrentClients to be {}, got null")
	}
	if len(exported.TorrentClients) != 0 {
		t.Fatalf("expected exported TorrentClients to be empty, got %d", len(exported.TorrentClients))
	}
}

func TestImportFromContentYAMLNullTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.yaml", []byte("main_settings:\n  tmdb_api: test-key\ntorrent_clients: null\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected null torrent_clients to import as an empty map")
	}
	if len(cfg.TorrentClients) != 0 {
		t.Fatalf("expected no imported torrent clients, got %d", len(cfg.TorrentClients))
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if string(exported["TorrentClients"]) != "{}" {
		t.Fatalf("expected exported TorrentClients {}, got %s", exported["TorrentClients"])
	}
}

func TestImportFromContentJSONOmittedTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.json", []byte(`{"MainSettings":{"TMDBAPI":"json-key"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected omitted TorrentClients to import as an empty map")
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if string(exported["TorrentClients"]) != "{}" {
		t.Fatalf("expected exported TorrentClients {}, got %s", exported["TorrentClients"])
	}
}

func TestImportFromContentJSONNullTorrentClientsExportsEmptyMap(t *testing.T) {
	cfg, _, err := ImportFromContent("config.json", []byte(`{"TorrentClients":null}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TorrentClients == nil {
		t.Fatal("expected null TorrentClients to normalize to an empty map")
	}
	if len(cfg.TorrentClients) != 0 {
		t.Fatalf("expected no imported torrent clients, got %d", len(cfg.TorrentClients))
	}

	payload, err := config.ExportToPlaintextJSON(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if string(exported["TorrentClients"]) != "{}" {
		t.Fatalf("expected exported TorrentClients {}, got %s", exported["TorrentClients"])
	}
}

func TestImportFromContentUnsupportedExtension(t *testing.T) {
	_, _, err := ImportFromContent("config.txt", []byte("irrelevant"))
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("expected unsupported extension error, got %v", err)
	}
}

func TestImportFromContentMissingExtension(t *testing.T) {
	_, _, err := ImportFromContent("config", []byte("irrelevant"))
	if err == nil {
		t.Fatal("expected error when extension is missing")
	}
}

func TestImportFromContentRejectsOversize(t *testing.T) {
	data := make([]byte, MaxFileBytes+1)
	_, _, err := ImportFromContent("big.yaml", data)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}

func TestImportFromContentRoutesPythonToLegacy(t *testing.T) {
	py := []byte(`config = {"DEFAULT": {"tmdb_api": "py-key"}, "TRACKERS": {}, "TORRENT_CLIENTS": {}}`)

	cfg, _, err := ImportFromContent("config.py", py)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestImportFromFileReadsAndDispatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("main_settings:\n  tmdb_api: file-key\n"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cfg, _, err := ImportFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MainSettings.TMDBAPI != "file-key" {
		t.Fatalf("expected tmdb_api from file, got %q", cfg.MainSettings.TMDBAPI)
	}
}

func TestImportFromFileMissing(t *testing.T) {
	_, _, err := ImportFromFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestImportFromFileRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(path, make([]byte, MaxFileBytes+1), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, _, err := ImportFromFile(path)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}

func TestImportFromContentDisablesUnsupportedImageRehost(t *testing.T) {
	yaml := []byte("trackers:\n  trackers:\n    TL:\n      img_rehost: true\n")

	cfg, warnings, err := ImportFromContent("config.yaml", yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Trackers.Trackers["TL"].ImgRehost {
		t.Fatal("expected TL img_rehost to be disabled during import")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TL") && strings.Contains(w, "img_rehost") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning about TL img_rehost, got %v", warnings)
	}
}

func TestIsPythonFile(t *testing.T) {
	cases := map[string]bool{
		"config.py":     true,
		"config.PY":     true,
		"config.yaml":   false,
		"config":        false,
		"config.py.bak": false,
	}
	for name, want := range cases {
		if got := isPythonFile(name); got != want {
			t.Errorf("isPythonFile(%q) = %v, want %v", name, got, want)
		}
	}
}
