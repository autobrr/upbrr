// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefaultAudioNameUsesAvailableInspectedDefault(t *testing.T) {
	for _, test := range []struct {
		name           string
		tracks         []api.MediaTrackFacts
		absent, manual bool
		releaseType    string
		want           string
	}{
		{
			name: "default commentary is still default audio",
			tracks: []api.MediaTrackFacts{{
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleCommentary,
				Default:      true,
				DefaultKnown: true,
				Codec:        "DD",
				AudioLabel:   "AC-3 2.0",
			}},
			want: "AC-3 2.0",
		},
		{name: "missing evidence", want: "AAC 2.0"},
		{
			name: "no default",
			tracks: []api.MediaTrackFacts{{
				Kind:       api.MediaTrackAudio,
				Codec:      "DTS",
				AudioLabel: "DTS 5.1",
			}},
			want: "AAC 2.0",
		},
		{
			name: "matching defaults",
			tracks: []api.MediaTrackFacts{{
				Kind:         api.MediaTrackAudio,
				Default:      true,
				DefaultKnown: true,
				Codec:        "DD+",
				AudioLabel:   "DD+ 5.1",
			}, {
				Kind:         api.MediaTrackAudio,
				Default:      true,
				DefaultKnown: true,
				Codec:        "DD+",
				AudioLabel:   "DD+ 5.1",
			}},
			want: "DD+ 5.1",
		},
		{
			name: "two defaults",
			tracks: []api.MediaTrackFacts{{
				Kind:         api.MediaTrackAudio,
				Default:      true,
				DefaultKnown: true,
				Codec:        "DTS",
				AudioLabel:   "DTS 5.1",
			}, {
				Kind:         api.MediaTrackAudio,
				Default:      true,
				DefaultKnown: true,
				Codec:        "DD",
				AudioLabel:   "AC-3 2.0",
			}},
			want: "AAC 2.0",
		},
		{
			name:   "missing technical label",
			tracks: []api.MediaTrackFacts{{Kind: api.MediaTrackAudio, Default: true}},
			want:   "AAC 2.0",
		},
		{
			name:   "manual audio preserved",
			manual: true,
			want:   "AAC 2.0",
		},
		{
			name:        "full disc preserved",
			releaseType: "DISC",
			want:        "AAC 2.0",
		},
		{name: "inspected audio absence", absent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{{
				Role:    api.NameRoleAudio,
				Value:   "AAC 2.0",
				Present: true,
				Manual:  test.manual,
			}}}
			editor := &NameEditor{document: doc}
			err := ApplyDefaultAudioName(editor, api.UploadSubject{Type: test.releaseType, LanguageFacts: api.LanguageFacts{Tracks: test.tracks, AudioAbsent: test.absent}})
			if err != nil {
				t.Fatalf("default evidence blocked naming: %v", err)
			}
			audio, _ := editor.Component(api.NameRoleAudio)
			if test.absent {
				if audio.Present {
					t.Fatal("known absent audio retained stale technical label")
				}
				return
			}
			if audio.Value != test.want {
				t.Fatalf("audio=%q want=%q", audio.Value, test.want)
			}
		})
	}
}
