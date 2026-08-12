// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTFlagFixturePreservesPartialSemantics(t *testing.T) {
	t.Parallel()

	payloadBytes, err := os.ReadFile(filepath.Join("testdata", "search_flags_variants.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	entries := antDupeEntries(payload)
	if len(entries) != 6 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].HDR.Status != api.HDREvidencePartial || entries[1].HDR.Status != api.HDREvidencePartial {
		t.Fatalf("ANT structured flag status = %#v %#v", entries[0].HDR, entries[1].HDR)
	}
	if entries[3].HDR.Status != api.HDREvidenceMissing || entries[4].HDR.Status != api.HDREvidenceMissing {
		t.Fatalf("ANT empty/missing flags = %#v %#v", entries[3].HDR, entries[4].HDR)
	}
}
