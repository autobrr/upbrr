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
		name                                  string
		originalLanguage                      string
		title                                 []string
		tracks                                []api.MediaTrackFacts
		wantOriginalLanguages                 []string
		manual                                api.FactProvenance
		original, englishDub, englishOriginal bool
	}{
		{"one tag matches unranked dialogue", "en", []string{"de", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French"}}}, []string{"German", "French"}, "", true, false, false},
		{"unranked multilingual original", "ja", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French", "English"}}}, []string{"English", "French"}, "", true, false, false},
		{"separate confirmed English dub", "fr", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French"}}, {Languages: []string{"English"}, AudioOrigin: api.TrackAudioDubbed}}, []string{"French"}, "", true, true, false},
		{"matching confirmed dub", "fr", []string{"fr"}, []api.MediaTrackFacts{{Languages: []string{"French"}, AudioOrigin: api.TrackAudioDubbed}}, []string{"French"}, "", false, false, false},
		{"every tag needs matching evidence", "ja", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"French", "German"}}}, []string{"English", "French"}, "", false, false, false},
		{"English original despite other dialogue", "en", []string{"en", "fr"}, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{"English"}, "", true, false, true},
		{"agreed Japanese makes English-only a dub", "ja", []string{"ja", "en"}, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{"Japanese"}, "", false, true, false},
		{"agreed Japanese keeps dual audio", "Japanese", []string{"en", "ja"}, []api.MediaTrackFacts{{Languages: []string{"Japanese"}}, {Languages: []string{"English"}}}, []string{"Japanese"}, "", true, true, false},
		{"single spoken language beats scalar", "en", []string{"ja"}, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{"Japanese"}, "", false, true, false},
		{"single English beats Japanese scalar", "ja", []string{"en"}, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{"English"}, "", true, false, true},
		{"explicit English original survives agreement", "ja", []string{"en", "ja"}, []api.MediaTrackFacts{{Languages: []string{"English"}, AudioOrigin: api.TrackAudioOriginal}}, []string{"Japanese"}, "", true, false, true},
		{"explicit unresolved survives agreement", "ja", []string{"en", "ja"}, []api.MediaTrackFacts{{Languages: []string{"English"}, AudioOrigin: api.TrackAudioUnresolved}}, []string{"Japanese"}, "", false, false, false},
		{"verified multilingual original survives agreement", "ja", []string{"en", "ja"}, []api.MediaTrackFacts{{Languages: []string{"English", "Japanese"}, AudioOrigin: api.TrackAudioOriginal}}, []string{"Japanese"}, "", true, false, false},
		{"scalar-only mismatch stays unresolved", "ja", nil, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{"Japanese"}, "", false, false, false},
		{"manual original overrides single spoken", "en", []string{"ja"}, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{"English"}, api.FactProvenanceManual, true, false, true},
		{"manual clear suppresses automatic evidence", "", []string{"en", "ja"}, []api.MediaTrackFacts{{Languages: []string{"English"}}}, []string{}, api.FactProvenanceManualEmpty, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.tracks {
				tc.tracks[i].Kind = api.MediaTrackAudio
				tc.tracks[i].Role = api.AudioRoleProgramme
			}
			media := api.MediaFacts{
				OriginalLanguage:           tc.originalLanguage,
				OriginalLanguageProvenance: tc.manual,
				TitleLanguages:             tc.title,
				TrackCoverageComplete:      true,
				Tracks:                     tc.tracks,
			}
			facts := ResolveLanguages(media)
			if !slices.Equal(facts.OriginalLanguages, tc.wantOriginalLanguages) {
				t.Fatalf("original languages = %v, want %v", facts.OriginalLanguages, tc.wantOriginalLanguages)
			}
			if facts.HasOriginalAudio() != tc.original || facts.HasEnglishDub() != tc.englishDub || facts.HasEnglishOriginalAudio() != tc.englishOriginal || len(facts.Tracks) != len(tc.tracks) {
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
