// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNordicDubNameRequiresConfirmedOrigin(t *testing.T) {
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioOriginal, api.TrackAudioDubbed} {
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{{
				ID:          "audio",
				Kind:        api.MediaTrackAudio,
				Role:        api.AudioRoleProgramme,
				Languages:   []string{"Swedish"},
				AudioOrigin: origin,
			}},
		})
		label, _, err := audioLabelForFacts(api.UploadSubject{LanguageFacts: facts})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(label, "Dubbed") != (origin == api.TrackAudioDubbed) {
			t.Fatalf("origin=%q label=%q", origin, label)
		}
	}
}

func TestManualMarkerDoesNotRequireUnneededOriginReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		languages []string
		manual    bool
		dubbed    bool
		want      string
	}{
		{"manual two languages", []string{"German", "French"}, true, false, ""},
		{"automatic two languages", []string{"German", "French"}, false, false, "language_origin"},
		{"manual three unresolved", []string{"German", "French", "Spanish"}, true, false, "language_origin"},
		{"manual three without original", []string{"German", "French", "Spanish"}, true, true, "language_multilingual_original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracks []api.MediaTrackFacts
			for _, language := range tc.languages {
				origin := api.TrackAudioOrigin("")
				if tc.dubbed {
					origin = api.TrackAudioDubbed
				}
				tracks = append(tracks, api.MediaTrackFacts{
					ID:                  language,
					ManifestFingerprint: "scan",
					Kind:                api.MediaTrackAudio,
					Role:                api.AudioRoleProgramme,
					Languages:           []string{language},
					AudioOrigin:         origin,
				})
			}
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "Japanese",
				TrackCoverageComplete: true,
				Tracks:                tracks,
			})
			subject := api.TrackerValidationSubject{Type: "ENCODE", LanguageFacts: facts}
			if tc.manual {
				subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, manualLanguageMarkerKey): "MULTi"}
			}
			failures := languageAssessment(subject)
			if tc.want == "" && len(failures) != 0 || tc.want != "" && !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == tc.want }) {
				t.Fatalf("want %q, got %#v", tc.want, failures)
			}
			if !tc.dubbed && !subject.LanguageFacts.HasUnresolvedProgrammeAudioOrigin() {
				t.Fatal("presentation marker rewrote canonical origin")
			}
		})
	}
}
