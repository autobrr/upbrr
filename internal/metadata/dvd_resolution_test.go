// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

func TestExtractDVDMediaInfoFromVOBJSON(t *testing.T) {
	meta := preparationstate.State{
		DiscType:            "DVD",
		SourcePath:          `/releases/Movie.DVD`,
		DVDVOBMediaInfoJSON: `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"720","Height":"576","FrameRate":"25.000","ScanType":"Interlaced"}]}}`,
	}

	info := extractDVDMediaInfo(meta)
	if info.Width != 720 || info.Height != 576 {
		t.Fatalf("expected 720x576, got %dx%d", info.Width, info.Height)
	}
	if info.ScanType != "i" {
		t.Fatalf("expected scan i, got %q", info.ScanType)
	}
	if info.Resolution != "576i" {
		t.Fatalf("expected 576i, got %q", info.Resolution)
	}
}

func TestExtractDVDMediaInfoFallsBackToText(t *testing.T) {
	meta := preparationstate.State{
		DiscType:            "DVD",
		SourcePath:          `/releases/Movie.DVD`,
		DVDVOBMediaInfoJSON: `{"media":{"track":[{"@type":"General"},{"@type":"Video"}]}}`,
		DVDVOBMediaInfoText: "Width : 720\nHeight : 480\nFrame rate : 29.970\nScan type : Interlaced\n",
	}

	info := extractDVDMediaInfo(meta)
	if info.Width != 720 || info.Height != 480 {
		t.Fatalf("expected 720x480, got %dx%d", info.Width, info.Height)
	}
	if info.FrameRate != "29.970" {
		t.Fatalf("expected frame rate from text, got %q", info.FrameRate)
	}
	if info.ScanType != "i" {
		t.Fatalf("expected scan i, got %q", info.ScanType)
	}
	if info.Resolution != "480i" {
		t.Fatalf("expected 480i, got %q", info.Resolution)
	}
}

func TestExtractDVDMediaInfoUsesInterlacedHintFromSourcePath(t *testing.T) {
	meta := preparationstate.State{
		DiscType:            "DVD",
		SourcePath:          `/releases/Movie.1080i.DVD`,
		DVDVOBMediaInfoJSON: `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"1920","Height":"1080","FrameRate":"25.000"}]}}`,
	}

	info := extractDVDMediaInfo(meta)
	if info.ScanType != "i" {
		t.Fatalf("expected scan i from source hint, got %q", info.ScanType)
	}
	if info.Resolution != "1080i" {
		t.Fatalf("expected 1080i, got %q", info.Resolution)
	}
}

func TestExtractDVDMediaInfoDefaultsUnknownScanToProgressive(t *testing.T) {
	meta := preparationstate.State{
		DiscType:            "DVD",
		SourcePath:          `/releases/Movie.DVD`,
		DVDVOBMediaInfoJSON: `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"720","Height":"576","FrameRate":"25.000"}]}}`,
	}

	info := extractDVDMediaInfo(meta)
	if info.ScanType != "p" {
		t.Fatalf("expected unknown scan to default to p, got %q", info.ScanType)
	}
	if info.Resolution != "576p" {
		t.Fatalf("expected 576p, got %q", info.Resolution)
	}
}

func TestExtractDVDMediaInfoDoesNotOverrideJSONProgressiveScan(t *testing.T) {
	meta := preparationstate.State{
		DiscType:            "DVD",
		SourcePath:          `/releases/Movie.DVD`,
		DVDVOBMediaInfoJSON: `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"720","Height":"576","FrameRate":"25.000","ScanType":"Progressive"}]}}`,
		DVDVOBMediaInfoText: "Width : 720\nHeight : 576\nFrame rate : 25.000\nScan type : Interlaced\n",
	}

	info := extractDVDMediaInfo(meta)
	if info.ScanType != "p" {
		t.Fatalf("expected JSON progressive scan to be preserved, got %q", info.ScanType)
	}
	if info.Resolution != "576p" {
		t.Fatalf("expected 576p, got %q", info.Resolution)
	}
}

func TestResolutionFromMediaInfo(t *testing.T) {
	tests := []struct {
		name     string
		guessed  string
		payload  string
		expected string
	}{
		{
			name:     "1080p",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"1920","Height":"1080","ScanType":"Progressive"}]}}`,
			expected: "1080p",
		},
		{
			name:     "720p",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"1280","Height":"720","ScanType":"Progressive"}]}}`,
			expected: "720p",
		},
		{
			name:     "720x540p",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"720","Height":"540","ScanType":"Progressive"}]}}`,
			expected: "540p",
		},
		{
			name:     "960x540p",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"960","Height":"540","ScanType":"Progressive"}]}}`,
			expected: "540p",
		},
		{
			name:     "1080i",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"1920","Height":"1080","ScanType":"Interlaced"}]}}`,
			expected: "1080i",
		},
		{
			name:     "1080i MBAFF",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"1920","Height":"1080","ScanType":"MBAFF"}]}}`,
			expected: "1080i",
		},
		{
			name:     "2160p",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"3840","Height":"2160","ScanType":"Progressive"}]}}`,
			expected: "2160p",
		},
		{
			name:     "floors cropped dimensions",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"1916","Height":"800","ScanType":"Progressive"}]}}`,
			expected: "1080p",
		},
		{
			name:     "SD 540 height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"854","Height":"540","ScanType":"Progressive"}]}}`,
			expected: "540p",
		},
		{
			name:     "SD 576 height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"960","Height":"576","ScanType":"Progressive"}]}}`,
			expected: "576p",
		},
		{
			name:     "720 height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"960","Height":"720","ScanType":"Progressive"}]}}`,
			expected: "720p",
		},
		{
			name:     "1080 height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"960","Height":"1080","ScanType":"Progressive"}]}}`,
			expected: "1080p",
		},
		{
			name:     "1440 height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"1280","Height":"1440","ScanType":"Progressive"}]}}`,
			expected: "1440p",
		},
		{
			name:     "2160 height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"1920","Height":"2160","ScanType":"Progressive"}]}}`,
			expected: "2160p",
		},
		{
			name:     "height below 720 does not round up",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"960","Height":"719","ScanType":"Progressive"}]}}`,
			expected: "576p",
		},
		{
			name:     "cropped 1440 width",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"2560","Height":"1000","ScanType":"Progressive"}]}}`,
			expected: "1440p",
		},
		{
			name:     "cropped 4320 width",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"7680","Height":"2000","ScanType":"Progressive"}]}}`,
			expected: "4320p",
		},
		{
			name:     "cropped 8640 width",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"15360","Height":"4000","ScanType":"Progressive"}]}}`,
			expected: "8640p",
		},
		{
			name:     "1080 interlaced height floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"960","Height":"1080","ScanType":"Interlaced"}]}}`,
			expected: "1080i",
		},
		{
			name:     "unsupported 720 interlaced floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"960","Height":"720","ScanType":"Interlaced"}]}}`,
			expected: "",
		},
		{
			name:     "unsupported 2160 interlaced floor",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"1920","Height":"2160","ScanType":"Interlaced"}]}}`,
			expected: "",
		},
		{
			name:     "missing dimensions retain filename evidence",
			guessed:  "1080p",
			payload:  `{"media":{"track":[{"@type":"Video","Width":"0","Height":"720"}]}}`,
			expected: "1080p",
		},
		{
			name:     "empty on missing track",
			payload:  `{"media":{"track":[{"@type":"General"}]}}`,
			expected: "",
		},
		{
			name:     "empty on zero dimensions",
			payload:  `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"0","Height":"0"}]}}`,
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := resolutionFromMediaInfo(mustParseMediaInfoDoc(tc.payload), "/releases/Movie", tc.guessed)
			if res != tc.expected {
				t.Fatalf("resolution = %q, want %q", res, tc.expected)
			}
		})
	}
}

func TestResolutionFromMediaInfoEmptyOnSingleZeroDimension(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "zero width",
			payload: `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"0","Height":"1080"}]}}`,
		},
		{
			name:    "zero height",
			payload: `{"media":{"track":[{"@type":"General"},{"@type":"Video","Width":"1920","Height":"0"}]}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := resolutionFromMediaInfo(mustParseMediaInfoDoc(tc.payload), "/releases/Movie", "")
			if res != "" {
				t.Fatalf("expected empty when one dimension is zero, got %q", res)
			}
		})
	}
}

func mustParseMediaInfoDoc(payload string) mediaInfoDoc {
	doc, err := loadMediaInfoDocFromJSONPayload(payload)
	if err != nil {
		panic(err)
	}
	return doc
}
