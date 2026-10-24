// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioOriginReviewKeepsLanguageAndSoundtrackEvidenceSeparate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		languages []string
		origin    api.TrackAudioOrigin
		want      bool
	}{
		{"ordinary original", []string{"English"}, "", false},
		{"mul tag", []string{"Multiple Languages"}, "", true},
		{"known mismatch", []string{"German"}, "", true},
		{"multilingual without origin review", []string{"English", "German"}, "", true},
		{"reviewed multilingual original", []string{"English", "German"}, api.TrackAudioOriginal, false},
		{"confirmed foreign dub", []string{"German"}, api.TrackAudioDubbed, false},
		{"explicit unresolved", []string{"English"}, api.TrackAudioUnresolved, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := api.LanguageFacts{
				OriginalLanguages:      []string{"English"},
				OriginalLanguagesKnown: true,
				Tracks: []api.MediaTrackFacts{{
					ID:                  "audio",
					ResourceID:          "resource",
					ManifestFingerprint: "scan",
					Kind:                api.MediaTrackAudio,
					Role:                api.AudioRoleProgramme,
					Languages:           tc.languages,
					AudioOrigin:         tc.origin,
				}},
			}
			got := UnresolvedProgrammeAudioOrigins(facts)
			if (len(got) == 1) != tc.want {
				t.Fatalf("origin review=%#v", got)
			}
			if tc.want && (got[0].ID != "audio" || got[0].ResourceID != "resource" || got[0].ManifestFingerprint != "scan") {
				t.Fatal("lost correction binding")
			}
			facts.Tracks[0].Role = api.AudioRoleCommentary
			if len(UnresolvedProgrammeAudioOrigins(facts)) != 0 {
				t.Fatal("commentary became programme origin review")
			}
		})
	}
}
