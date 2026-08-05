// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDHDRFixturePreservesIndependentFields(t *testing.T) {
	t.Parallel()

	payloadBytes, err := os.ReadFile(filepath.Join("testdata", "search_hdr_variants.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	entries := bhdEntries(payload)
	if len(entries) != 8 {
		t.Fatalf("entries = %d", len(entries))
	}
	if !hasBHDFormat(entries[0].HDR, api.HDRFormatDolbyVision) ||
		!hasBHDFormat(entries[2].HDR, api.HDRFormatHDR10Plus) ||
		!hasBHDFormat(entries[5].HDR, api.HDRFormatHLG) {
		t.Fatalf("structured HDR variants = %#v %#v %#v", entries[0].HDR, entries[2].HDR, entries[5].HDR)
	}
	if entries[6].HDR.Status != api.HDREvidenceComplete || !hasBHDFormat(entries[6].HDR, api.HDRFormatSDR) {
		t.Fatalf("structured SDR = %#v", entries[6].HDR)
	}
	if entries[7].HDR.Status != api.HDREvidencePartial {
		t.Fatalf("malformed field status = %#v", entries[7].HDR)
	}
}

func hasBHDFormat(facts api.HDRFacts, want api.HDRFormat) bool {
	return slices.Contains(facts.Formats, want)
}
