// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSpokenLanguageSetNeverSelectsFirstCountryLanguage(t *testing.T) {
	for _, languages := range [][]string{{"ja", "en"}, {"en", "ja"}} {
		meta := btnCountryAutofillSubject()
		meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			TitleLanguages:        languages,
			PrimaryAudioTrackID:   "audio-0",
			TrackCoverageComplete: true,
			Tracks:                meta.LanguageFacts.Tracks,
		})
		requireBTNCountryResolution(t, meta, "", true)
	}
}

func TestListedSpokenLanguageDoesNotWaiveConfirmedDubStaffRule(t *testing.T) {
	facts := btnLanguageFacts("English", "English", "Japanese")
	facts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "English",
		TitleLanguages:        []string{"en", "ja"},
		PrimaryAudioTrackID:   "audio-0",
		TrackCoverageComplete: true,
		Tracks:                facts.Tracks,
	})
	failures := languageAssessment(api.TrackerValidationSubject{LanguageFacts: facts, Type: "ENCODE"})
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_staff_dub" && api.IsStrictRuleFailure(f) }) {
		t.Fatalf("a listed dialogue language erased the staff-only dub restriction: %#v", failures)
	}
}

func TestMatchingSpokenDubCannotUseWorkOriginCountry(t *testing.T) {
	for _, origin := range []api.TrackAudioOrigin{api.TrackAudioOriginal, api.TrackAudioDubbed} {
		meta := btnCountryAutofillSubject()
		meta.ProviderMetadata.TVDB = nil
		meta.ProviderMetadata.TMDB = &api.TMDBMetadata{
			TMDBID:           100002,
			OriginalLanguage: "en",
			SpokenLanguages:  []string{"fr"},
			OriginCountry:    []string{"DE"},
		}
		meta.LanguageFacts.Tracks[0].Languages = []string{"French"}
		meta.LanguageFacts.Tracks[0].AudioOrigin = origin
		meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "en",
			TitleLanguages:        []string{"fr"},
			TrackCoverageComplete: true,
			PrimaryAudioTrackID:   "audio-0",
			Tracks:                meta.LanguageFacts.Tracks,
		})
		if origin == api.TrackAudioOriginal {
			requireBTNCountryResolution(t, meta, "7", false)
		} else {
			requireBTNCountryResolution(t, meta, "", true)
		}
	}
}
