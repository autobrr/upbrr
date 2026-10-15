// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import "testing"

func TestDecodedHDRSourceEvidenceDoesNotGuess(t *testing.T) {
	video := map[string]any{
		"@type":                    "Video",
		"Format":                   "HEVC",
		"Width":                    "3840",
		"Height":                   "2160",
		"BitDepth":                 "10",
		"PixelAspectRatio":         "1.000",
		"colour_range":             "Limited",
		"colour_primaries":         "BT.2020",
		"transfer_characteristics": "PQ",
		"matrix_coefficients":      "BT.2020 non-constant",
		"FrameRate":                "23.976",
		"FrameRate_Num":            "24000",
		"FrameRate_Den":            "1001",
	}
	var doc MediaInfoDocument
	doc.Media.Track = []map[string]any{video}
	e := HDRSourceFromMediaInfo(doc)
	if e.Decoded == nil || e.Decoded.VideoStreams != 1 || e.Decoded.Width != 3840 || e.Decoded.Height != 2160 ||
		e.Decoded.PixelAspectNumerator != 1 || e.Decoded.PixelAspectDenominator != 1 || e.Decoded.Range != "limited" ||
		e.Decoded.FrameRateNumerator != "24000" || e.Decoded.FrameRateDenominator != "1001" {
		t.Fatalf("target probe evidence: %#v", e.Decoded)
	}
	doc.Media.Track = append(doc.Media.Track, map[string]any{"@type": "Video", "Format": "AVC"})
	e = HDRSourceFromMediaInfo(doc)
	if !e.UniqueHEVC || e.Decoded.VideoStreams != 2 {
		t.Fatal("unique native HEVC was confused with single decoder video")
	}
	doc.Media.Track[0], doc.Media.Track[1] = doc.Media.Track[1], doc.Media.Track[0]
	e = HDRSourceFromMediaInfo(doc)
	if !e.UniqueHEVC || e.Decoded.VideoStreamOrdinal != 1 {
		t.Fatal("the unique HEVC decoder stream ordinal was lost")
	}
	delete(video, "FrameRate_Num")
	delete(video, "FrameRate_Den")
	delete(video, "colour_range")
	delete(video, "PixelAspectRatio")
	e = HDRSourceFromMediaInfo(doc)
	if e.Decoded.FrameRateNumerator != "" || e.Decoded.FrameRateDenominator != "" || e.Decoded.Range != "" || e.Decoded.PixelAspectDenominator != 0 {
		t.Fatal("missing probe fields acquired defaults")
	}
}
