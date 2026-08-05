// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyTagOverrides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tags.json")
	data := []byte(`{"SubsPlease":{"type":"WEBDL","source":"WEB","in_name":"SubsPlease","personalrelease":"true"},"Pasta":{"type":"WEBRIP"}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write tags json: %v", err)
	}

	t.Run("in-name override", func(t *testing.T) {
		t.Parallel()
		tag, override, err := ApplyTagOverrides("/media/SubsPlease.Show.mkv", "", path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "-SubsPlease" {
			t.Fatalf("expected tag -SubsPlease, got %q", tag)
		}
		if override == nil || override.Type != "WEBDL" || override.Source != "WEB" || !override.PersonalRelease {
			t.Fatalf("unexpected override: %+v", override)
		}
	})

	t.Run("explicit tag override", func(t *testing.T) {
		t.Parallel()
		tag, override, err := ApplyTagOverrides("/media/Pasta.Show.mkv", "-Pasta", path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "-Pasta" {
			t.Fatalf("expected tag -Pasta, got %q", tag)
		}
		if override == nil || override.Type != "WEBRIP" {
			t.Fatalf("unexpected override: %+v", override)
		}
	})
}

func TestEnsureDefaultTagOverridesCreatesBundledDefaults(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "db.sqlite")
	tagsPath, err := EnsureDefaultTagOverrides(dbPath)
	if err != nil {
		t.Fatalf("ensure default tag overrides: %v", err)
	}
	expectedPath := filepath.Join(filepath.Dir(dbPath), "tags.json")
	if tagsPath != expectedPath {
		t.Fatalf("tag overrides path got %q want %q", tagsPath, expectedPath)
	}

	data, err := os.ReadFile(tagsPath)
	if err != nil {
		t.Fatalf("read default tag overrides: %v", err)
	}
	entries := map[string]tagOverrideEntry{}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal default tag overrides: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("default tag override count got %d want 6", len(entries))
	}
	expected := map[string]tagOverrideEntry{
		"SubsPlease":   {Type: "WEBDL", Source: "WEB"},
		"Pasta":        {Type: "WEBRIP"},
		"Sootery":      {Type: "WEBRIP"},
		"YameteTomete": {Type: "WEBRIP"},
		"Golumpa":      {Type: "WEBDL"},
		"NanDesuKa":    {InName: "NanDesuKa"},
	}
	for key, want := range expected {
		got, ok := entries[key]
		if !ok {
			t.Fatalf("default tag override %q is missing", key)
		}
		if got != want {
			t.Fatalf("default tag override %q got %+v want %+v", key, got, want)
		}
	}

	tag, override, err := ApplyTagOverrides("[SubsPlease] Example Show - 01.mkv", "-SubsPlease", tagsPath)
	if err != nil {
		t.Fatalf("apply bundled SubsPlease override: %v", err)
	}
	if tag != "-SubsPlease" || override == nil || override.Type != "WEBDL" || override.Source != "WEB" {
		t.Fatalf("unexpected bundled SubsPlease override: tag=%q override=%+v", tag, override)
	}
}

func TestEnsureDefaultTagOverridesPreservesExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	tagsPath := filepath.Join(dir, "tags.json")
	existing := []byte(`{"Custom":{"type":"REMUX"}}`)
	if err := os.WriteFile(tagsPath, existing, 0o600); err != nil {
		t.Fatalf("write existing tag overrides: %v", err)
	}

	resolvedPath, err := EnsureDefaultTagOverrides(dbPath)
	if err != nil {
		t.Fatalf("ensure default tag overrides: %v", err)
	}
	if resolvedPath != tagsPath {
		t.Fatalf("tag overrides path got %q want %q", resolvedPath, tagsPath)
	}
	got, err := os.ReadFile(tagsPath)
	if err != nil {
		t.Fatalf("read existing tag overrides: %v", err)
	}
	if string(got) != string(existing) {
		t.Fatalf("existing tag overrides were modified")
	}
}

func TestEnsureDefaultTagOverridesConcurrentInitialization(t *testing.T) {
	t.Parallel()

	const initializerCount = 32

	largeValue := bytes.Repeat([]byte("X"), 8<<20)
	defaults := append([]byte(`{"Concurrent":{"type":"`), largeValue...)
	defaults = append(defaults, []byte(`"}}`)...)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	expectedPath := filepath.Join(dir, "tags.json")
	start := make(chan struct{})
	type result struct {
		path     string
		complete bool
		err      error
	}
	results := make(chan result, initializerCount)

	for range initializerCount {
		go func() {
			<-start
			path, err := ensureDefaultTagOverrides(dbPath, defaults)
			if err != nil {
				results <- result{err: err}
				return
			}
			data, err := os.ReadFile(path)
			results <- result{
				path:     path,
				complete: bytes.Equal(data, defaults),
				err:      err,
			}
		}()
	}

	close(start)
	for range initializerCount {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent ensure default tag overrides: %v", result.err)
		}
		if result.path != expectedPath {
			t.Fatalf("tag overrides path got %q want %q", result.path, expectedPath)
		}
		if !result.complete {
			t.Fatal("concurrent initializer observed incomplete default tag overrides")
		}
	}
}
