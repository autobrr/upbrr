// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

func TestNormalizeAudioFormatDolbyAliases(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		format string
		want   string
	}{
		{format: "DDP", want: "DD+"},
		{format: "E-AC3", want: "DD+"},
		{format: "E-AC-3", want: "DD+"},
		{format: "DD+", want: "DD+"},
		{format: "DD", want: "DD"},
		{format: "AC-3", want: "DD"},
		{format: " ddp ", want: "DD+"},
		{format: " dd ", want: "DD"},
	} {
		for _, commercial := range []string{"", "Audio"} {
			t.Run(test.format+"/"+commercial, func(t *testing.T) {
				t.Parallel()
				track := map[string]any{"Format": test.format, "Format_Commercial": commercial}
				if got := normalizeAudioFormat(track); got != test.want {
					t.Fatalf("normalized format = %q, want %q", got, test.want)
				}
			})
		}
	}
}

func TestNormalizeAudioFormatDolbyFallbacks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		track map[string]any
		want  string
	}{
		{
			name:  "format string alias",
			track: map[string]any{"Format_String": "E-AC3"},
			want:  "DD+",
		},
		{
			name:  "commercial name with alias",
			track: map[string]any{"Format": "DDP", "Format_Commercial_IfAny": "Dolby Digital Plus with Dolby Atmos"},
			want:  "DD+",
		},
		{
			name:  "commercial name retains precedence",
			track: map[string]any{"Format": "DD", "Format_Commercial": "Dolby Digital Plus"},
			want:  "DD+",
		},
		{
			name:  "generic format codec id",
			track: map[string]any{"Format": "Audio", "CodecID": "A_EAC3"},
			want:  "DD+",
		},
		{
			name:  "absent format codec id",
			track: map[string]any{"CodecID_Compatible": "A_AC3"},
			want:  "DD",
		},
		{
			name:  "unknown format containing alias",
			track: map[string]any{"Format": "NotDDP"},
			want:  "NotDDP",
		},
		{
			name:  "unknown format prefixed by alias",
			track: map[string]any{"Format": "DD+ Other"},
			want:  "DD+ Other",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeAudioFormat(test.track); got != test.want {
				t.Fatalf("normalized format = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAudioFromMediaDolbyAliasRetainsJOCAtmos(t *testing.T) {
	t.Parallel()
	var doc mediaInfoDoc
	doc.Media.Track = []map[string]any{{
		"@type":                     "Audio",
		"Format":                    "DDP",
		"Format_Commercial":         "Audio",
		"Format_AdditionalFeatures": "JOC",
		"Channels":                  "6",
		"ChannelLayout":             "L R C LFE Ls Rs",
	}}
	audio, channels, commentary := audioFromMedia(preparationstate.State{}, doc, nil)
	if audio != "DD+ 5.1 Atmos" || channels != "5.1" || commentary {
		t.Fatalf("audio=%q channels=%q commentary=%t, want DD+ 5.1 Atmos, 5.1, false", audio, channels, commentary)
	}
}
