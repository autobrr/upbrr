// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnit3DSearchEntriesDeriveHDRFromMediaInfo(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "impl", "unit3d", "testdata", "search_mediainfo_variants.json")
	payloadBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var payload unit3dSearchResponse
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	entries := buildUnit3DSearchEntries(payload.Data, false)
	if len(entries) != 4 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].HDR.Status != api.HDREvidenceComplete || entries[0].HDR.Origin != api.HDREvidenceMediaInfo ||
		!slices.Equal(entries[0].HDR.Formats, []api.HDRFormat{api.HDRFormatSDR}) {
		t.Fatalf("SDR MediaInfo = %#v", entries[0].HDR)
	}
	if entries[1].HDR.Status != api.HDREvidenceComplete ||
		!slices.Equal(entries[1].HDR.Formats, []api.HDRFormat{api.HDRFormatHDR10Plus}) {
		t.Fatalf("HDR10+ MediaInfo = %#v", entries[1].HDR)
	}
	if entries[2].HDR.Status != api.HDREvidenceMissing {
		t.Fatalf("missing MediaInfo = %#v", entries[2].HDR)
	}
	if entries[3].HDR.DolbyVisionProfile != "8.1" ||
		!slices.Equal(entries[3].HDR.Formats, []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}) {
		t.Fatalf("Dolby Vision MediaInfo = %#v", entries[3].HDR)
	}
}

func TestUnit3DSearchEntriesPreserveRawAndCanonicalTypes(t *testing.T) {
	t.Parallel()

	entries := buildUnit3DSearchEntries([]unit3dSearchItem{
		{Attributes: unit3dSearchAttrs{Type: " WEB-DL "}},
		{Attributes: unit3dSearchAttrs{Type: "Special Type"}},
	}, false)
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Type != "WEB-DL" || entries[0].CanonicalType != "WEBDL" {
		t.Fatalf("known Unit3D type = %#v", entries[0])
	}
	if entries[1].Type != "Special Type" || entries[1].CanonicalType != "" {
		t.Fatalf("unknown Unit3D type = %#v", entries[1])
	}

	pending := buildUnit3DPendingEntries(
		[]unit3dPendingSearchItem{{
			Type:      "WEB-RIP",
			MediaInfo: "Video\nFormat : AVC\nWidth : 1 920 pixels",
		}},
		unit3dSearchEndpoint{},
		false,
	)
	if len(pending) != 1 || pending[0].Type != "WEB-RIP" || pending[0].CanonicalType != "WEBRIP" ||
		!slices.Equal(pending[0].HDR.Formats, []api.HDRFormat{api.HDRFormatSDR}) {
		t.Fatalf("pending Unit3D type = %#v", pending)
	}
}
