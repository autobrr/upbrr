// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSpokenLanguageAgreementSelectsCountryIndependentlyOfListOrder(t *testing.T) {
	for _, languages := range [][]string{{"ja", "en"}, {"en", "ja"}} {
		for _, original := range []string{"Japanese", "French"} {
			meta := btnCountryAutofillSubject()
			meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      original,
				TitleLanguages:        languages,
				PrimaryAudioTrackID:   "audio-0",
				TrackCoverageComplete: true,
				Tracks:                meta.LanguageFacts.Tracks,
			})
			if original == "Japanese" {
				requireBTNCountryResolution(t, meta, "17", false)
			} else {
				requireBTNCountryResolution(t, meta, "", true)
			}
		}
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

func TestStaffApprovalUsesOriginalSoundtrackEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, scalar, dub string
		title, original   []string
		manual            api.FactProvenance
		staff, unresolved bool
	}{
		{"English original outweighs Italian production language", "it", "German", []string{"en"}, []string{"English"}, "", true, false},
		{"English dub does not change Japanese original", "ja", "English", []string{"ja", "en"}, []string{"Japanese"}, "", false, false},
		{"incidental English does not restrict a Japanese show's other dub", "ja", "German", []string{"ja", "en"}, []string{"Japanese"}, "", false, false},
		{"unranked multilingual original does not establish a primary language", "fr", "German", []string{"ja", "en"}, []string{"Japanese", "English"}, "", false, true},
		{"definitive Japanese resolves multilingual original", "ja", "German", []string{"ja", "en"}, []string{"Japanese", "English"}, "", false, false},
		{"explicit original-language correction resolves a mixed reference", "ja", "German", []string{"ja", "en"}, []string{"Japanese", "English"}, api.FactProvenanceManual, false, false},
		{"verified title correction overrides a conflicting Japanese original flag", "en", "German", []string{"ja", "en"}, []string{"Japanese"}, api.FactProvenanceManual, true, false},
		{"verified non-English title correction governs an English original flag", "ja", "German", []string{"ja", "en"}, []string{"English"}, api.FactProvenanceManual, false, false},
		{"Auto restores established Japanese source evidence", "en", "German", []string{"ja", "en"}, []string{"Japanese"}, "", false, false},
		{"cleared title evidence does not erase an established English original", "", "German", []string{"en"}, []string{"English"}, api.FactProvenanceManualEmpty, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:           tc.scalar,
				OriginalLanguageProvenance: tc.manual,
				TitleLanguages:             tc.title,
				TrackCoverageComplete:      true,
				Tracks: []api.MediaTrackFacts{
					{
						ID:          "original",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Languages:   tc.original,
						AudioOrigin: api.TrackAudioOriginal,
					},
					{
						ID:          "dub",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Languages:   []string{tc.dub},
						AudioOrigin: api.TrackAudioDubbed,
					},
				},
			})
			subject := api.TrackerValidationSubject{
				Type:              "ENCODE",
				LanguageFacts:     facts,
				EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: tc.scalar, OriginalLanguageProvenance: tc.manual},
			}
			failures := languageAssessment(subject)
			staff := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_staff_dub" })
			unresolved := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_staff_dub_evidence" })
			if staff != tc.staff || unresolved != tc.unresolved {
				t.Fatalf("staff=%t unresolved=%t, want %t/%t: %#v", staff, unresolved, tc.staff, tc.unresolved, failures)
			}
		})
	}
}

func TestStaffApprovalAutoOriginPreservesConfirmedOriginal(t *testing.T) {
	for _, tc := range []struct {
		name              string
		japanese, english api.TrackAudioOrigin
		japaneseLanguage  string
		clearTitle        bool
		staff, unresolved bool
	}{
		{"English Auto cannot override confirmed Japanese original", api.TrackAudioOriginal, "", "Japanese", false, false, false},
		{"verified English original can establish English-original status", api.TrackAudioOriginal, api.TrackAudioOriginal, "Japanese", false, true, false},
		{"cleared English origin does not establish English-original status", api.TrackAudioOriginal, api.TrackAudioUnresolved, "Japanese", false, false, false},
		{"agreement establishes Japanese original without source correction", "", "", "Japanese", false, false, false},
		{"und original track does not override definitive Japanese title", api.TrackAudioOriginal, "", "und", false, false, false},
		{"missing original track language does not override definitive Japanese title", api.TrackAudioOriginal, "", "", false, false, false},
		{"title clear preserves confirmed Japanese original", api.TrackAudioOriginal, "", "Japanese", true, false, false},
		{"title clear leaves Auto originals unresolved", "", "", "Japanese", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			media := api.MediaFacts{
				OriginalLanguage:      "ja",
				TitleLanguages:        []string{"ja", "en"},
				TrackCoverageComplete: true,
				Tracks: []api.MediaTrackFacts{
					{
						ID:          "japanese",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						AudioOrigin: tc.japanese,
					},
					{
						ID:          "english",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Languages:   []string{"English"},
						AudioOrigin: tc.english,
					},
					{
						ID:          "dub",
						Kind:        api.MediaTrackAudio,
						Role:        api.AudioRoleProgramme,
						Languages:   []string{"German"},
						AudioOrigin: api.TrackAudioDubbed,
					},
				},
			}
			if tc.english != "" {
				media.Tracks[1].AudioOriginProvenance = api.FactProvenanceManual
			}
			if tc.japaneseLanguage != "" {
				media.Tracks[0].Languages = []string{tc.japaneseLanguage}
			}
			if tc.clearTitle {
				media.OriginalLanguage, media.OriginalLanguageProvenance = "", api.FactProvenanceManualEmpty
			}
			for range 2 {
				facts := mediafacts.ResolveLanguages(media)
				failures := languageAssessment(api.TrackerValidationSubject{
					Type:              "ENCODE",
					LanguageFacts:     facts,
					EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: media.OriginalLanguage, OriginalLanguageProvenance: media.OriginalLanguageProvenance},
				})
				staff := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_staff_dub" })
				unresolved := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_staff_dub_evidence" })
				if staff != tc.staff || unresolved != tc.unresolved {
					t.Fatalf("staff=%t unresolved=%t, want %t/%t: %#v", staff, unresolved, tc.staff, tc.unresolved, failures)
				}
				slices.Reverse(media.Tracks)
			}
		})
	}
}
