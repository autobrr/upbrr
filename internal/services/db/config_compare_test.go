// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestConfigRewrapRejectsConcurrentSettingsChange(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "config.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	original := map[string]any{"settings": map[string]any{"value": "original"}}
	if err := first.SaveFullConfig(ctx, original); err != nil {
		t.Fatal(err)
	}
	var snapshot json.RawMessage
	if err := first.LoadFullConfig(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}
	changed := map[string]any{"settings": map[string]any{"value": "newer"}}
	if err := second.SaveFullConfig(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveFullConfigIfUnchanged(ctx, original, snapshot); !errors.Is(err, api.ErrConfigActivationChanged) {
		t.Fatalf("stale replacement error = %v", err)
	}
	var retained map[string]map[string]string
	if err := first.LoadFullConfig(ctx, &retained); err != nil {
		t.Fatal(err)
	}
	if retained["settings"]["value"] != "newer" {
		t.Fatal("stale replacement overwrote newer settings")
	}
	if err := first.LoadFullConfig(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveFullConfigIfUnchanged(ctx, original, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := first.LoadFullConfig(ctx, &retained); err != nil {
		t.Fatal(err)
	}
	if retained["settings"]["value"] != "original" {
		t.Fatal("matching snapshot was not replaced")
	}
}
