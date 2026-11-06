// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTitleLanguageSetDoesNotInventOrEraseSoundtracks(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		title                []string
		tracks               []api.MediaTrackFacts
		original, englishDub bool
	}{
		{"one tag matches one dialogue language", []string{"de", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French"}}}, true, false},
		{"multilingual original", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French", "English"}}}, true, false},
		{"separate English dub", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French"}}, {Languages: []string{"English"}, AudioOrigin: api.TrackAudioDubbed}}, true, true},
		{"matching confirmed dub", []string{"fr"}, []api.MediaTrackFacts{{Languages: []string{"French"}, AudioOrigin: api.TrackAudioDubbed}}, false, false},
		{"every tag needs matching evidence", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French", "German"}}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.tracks {
				tc.tracks[i].Kind = api.MediaTrackAudio
				tc.tracks[i].Role = api.AudioRoleProgramme
			}
			media := api.MediaFacts{
				OriginalLanguage:      "en",
				TitleLanguages:        tc.title,
				TrackCoverageComplete: true,
				Tracks:                tc.tracks,
			}
			facts := ResolveLanguages(media)
			if facts.HasOriginalAudio() != tc.original || facts.HasEnglishDub() != tc.englishDub || len(facts.Tracks) != len(tc.tracks) {
				t.Fatalf("title evidence changed soundtrack classification: %#v", facts)
			}
			for i := range tc.tracks {
				if !slices.Equal(facts.Tracks[i].Languages, tc.tracks[i].Languages) {
					t.Fatal("provider languages populated inspected audio")
				}
			}
		})
	}
}
