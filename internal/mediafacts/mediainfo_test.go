// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

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
