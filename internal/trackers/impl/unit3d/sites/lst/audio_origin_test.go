// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioDefectsRequireConfirmedDub(t *testing.T) {
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioOriginal, api.TrackAudioDubbed} {
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{{
				ID:          "audio",
				Kind:        api.MediaTrackAudio,
				Role:        api.AudioRoleProgramme,
				Languages:   []string{"German"},
				AudioOrigin: origin,
			}},
		})
		defects := audioDefects(api.TrackerValidationSubject{LanguageFacts: facts})
		dub := slices.ContainsFunc(defects, func(d audioDefect) bool { return d.key == "extra_dub" || d.key == "non_original_non_english_dub" })
		if dub != (origin == api.TrackAudioDubbed) {
			t.Fatalf("origin=%q defects=%#v", origin, defects)
		}
	}
}
