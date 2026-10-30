// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRegisteredLanguageStreamReviewsMatchBaseIdentities(t *testing.T) {
	registry := MustNewRegistry()
	for _, tc := range []struct{ tracker, rule string }{
		{"AITHER", "language_additional_main_audio"},
		{"AZ", "language_duplicate_language"},
		{"LST", "language_redundant_programme"},
		{"BHD", "language_redundant_original"},
		{"PTP", "language_redundant_audio"},
	} {
		t.Run(tc.tracker, func(t *testing.T) {
			descriptor, _ := registry.LookupDescriptor(tc.tracker)
			if descriptor.Validation.LanguageAssessment == nil {
				t.Fatal("registered language review is missing")
			}
			media := api.MediaFacts{
				OriginalLanguage:      "Japanese",
				TrackCoverageComplete: true,
				Tracks: []api.MediaTrackFacts{
					{
						ID:          "original",
						ResourceID:  "episode-1",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Codec:       "DTS",
						Languages:   []string{"Japanese"},
						AudioOrigin: api.TrackAudioOriginal,
					},
					{
						ID:          "english-1",
						ResourceID:  "episode-1",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Codec:       "DTS",
						Languages:   []string{"English [en-US]"},
						AudioOrigin: api.TrackAudioDubbed,
					},
					{
						ID:          "english-2",
						ResourceID:  "episode-1",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Codec:       "DTS",
						Languages:   []string{"English [en-GB]"},
						AudioOrigin: api.TrackAudioDubbed,
					},
				},
			}
			for _, separateStreams := range []bool{true, false} {
				if !separateStreams {
					media.Tracks = media.Tracks[:2]
					media.Tracks[1].Languages = []string{"English [en-US]", "English [en-GB]"}
				}
				subject := api.NewTrackerValidationSubject(api.UploadSubject{Type: "REMUX", LanguageFacts: mediafacts.ResolveLanguages(media)}, tc.tracker)
				failures := descriptor.Validation.LanguageAssessment(subject)
				got := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == tc.rule })
				if got != separateStreams {
					t.Fatalf("separate streams=%t, rule present=%t: %+v", separateStreams, got, failures)
				}
			}
		})
	}
}
