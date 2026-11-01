// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDR10PlusTrackRequiresExplicitMediaInfoEvidence(t *testing.T) {
	for _, test := range []struct {
		name, tracks string
		want         bool
	}{
		{"HDR10+", `{"@type":"Video","Format":"HEVC","HDR_Format":"SMPTE ST 2094 App 4","HDR_Format_Compatibility":"HDR10+"}`, true},
		{"hybrid Dolby Vision", `{"@type":"Video","Format":"HEVC","HDR_Format":"Dolby Vision / HDR10+","HDR_Format_Compatibility":"HDR10"}`, true},
		{"SDR", `{"@type":"Video","Format":"HEVC"}`, false},
		{"static HDR", `{"@type":"Video","Format":"HEVC","HDR_Format":"HDR10"}`, false},
		{"Dolby Vision only", `{"@type":"Video","Format":"HEVC","HDR_Format":"Dolby Vision"}`, false},
		{"container claim", `{"@type":"General","Format":"HEVC","HDR_Format":"HDR10+"},{"@type":"Video","Format":"HEVC"}`, false},
		{"wrong codec", `{"@type":"Video","Format":"AVC","HDR_Format":"HDR10+"}`, false},
		{"ambiguous HEVC", `{"@type":"Video","Format":"HEVC","HDR_Format":"HDR10+"},{"@type":"Video","Format":"HEVC"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var doc MediaInfoDocument
			if err := json.Unmarshal([]byte(`{"media":{"track":[`+test.tracks+`]}}`), &doc); err != nil {
				t.Fatal(err)
			}
			if got := HDR10PlusTrackFromMediaInfo(doc); got != test.want {
				t.Fatalf("eligible=%v want=%v", got, test.want)
			}
		})
	}
}

func TestHDRFromMediaInfoText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		status   api.HDREvidenceStatus
		formats  []api.HDRFormat
		profile  string
		fallback []api.HDRFormat
	}{
		{
			name:    "usable SDR",
			text:    "General\nFormat : Matroska\nVideo\nFormat : AVC\nWidth : 1 920 pixels\nHeight : 1 080 pixels",
			status:  api.HDREvidenceComplete,
			formats: []api.HDRFormat{api.HDRFormatSDR},
		},
		{
			name:    "HDR10 plus",
			text:    "Video\nFormat : HEVC\nHDR format : SMPTE ST 2094 App 4, Version 1, HDR10+ Profile B compatible",
			status:  api.HDREvidenceComplete,
			formats: []api.HDRFormat{api.HDRFormatHDR10Plus},
		},
		{
			name:     "Dolby Vision with HDR10 compatibility",
			text:     "Video #1\nFormat : HEVC\nHDR format : Dolby Vision\nHDR format string : Dolby Vision, Profile 8.1, HDR10 compatible",
			status:   api.HDREvidenceComplete,
			formats:  []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			profile:  "8.1",
			fallback: []api.HDRFormat{api.HDRFormatHDR10},
		},
		{
			name:   "malformed",
			text:   "General\nFormat : Matroska",
			status: api.HDREvidenceMissing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			facts := HDRFromMediaInfoText(test.text)
			if facts.Status != test.status || !slices.Equal(facts.Formats, test.formats) ||
				facts.DolbyVisionProfile != test.profile || !slices.Equal(facts.FallbackFormats, test.fallback) {
				t.Fatalf("HDR facts = %#v", facts)
			}
			if test.status == api.HDREvidenceComplete && facts.Origin != api.HDREvidenceMediaInfo {
				t.Fatalf("origin = %q", facts.Origin)
			}
		})
	}
}

func TestVideoCodecFromMediaInfoText(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		text string
		want string
	}{
		{
			name: "format",
			text: "Video\nFormat : HEVC\nCodec ID : V_MPEGH/ISO/HEVC",
			want: "HEVC",
		},
		{
			name: "codec ID alone is not format evidence",
			text: "Video\nCodec ID : V_MPEG4/ISO/AVC",
		},
		{
			name: "MPEG family alone is not a codec",
			text: "Video\nFormat : MPEG Video\nFormat version : Version 2",
		},
		{
			name: "MPEG-4 Visual alone does not identify the encoder",
			text: "Video\nFormat : MPEG-4 Visual\nWriting library : XviD",
		},
		{name: "no video", text: "General\nFormat : Matroska"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := VideoCodecFromMediaInfoText(test.text); got != test.want {
				t.Fatalf("video codec = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHDRFromMediaInfoDocumentMatchesTextNormalization(t *testing.T) {
	t.Parallel()

	var doc MediaInfoDocument
	if err := json.Unmarshal([]byte(
		`{"media":{"track":[{"@type":"General"},{"@type":"Video","Format":"HEVC","HDR_Format":"HDR10+"}]}}`,
	), &doc); err != nil {
		t.Fatalf("decode MediaInfo JSON: %v", err)
	}

	facts := HDRFromMediaInfoDocument(doc)
	if facts.Status != api.HDREvidenceComplete || facts.Origin != api.HDREvidenceMediaInfo ||
		!slices.Equal(facts.Formats, []api.HDRFormat{api.HDRFormatHDR10Plus}) {
		t.Fatalf("HDR facts = %#v", facts)
	}
}

func TestEnglishSubtitleEvidenceRequiresTextTrack(t *testing.T) {
	for _, test := range []struct {
		report string
		want   bool
	}{
		{"General\nTitle : English Subtitles\nAudio\nLanguage : English", false},
		{"Text #1\nLanguage : English", true},
		{"Text #1\nLanguage : Japanese\nText #2\nLanguage : eng", true},
		{"Text\nLanguage : Japanese\nAudio\nLanguage : English", false},
		{"Text\nTitle : English", false},
	} {
		if got := EnglishSubtitlesFromMediaInfoText(test.report); got != test.want {
			t.Fatalf("%q: %v", test.report, got)
		}
	}
}

func TestEnglishSubtitleInspectionKnownState(t *testing.T) {
	const header = "General\nFormat : Matroska\nVideo\nFormat : AVC\n"
	for _, tc := range []struct {
		name, report   string
		english, known bool
	}{
		{"missing", "", false, false},
		{"negative", "General\nFormat : Matroska\nCount of text streams : 1\nVideo\nFormat : AVC\nText\nLanguage : Japanese\n", false, true},
		{"count unknown negative", header + "Text\nLanguage : Japanese\n", false, false},
		{"positive", "Text\nLanguage : English\n", true, true},
		{"regional English", "Text\nLanguage : en-US\n", true, true},
		{"unknown language", header + "Text\nLanguage : und\n", false, false},
		{"partial track", header + "Text #1\nLanguage : Japanese\nText #2\nFormat : UTF-8\n", false, false},
		{"partial report", "Text\nLanguage : Japanese\n", false, false},
		{"unproven absent", header, false, false},
		{"explicit absent", "General\nFormat : Matroska\nCount of text streams : 0\nVideo\nFormat : AVC\n", false, true},
		{"count mismatch", "General\nFormat : Matroska\nCount of text streams : 2\nVideo\nFormat : AVC\nText\nLanguage : Japanese\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			english, known := EnglishSubtitleEvidenceFromMediaInfoText(tc.report)
			if english != tc.english || known != tc.known {
				t.Fatalf("got (%t,%t), want (%t,%t)", english, known, tc.english, tc.known)
			}
		})
	}
}
