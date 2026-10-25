// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestRateControlFromExplicitEncoderSettings(t *testing.T) {
	for _, tc := range []struct {
		name, library, settings string
		want                    api.VideoRateControl
	}{
		{"x264 CRF", "x264", "ref=4 / rc=crf / crf=18.0", api.VideoRateControlCRF},
		{"x264 two pass", "x264 core 164", "rc=2pass / bitrate=4000", api.VideoRateControlTwoPassVBR},
		{"x264 ABR", "x264", "rc=abr / bitrate=4000", api.VideoRateControlABR},
		{"x264 CBR", "x264", "rc=cbr / bitrate=4000", api.VideoRateControlCBR},
		{"x264 CQP", "x264", "rc=cqp / qp=20", api.VideoRateControlCQP},
		{"x265 CRF", "x265", "rc=crf / crf=18 / stats-read=0 / stats-write=0", api.VideoRateControlCRF},
		{"x265 raw final pass flag", "x265", "rc=abr / stats-read=2 / stats-write=0", api.VideoRateControlMultiPassVBR},
		{"x265 final two pass", "x265", "rc=abr / bitrate=4000 / stats-read=1 / stats-write=0", api.VideoRateControlMultiPassVBR},
		{"x265 first pass", "x265", "rc=abr / bitrate=4000 / stats-read=0 / stats-write=1", api.VideoRateControlABR},
		{"x265 missing pass facts", "x265", "rc=abr / bitrate=4000", ""},
		{"x265 intermediate pass", "x265", "rc=abr / stats-read=1 / stats-write=1", ""},
		{"x265 conflicting CRF pass", "x265", "rc=crf / stats-read=1 / stats-write=0", ""},
		{"generic VBR", "x264", "bitrate_mode=VBR", ""},
		{"settings presence only", "x264", "ref=4 / cabac=1", ""},
		{"missing library", "", "rc=crf", ""},
		{"other encoder", "other", "rc=crf", ""},
		{"unknown mode", "x264", "rc=vbr", ""},
		{"conflicting modes", "x264", "rc=crf / rc=cqp", ""},
		{"conflicting values", "x264", "rc=crf / bitrate=4000", ""},
		{"malformed token", "x264", "rc = crf", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc MediaInfoDocument
			doc.Media.Track = []map[string]any{{
				"@type":                    "Video",
				"Encoded_Library_Name":     tc.library,
				"Encoded_Library_Settings": tc.settings,
				"BitRate_Mode":             "VBR",
			}}
			if got := VideoRateControlFromMediaInfo(doc); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRateControlRequiresUnambiguousVideo(t *testing.T) {
	var doc MediaInfoDocument
	video := map[string]any{
		"@type":                    "Video",
		"Format":                   "AVC",
		"Encoded_Library":          "x264 core 164",
		"Encoded_Library_Settings": "rc=crf",
	}
	doc.Media.Track = []map[string]any{video}
	if got := VideoRateControlFromMediaInfo(doc); got != api.VideoRateControlCRF {
		t.Fatalf("library fallback: %q", got)
	}
	video["Encoded_Library_Name"] = "x265"
	if got := VideoRateControlFromMediaInfo(doc); got != "" {
		t.Fatalf("conflicting library fields: %q", got)
	}
	delete(video, "Encoded_Library_Name")
	doc.Media.Track = append(doc.Media.Track, video)
	if got := VideoRateControlFromMediaInfo(doc); got != "" {
		t.Fatalf("multiple videos: %q", got)
	}
	doc.Media.Track = []map[string]any{video}
	video["Format"] = "HEVC"
	if got := VideoRateControlFromMediaInfo(doc); got != "" {
		t.Fatalf("contradictory codec: %q", got)
	}
	doc.Media.Track = nil
	if got := VideoRateControlFromMediaInfo(doc); got != "" {
		t.Fatalf("missing video: %q", got)
	}
}
