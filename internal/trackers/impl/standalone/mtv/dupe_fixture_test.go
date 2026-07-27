// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMTVTorznabFixtureKeepsTitleSeparateFromFiles(t *testing.T) {
	t.Parallel()

	payloadBytes, err := os.ReadFile(filepath.Join("testdata", "torznab_attributes.xml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var payload mtvRSS
	if err := xml.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if payload.Channel.Response.Total != 2 || len(payload.Channel.Items) != 2 {
		t.Fatalf("pagination/items = %#v", payload.Channel)
	}
	entry, ok := mtvDupeEntry(payload.Channel.Items[0])
	if !ok {
		t.Fatal("fixture entry was not normalized")
	}
	if len(entry.Files) != 0 || entry.Res != "2160p" || entry.Source != "WEB-DL" {
		t.Fatalf("normalized entry = %#v", entry)
	}
	if entry.HDR.Origin != api.HDREvidenceTrackerAPI || entry.HDR.Status != api.HDREvidencePartial {
		t.Fatalf("MTV HDR evidence = %#v", entry.HDR)
	}
	if !slices.Equal(entry.HDR.Formats, []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus}) {
		t.Fatalf("MTV combined HDR formats = %#v", entry.HDR)
	}
}
