// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if entries[2].HDR.Status != api.HDREvidenceMissing || entries[2].HDR.Origin != api.HDREvidenceUnknown || len(entries[2].HDR.Formats) != 0 {
		t.Fatalf("missing MediaInfo = %#v", entries[2].HDR)
	}
	if entries[3].HDR.DolbyVisionProfile != "8.1" ||
		!slices.Equal(entries[3].HDR.Formats, []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}) {
		t.Fatalf("Dolby Vision MediaInfo = %#v", entries[3].HDR)
	}
}

func TestUnit3DEntriesDeriveCompleteHDRFromRecognizableTitles(t *testing.T) {
	t.Parallel()

	const (
		dvdName                   = "Example.Movie.2026.NTSC.DVD.REMUX.DD2.0-GRP"
		explicitResolutionDVDName = "Example.Movie.2026.480p.NTSC.DVD.X264.DD2.0-GRP"
	)
	searchEntries, dropped := buildUnit3DSearchEntries([]unit3dSearchItem{
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2026.2160p.WEB-DL.HEVC.DV.HDR10+-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2026.2160p.WEB-DL.HEVC-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "HDR10+"}},
		{Attributes: unit3dSearchAttrs{}},
		{Attributes: unit3dSearchAttrs{
			Name:       dvdName,
			Type:       "REMUX",
			Resolution: "480p",
		}},
		{Attributes: unit3dSearchAttrs{Name: dvdName, Type: "REMUX"}},
		{Attributes: unit3dSearchAttrs{
			Name:       explicitResolutionDVDName,
			Type:       "ENCODE",
			Resolution: "480p",
		}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2026.2160p.WEB-DL.HEVC.WCG-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2026.2160p.WEB-DL.HEVC.HDR.Vivid-GRP"}},
	}, 0, false)
	if dropped != 0 || len(searchEntries) != 9 {
		t.Fatalf("search entries=%d dropped=%d", len(searchEntries), dropped)
	}

	pendingEntries, dropped := buildUnit3DPendingEntries([]unit3dPendingSearchItem{
		{Name: "Example.Show.S01E01.2160p.WEB-DL.HEVC.HLG-GRP"},
		{Name: "Example.Show.S01E02.2160p.WEB-DL.HEVC.PQ10-GRP"},
		{Name: "Example.Show.S01E03.2160p.WEB-DL.HEVC-GRP"},
		{Name: "HLG"},
		{},
		{
			Name:       dvdName,
			Type:       "REMUX",
			Resolution: "480p",
		},
		{Name: dvdName, Resolution: "480p"},
		{
			Name:       explicitResolutionDVDName,
			Type:       "DISC",
			Resolution: "480p",
		},
		{Name: "Example.Show.S01E04.2160p.WEB-DL.HEVC.WCG-GRP"},
		{Name: "Example Show S01E05 2160p WEB-DL HEVC HDR Vivid-GRP"},
	}, unit3dSearchEndpoint{}, false)
	if dropped != 0 || len(pendingEntries) != 10 {
		t.Fatalf("pending entries=%d dropped=%d", len(pendingEntries), dropped)
	}

	completeTitleHDR := func(formats ...api.HDRFormat) api.HDRFacts {
		return api.HDRFacts{
			Formats:      formats,
			Origin:       api.HDREvidenceTrackerTitle,
			Status:       api.HDREvidenceComplete,
			SourceFields: []string{"title"},
		}
	}
	dvHDR10Plus := completeTitleHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus)
	dvHDR10Plus.FallbackFormats = []api.HDRFormat{api.HDRFormatHDR10Plus, api.HDRFormatHDR10}
	missing := api.HDRFacts{Origin: api.HDREvidenceUnknown, Status: api.HDREvidenceMissing}

	tests := []struct {
		name string
		got  api.HDRFacts
		want api.HDRFacts
	}{
		{
			name: "search explicit DV and HDR10+",
			got:  searchEntries[0].HDR,
			want: dvHDR10Plus,
		},
		{
			name: "search omitted markers",
			got:  searchEntries[1].HDR,
			want: completeTitleHDR(api.HDRFormatSDR),
		},
		{
			name: "search malformed title",
			got:  searchEntries[2].HDR,
			want: missing,
		},
		{
			name: "search blank title",
			got:  searchEntries[3].HDR,
			want: missing,
		},
		{
			name: "search DVD with structured type and resolution",
			got:  searchEntries[4].HDR,
			want: completeTitleHDR(api.HDRFormatSDR),
		},
		{
			name: "search DVD with unresolved structured resolution",
			got:  searchEntries[5].HDR,
			want: missing,
		},
		{
			name: "search explicit-resolution DVD non-remux",
			got:  searchEntries[6].HDR,
			want: completeTitleHDR(api.HDRFormatSDR),
		},
		{
			name: "search WCG",
			got:  searchEntries[7].HDR,
			want: completeTitleHDR(api.HDRFormatWCG),
		},
		{
			name: "search HDR Vivid",
			got:  searchEntries[8].HDR,
			want: completeTitleHDR(api.HDRFormatHDRVivid),
		},
		{
			name: "pending explicit HLG",
			got:  pendingEntries[0].HDR,
			want: completeTitleHDR(api.HDRFormatHLG),
		},
		{
			name: "pending explicit PQ10",
			got:  pendingEntries[1].HDR,
			want: completeTitleHDR(api.HDRFormatPQ10),
		},
		{
			name: "pending omitted markers",
			got:  pendingEntries[2].HDR,
			want: completeTitleHDR(api.HDRFormatSDR),
		},
		{
			name: "pending malformed title",
			got:  pendingEntries[3].HDR,
			want: missing,
		},
		{
			name: "pending blank title",
			got:  pendingEntries[4].HDR,
			want: missing,
		},
		{
			name: "pending DVD with structured type and resolution",
			got:  pendingEntries[5].HDR,
			want: completeTitleHDR(api.HDRFormatSDR),
		},
		{
			name: "pending DVD with unresolved structured type",
			got:  pendingEntries[6].HDR,
			want: missing,
		},
		{
			name: "pending explicit-resolution DVD non-remux",
			got:  pendingEntries[7].HDR,
			want: completeTitleHDR(api.HDRFormatSDR),
		},
		{
			name: "pending WCG",
			got:  pendingEntries[8].HDR,
			want: completeTitleHDR(api.HDRFormatWCG),
		},
		{
			name: "pending HDR Vivid",
			got:  pendingEntries[9].HDR,
			want: completeTitleHDR(api.HDRFormatHDRVivid),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !reflect.DeepEqual(test.got, test.want) {
				t.Fatalf("HDR = %#v, want %#v", test.got, test.want)
			}
		})
	}
}

func TestUnit3DTitleHDRFallbackUsesBoundedMetadata(t *testing.T) {
	t.Parallel()

	searchEntries, dropped := buildUnit3DSearchEntries([]unit3dSearchItem{
		{Attributes: unit3dSearchAttrs{Name: "HDR.Movie.2026.2160p.WEB-DL.HEVC-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "WCG.Movie.2026.2160p.WEB-DL.HEVC-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "HDRVivid.Movie.2026.2160p.WEB-DL.HEVC-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2026.2160p.WEB-DL.HEVC-HDR"}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2026.2160p.WEB-DL.HEVC.HDR.Vivid-GRP"}},
		{Attributes: unit3dSearchAttrs{Name: "Example.Movie.2160p.WEB-DL.HEVC.HDR10-GRP"}},
		{Attributes: unit3dSearchAttrs{
			Name:      "HDR.Movie.2026.2160p.WEB-DL.HEVC.WCG-GRP",
			MediaInfo: "Video\nFormat : HEVC\nHDR format : HDR10+",
		}},
	}, 0, false)
	if dropped != 0 || len(searchEntries) != 7 {
		t.Fatalf("search entries=%d dropped=%d", len(searchEntries), dropped)
	}

	pendingEntries, dropped := buildUnit3DPendingEntries([]unit3dPendingSearchItem{
		{Name: "HDR.Show.S01E01.2160p.WEB-DL.HEVC-GRP"},
		{Name: "WCG.Show.S01E02.2160p.WEB-DL.HEVC-GRP"},
		{Name: "HDRVivid.Show.S01E03.2160p.WEB-DL.HEVC-GRP"},
		{Name: "Example.Show.S01E04.2160p.WEB-DL.HEVC-HDRVivid"},
		{Name: "Example.Show.S01E05.2160p.WEB-DL.HEVC.WCG-GRP"},
		{Name: "Example.Show.2160p.WEB-DL.HEVC.HDR10-GRP"},
		{
			Name:      "WCG.Show.S01E06.2160p.WEB-DL.HEVC.HDR.Vivid-GRP",
			MediaInfo: "Video\nFormat : HEVC\nHDR format : HDR10",
		},
	}, unit3dSearchEndpoint{}, false)
	if dropped != 0 || len(pendingEntries) != 7 {
		t.Fatalf("pending entries=%d dropped=%d", len(pendingEntries), dropped)
	}

	assertTitleHDR := func(label string, got api.HDRFacts, formats ...api.HDRFormat) {
		t.Helper()
		want := api.HDRFacts{
			Formats:      formats,
			Origin:       api.HDREvidenceTrackerTitle,
			Status:       api.HDREvidenceComplete,
			SourceFields: []string{"title"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s HDR = %#v, want %#v", label, got, want)
		}
	}
	for index, label := range []string{"HDR title word", "WCG title word", "HDR Vivid title word", "HDR release group"} {
		assertTitleHDR("search "+label, searchEntries[index].HDR, api.HDRFormatSDR)
	}
	assertTitleHDR("search post-boundary HDR Vivid", searchEntries[4].HDR, api.HDRFormatHDRVivid)
	if got := searchEntries[5].HDR; got.Status != api.HDREvidenceMissing || got.Origin != api.HDREvidenceUnknown {
		t.Fatalf("search boundary-absent HDR = %#v", got)
	}
	if got := searchEntries[6].HDR; got.Status != api.HDREvidenceComplete || got.Origin != api.HDREvidenceMediaInfo ||
		!slices.Equal(got.Formats, []api.HDRFormat{api.HDRFormatHDR10Plus}) {
		t.Fatalf("search structured HDR = %#v", got)
	}

	for index, label := range []string{"HDR title word", "WCG title word", "HDR Vivid title word", "HDR Vivid release group"} {
		assertTitleHDR("pending "+label, pendingEntries[index].HDR, api.HDRFormatSDR)
	}
	assertTitleHDR("pending post-boundary WCG", pendingEntries[4].HDR, api.HDRFormatWCG)
	if got := pendingEntries[5].HDR; got.Status != api.HDREvidenceMissing || got.Origin != api.HDREvidenceUnknown {
		t.Fatalf("pending boundary-absent HDR = %#v", got)
	}
	if got := pendingEntries[6].HDR; got.Status != api.HDREvidenceComplete || got.Origin != api.HDREvidenceMediaInfo ||
		!slices.Equal(got.Formats, []api.HDRFormat{api.HDRFormatHDR10}) {
		t.Fatalf("pending structured HDR = %#v", got)
	}
}

func TestUnit3DTitleHDRRecognitionUsesMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		want api.HDREvidenceStatus
	}{
		{name: "DVD Movie 2026 REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "1080p Movie 2026 REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 REMUX AVC-DVD", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 MyDVD REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 DVD Remuxed AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 2160p AVC-OTHER", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 2160p REMUX AVC-OTHER", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 2160p MyBluRay REMUX AVC-OTHER", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 2160p AVC-DVD", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 2160p BluRay REMUX-GRP", want: api.HDREvidenceMissing},
		{name: "Example Movie 2026 2160p BluRay REMUX AVC-OTHER", want: api.HDREvidenceComplete},
		{name: "Alpha 2026 Extended / Beta 2025 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Alpha 2026 / Beta 2025 Extended Cut 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Face/Off 2026 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceComplete},
		{name: "Example Movie 2026 1080p BluRay REMUX AVC/DD2.0-GRP", want: api.HDREvidenceComplete},
		{name: "Example Movie 2026 NTSC DVD REMUX AVC-GRP", want: api.HDREvidenceComplete},
		{name: "Example Show S01E02 HDR 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show 2026 S01E02 Final Cut 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show S01E02 Example Episode 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show S01E00 Final Cut 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show S00 Final Cut Interviews 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show S01 Extras HDR 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show S01 OVA Final Cut 1080p BluRay REMUX AVC-GRP", want: api.HDREvidenceMissing},
		{name: "Example Show S01E02 1080p BluRay REMUX HDR AVC-GRP", want: api.HDREvidenceComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			search, _ := buildUnit3DSearchEntries([]unit3dSearchItem{{Attributes: unit3dSearchAttrs{
				Name:       test.name,
				Type:       "REMUX",
				Resolution: "480p",
			}}}, 0, false)
			pending, _ := buildUnit3DPendingEntries([]unit3dPendingSearchItem{{
				Name:       test.name,
				Type:       "REMUX",
				Resolution: "480p",
			}}, unit3dSearchEndpoint{}, false)
			for _, entry := range []api.DupeEntry{search[0], pending[0]} {
				if entry.HDR.Status != test.want {
					t.Fatalf("HDR = %#v, want status %s", entry.HDR, test.want)
				}
			}
		})
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
		Name:       "Example.Release.2026.2160p.WEB-DL.H.265-OTHER",
		Type:       "WEB-DL",
		Resolution: "2160p",
		MediaInfo:  "Video\nFormat : HEVC\nHDR format : HDR10",
		HDRDV:      &explicitSDR,
		Provider:   "PROVIDER",
	}}}, 0, false)
	if dropped != 0 || len(entries) != 1 {
		t.Fatalf("entries=%d dropped=%d", len(entries), dropped)
	}
	target := api.TrackerDuplicateTarget{
		Names:       []string{"Example.Release.2026.2160p.WEB-DL.H.265-TARGET"},
		Type:        "WEBDL",
		Provider:    "PROVIDER",
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
		{Attributes: unit3dSearchAttrs{
			Type:      " WEB-DL ",
			Provider:  " PROVIDER ",
			MediaInfo: "Video\nFormat : HEVC",
		}},
		{Attributes: unit3dSearchAttrs{Type: "Special Type"}},
	}, 0, false)
	if dropped != 0 {
		t.Fatalf("wrong-work rows = %d", dropped)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Type != "WEB-DL" || entries[0].CanonicalType != "WEBDL" || entries[0].Provider != "PROVIDER" || entries[0].Codec != "HEVC" {
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
	if len(pending) != 1 || pending[0].Type != "WEB-RIP" || pending[0].CanonicalType != "WEBRIP" || pending[0].Codec != "AVC" ||
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
