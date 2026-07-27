// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUnit3DFlagFixturePreservesAbsentVersusEmpty(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "impl", "unit3d", "testdata", "search_flags_variants.json")
	payloadBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var payload unit3dSearchResponse
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	entries := buildUnit3DSearchEntries(payload.Data, false)
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	if !entries[0].FlagsPresent || !entries[0].FlagsComplete {
		t.Fatalf("non-empty flags = %#v", entries[0])
	}
	if !entries[1].FlagsPresent || entries[1].FlagsComplete {
		t.Fatalf("empty flags = %#v", entries[1])
	}
	if entries[2].FlagsPresent || entries[2].FlagsComplete {
		t.Fatalf("absent flags = %#v", entries[2])
	}
}
