// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
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
	entries, dropped := buildUnit3DSearchEntries(payload.Data, 0, false)
	if dropped != 0 {
		t.Fatalf("wrong-work rows = %d", dropped)
	}
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

func TestUnit3DSearchEntriesPreferPresentHDRDVAndPreserveProvider(t *testing.T) {
	t.Parallel()

	var payload unit3dSearchResponse
	if err := json.Unmarshal([]byte(`{
		"data": [
			{"attributes": {"name": "Example.Release.2026.2160p.PROVIDER.WEB-DL-GRP", "hdr_dv": "DV P8 HDR", "provider": " PROVIDER ", "media_info": "Video\nFormat : HEVC\nHDR format : Dolby Vision\nHDR format string : Dolby Vision, Profile 8.1, HDR10 compatible"}},
			{"attributes": {"name": "Example.Release.2026.1080p.WEB-DL-GRP", "hdr_dv": "", "media_info": "Video\nHDR format : HDR10+"}},
			{"attributes": {"name": "Example.Release.2026.1080p.WEB-DL-GRP", "media_info": "Video\nHDR format : HDR10+"}},
			{"attributes": {"name": "Example.Release.2026.2160p.WEB-DL-GRP", "hdr_dv": "DV P9 HDR", "media_info": "Video\nHDR format : HDR10"}},
			{"attributes": {"name": "Example.Release.2026.2160p.WEB-DL-GRP", "hdr_dv": "HDR10"}},
			{"attributes": {"name": "Example.Release.2026.2160p.WEB-DL-GRP", "hdr_dv": "DV P7 HDR", "media_info": "Video\nFormat : HEVC\nHDR format : Dolby Vision\nHDR format string : Dolby Vision, Profile 8.1, HDR10 compatible"}}
		]
	}`), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	entries, dropped := buildUnit3DSearchEntries(payload.Data, 0, false)
	if dropped != 0 || len(entries) != 6 {
		t.Fatalf("entries=%d dropped=%d", len(entries), dropped)
	}
	if entries[0].Provider != "PROVIDER" || entries[0].HDR.Status != api.HDREvidenceComplete ||
		entries[0].HDR.Origin != api.HDREvidenceTrackerAPI || entries[0].HDR.DolbyVisionProfile != "8" ||
		!slices.Equal(entries[0].HDR.Formats, []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}) {
		t.Fatalf("structured entry = %#v", entries[0])
	}
	if entries[1].HDR.Status != api.HDREvidenceContradictory || entries[1].HDR.Origin != api.HDREvidenceTrackerAPI ||
		!slices.Equal(entries[1].HDR.SourceFields, []string{"hdr_dv", "media_info"}) || len(entries[1].HDR.Contradictions) != 1 ||
		!slices.Equal(entries[1].HDR.Formats, []api.HDRFormat{api.HDRFormatSDR}) {
		t.Fatalf("conflicting explicit SDR = %#v", entries[1].HDR)
	}
	if entries[2].HDR.Origin != api.HDREvidenceMediaInfo ||
		!slices.Equal(entries[2].HDR.Formats, []api.HDRFormat{api.HDRFormatHDR10Plus}) {
		t.Fatalf("MediaInfo fallback = %#v", entries[2].HDR)
	}
	if entries[3].HDR.Status != api.HDREvidencePartial || entries[3].HDR.Origin != api.HDREvidenceTrackerAPI ||
		len(entries[3].HDR.Formats) != 0 {
		t.Fatalf("unknown structured HDR = %#v", entries[3].HDR)
	}
	if entries[4].HDR.Status != api.HDREvidenceComplete || entries[4].HDR.Origin != api.HDREvidenceTrackerAPI ||
		!slices.Equal(entries[4].HDR.Formats, []api.HDRFormat{api.HDRFormatHDR10}) {
		t.Fatalf("structured HDR without MediaInfo = %#v", entries[4].HDR)
	}
	if entries[5].HDR.Status != api.HDREvidenceContradictory || entries[5].HDR.DolbyVisionProfile != "7" ||
		!slices.Equal(entries[5].HDR.Formats, []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}) {
		t.Fatalf("conflicting Dolby Vision profile = %#v", entries[5].HDR)
	}
}

func TestUnit3DSearchEntriesConflictingHDRRequiresReview(t *testing.T) {
	t.Parallel()

	explicitSDR := ""
	entries, dropped := buildUnit3DSearchEntries([]unit3dSearchItem{{Attributes: unit3dSearchAttrs{
		Name:       "Example.Release.2026.2160p.AMZN.WEB-DL.H.265-OTHER",
		Type:       "WEB-DL",
		Resolution: "2160p",
		MediaInfo:  "Video\nFormat : HEVC\nHDR format : HDR10",
		HDRDV:      &explicitSDR,
		Provider:   "AMZN",
	}}}, 0, false)
	if dropped != 0 || len(entries) != 1 {
		t.Fatalf("entries=%d dropped=%d", len(entries), dropped)
	}
	target := api.TrackerDuplicateTarget{
		Names:       []string{"Example.Release.2026.2160p.AMZN.WEB-DL.H.265-TARGET"},
		Type:        "WEBDL",
		Provider:    "AMZN",
		Resolution:  "2160p",
		VideoEncode: "H.265",
		HDR:         mediafacts.HDRFromMediaInfoText("Video\nFormat : HEVC\nHDR format : HDR10"),
	}
	policy := trackers.DupePolicy{
		ID:                                    "test/unit3d-hdr-conflict",
		SlotDimensions:                        []trackers.DupeDimension{trackers.DupeDimensionMediaKind, trackers.DupeDimensionResolution, trackers.DupeDimensionHDR},
		SlotContradictionsRequireManualReview: true,
	}
	result := dupe.Evaluate(
		target,
		[]dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "TEST")},
		policy,
		dupe.SearchEvidence{
			Complete:  true,
			Pages:     1,
			WorkScope: dupe.WorkScopeProviderID,
		},
	)
	if got := result.Candidates[0]; got.Relation != api.DupeRelationManualReview || !result.RequiresAction {
		t.Fatalf("conflicting HDR evaluation = %#v", result)
	}
}

func TestUnit3DSearchEntriesPreserveRawAndCanonicalTypes(t *testing.T) {
	t.Parallel()

	entries, dropped := buildUnit3DSearchEntries([]unit3dSearchItem{
		{Attributes: unit3dSearchAttrs{Type: " WEB-DL "}},
		{Attributes: unit3dSearchAttrs{Type: "Special Type"}},
	}, 0, false)
	if dropped != 0 {
		t.Fatalf("wrong-work rows = %d", dropped)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Type != "WEB-DL" || entries[0].CanonicalType != "WEBDL" {
		t.Fatalf("known Unit3D type = %#v", entries[0])
	}
	if entries[1].Type != "Special Type" || entries[1].CanonicalType != "" {
		t.Fatalf("unknown Unit3D type = %#v", entries[1])
	}

	pending, dropped := buildUnit3DPendingEntries(
		[]unit3dPendingSearchItem{{
			Type:      "WEB-RIP",
			MediaInfo: "Video\nFormat : AVC\nWidth : 1 920 pixels",
		}},
		unit3dSearchEndpoint{},
		false,
	)
	if dropped != 0 {
		t.Fatalf("pending wrong-work rows = %d", dropped)
	}
	if len(pending) != 1 || pending[0].Type != "WEB-RIP" || pending[0].CanonicalType != "WEBRIP" ||
		!slices.Equal(pending[0].HDR.Formats, []api.HDRFormat{api.HDRFormatSDR}) {
		t.Fatalf("pending Unit3D type = %#v", pending)
	}
}

func TestUnit3DSearchEntriesDropOnlyConflictingWorkIDs(t *testing.T) {
	t.Parallel()

	entries, dropped := buildUnit3DSearchEntries([]unit3dSearchItem{
		{Attributes: unit3dSearchAttrs{Name: "Example.Release.2026.1080p-GRP", TMDBID: 1234567}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Release.2026.2160p-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "Different.Work.2026.1080p-GRP", TMDBID: 7654321}},
	}, 1234567, false)
	if dropped != 1 || len(entries) != 2 {
		t.Fatalf("entries=%d dropped=%d, want 2/1", len(entries), dropped)
	}
}
