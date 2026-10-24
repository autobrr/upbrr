// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReviewedOriginalDoesNotRequireDialectException(t *testing.T) {
	facts := mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "English",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{{
			ID:          "audio",
			Kind:        api.MediaTrackAudio,
			Role:        api.AudioRoleProgramme,
			Languages:   []string{"German", "English"},
			AudioOrigin: api.TrackAudioOriginal,
		}},
	})
	if got := azDialectLanguages(api.TrackerValidationSubject{Type: "ENCODE", LanguageFacts: facts}); len(got) != 0 {
		t.Fatalf("reviewed original required dialect exception: %v", got)
	}
	facts.Tracks[0].AudioOrigin = ""
	if got := azDialectLanguages(api.TrackerValidationSubject{Type: "ENCODE", LanguageFacts: facts}); len(got) != 1 || got[0] != "German" {
		t.Fatalf("existing dialect review lost: %v", got)
	}
}

func TestSatisfiedRemuxOriginalOrEnglishDoesNotRequireExtraOrigin(t *testing.T) {
	for _, language := range []string{"Japanese", "English"} {
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{
				{
					ID:        "accepted",
					Kind:      api.MediaTrackAudio,
					Role:      api.AudioRoleProgramme,
					Languages: []string{language},
				},
				{
					ID:        "extra",
					Kind:      api.MediaTrackAudio,
					Role:      api.AudioRoleProgramme,
					Languages: []string{"German"},
				},
			},
		})
		for _, failure := range evaluateAZLanguageRules(siteFor("AZ"), api.TrackerValidationSubject{Type: "REMUX", LanguageFacts: facts}) {
			if failure.Rule == "language_origin" || failure.Rule == "language_original_or_english" || failure.Rule == "language_regional_dialect" {
				t.Fatalf("accepted %s remux audio gained unnecessary origin question: %#v", language, failure)
			}
		}
	}
}
