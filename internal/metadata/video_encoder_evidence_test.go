// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import "testing"

func TestVideoEncoderEvidenceDoesNotInferFromCodec(t *testing.T) {
	for _, test := range []struct{ library, want string }{{"", ""}, {"x264 core 164", "x264"}, {"x265 4.1", "x265"}, {"NVENC", "NVENC"}, {"SVT-AV1 3.0", "av1"}} {
		doc := mediaInfoDoc{}
		doc.Media.Track = []map[string]any{{
			"@type":           "Video",
			"Format":          "AVC",
			"Encoded_Library": test.library,
		}}
		if got := measuredVideoEncoder(doc); got != test.want {
			t.Fatalf("library %q: got %q want %q", test.library, got, test.want)
		}
	}
}
