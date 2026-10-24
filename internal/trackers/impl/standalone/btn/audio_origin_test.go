// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDubFindingRequiresReviewedProgrammeOrigin(t *testing.T) {
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioUnresolved, api.TrackAudioOriginal, api.TrackAudioDubbed} {
		t.Run(string(origin), func(t *testing.T) {
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "English",
				TrackCoverageComplete: true,
				Tracks: []api.MediaTrackFacts{{
					ID:                  "programme",
					ManifestFingerprint: "scan",
					Kind:                api.MediaTrackAudio,
					Role:                api.AudioRoleProgramme,
					Languages:           []string{"German"},
					AudioOrigin:         origin,
				}},
			})
			subject := api.TrackerValidationSubject{Type: "ENCODE", LanguageFacts: facts}
			failures := languageAssessment(subject)
			got := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_staff_dub" })
			if got != (origin == api.TrackAudioDubbed) {
				t.Fatalf("origin %q failures=%#v", origin, failures)
			}
			if origin == "" || origin == api.TrackAudioUnresolved {
				if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
					return f.Rule == "language_origin" && slices.Contains(f.AudioOriginCorrectionTrackIDs, "programme")
				}) {
					t.Fatalf("missing source-bound origin review: %#v", failures)
				}
			}
		})
	}
}

func TestOptionalDubOriginDoesNotAddStrictQuestion(t *testing.T) {
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioUnresolved, api.TrackAudioDubbed} {
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{
				{
					ID:        "original",
					Kind:      api.MediaTrackAudio,
					Role:      api.AudioRoleProgramme,
					Languages: []string{"Japanese"},
				},
				{
					ID:          "extra",
					Kind:        api.MediaTrackAudio,
					Role:        api.AudioRoleProgramme,
					Languages:   []string{"German"},
					AudioOrigin: origin,
				},
			},
		})
		failures := languageAssessment(api.TrackerValidationSubject{Type: "ENCODE", LanguageFacts: facts})
		optional := false
		for _, failure := range failures {
			if failure.Rule == "language_origin" || failure.Rule == "language_staff_dub" || failure.Rule == "language_original_pack" {
				t.Fatalf("origin %q added unnecessary origin gate: %#v", origin, failure)
			}
			if failure.Rule == "language_optional_dub" {
				optional = true
				if failure.Disposition != api.RuleDispositionAdvisory {
					t.Fatalf("optional dub became strict: %#v", failure)
				}
			}
		}
		if optional != (origin == api.TrackAudioDubbed) {
			t.Fatalf("origin %q optional dub=%v", origin, optional)
		}
	}
}
