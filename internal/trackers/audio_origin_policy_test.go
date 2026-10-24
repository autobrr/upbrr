// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProgrammeOriginDoesNotInferForeignDubs(t *testing.T) {
	policy := LanguagePolicy{OriginalAudio: LanguageProhibited, ExtraDubs: LanguageProhibited}
	for _, tc := range []struct {
		name                string
		languages           []string
		origin              api.TrackAudioOrigin
		wantOrigin, wantDub bool
	}{
		{"mul", []string{"Multiple Languages"}, "", true, false},
		{"mismatched tag", []string{"German"}, "", true, false},
		{"multilingual not reviewed", []string{"English", "German"}, "", true, false},
		{"multilingual original", []string{"English", "German"}, api.TrackAudioOriginal, false, false},
		{"main-language original", []string{"German"}, api.TrackAudioOriginal, false, false},
		{"confirmed sole foreign dub", []string{"German"}, api.TrackAudioDubbed, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "English",
				TrackCoverageComplete: true,
				Tracks: []api.MediaTrackFacts{{
					ID:                  "audio",
					ManifestFingerprint: "scan",
					ResourceID:          "resource",
					Kind:                api.MediaTrackAudio,
					Role:                api.AudioRoleProgramme,
					Languages:           tc.languages,
					AudioOrigin:         tc.origin,
				}},
			})
			subject := api.TrackerValidationSubject{LanguageFacts: facts, Type: "ENCODE"}
			failures := EvaluateLanguagePolicy(subject, policy)
			has := func(rule string) bool {
				return slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule })
			}
			if has("language_origin") != tc.wantOrigin || has("language_extra_dub") != tc.wantDub {
				t.Fatalf("failures=%#v", failures)
			}
			if tc.wantOrigin && has("language_original") {
				t.Fatal("unresolved origin became a proven missing-original violation")
			}
			if tc.origin == api.TrackAudioOriginal && has("language_original") {
				t.Fatal("reviewed original soundtrack rejected by tag mismatch")
			}
			for _, f := range failures {
				if f.Rule == "language_origin" && (f.EvidenceStatus != api.MetadataEvidenceStatusPartial || !slices.Equal(f.AudioOriginCorrectionTrackIDs, []string{"audio"})) {
					t.Fatalf("origin review not actionable: %#v", f)
				}
			}
		})
	}
}

func TestOriginalSoundtrackDialogueDoesNotWaiveDubSubtitleRule(t *testing.T) {
	policy := LanguagePolicy{EnglishSubtitles: "foreign_without_dub", MissingSubtitles: LanguageProhibited}
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioOriginal, api.TrackAudioDubbed} {
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{{
				ID:                  "audio",
				ManifestFingerprint: "scan",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           []string{"Japanese", "English"},
				AudioOrigin:         origin,
			}},
		})
		failures := EvaluateLanguagePolicy(api.TrackerValidationSubject{LanguageFacts: facts}, policy)
		index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_subtitles" })
		if origin == api.TrackAudioDubbed {
			if index >= 0 {
				t.Fatal("confirmed English dub did not retain subtitle exception")
			}
			continue
		}
		if index < 0 {
			t.Fatal("incidental English dialogue waived subtitle rule")
		}
		want := api.MetadataEvidenceStatusComplete
		if origin == "" {
			want = api.MetadataEvidenceStatusPartial
		}
		if failures[index].EvidenceStatus != want {
			t.Fatalf("origin=%q failure=%#v", origin, failures[index])
		}
	}
}

func TestAdvisoryOriginReviewDoesNotBecomeInputGate(t *testing.T) {
	subject := api.UploadSubject{LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "English",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{{
			ID:                  "audio",
			ManifestFingerprint: "scan",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			Languages:           []string{"German"},
		}},
	})}
	binding := WithLanguagePolicy(NoExtraValidationPolicy("test"), LanguagePolicy{OriginalFirst: LanguageAdvisory, OriginalDefault: LanguageAdvisory})
	if fields := inputLanguageReview(binding, subject, "ONE"); len(fields) != 0 {
		t.Fatalf("advisory became source gate: %#v", fields)
	}
	for _, failure := range binding.LanguageAssessment(api.NewTrackerValidationSubject(subject, "ONE")) {
		if failure.Rule == "language_origin" && failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("advisory origin became strict: %#v", failure)
		}
	}
}

func TestAggregateOnlyAudioNeedsInspectionNotDubClassification(t *testing.T) {
	subject := api.TrackerValidationSubject{LanguageFacts: api.LanguageFacts{
		OriginalLanguages:      []string{"English"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"German"},
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
	}}
	failures := EvaluateLanguagePolicy(subject, LanguagePolicy{OriginalAudio: LanguageProhibited, ExtraDubs: LanguageProhibited})
	if len(failures) != 1 || failures[0].Rule != "language_origin" || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial || len(failures[0].AudioOriginCorrectionTrackIDs) != 0 {
		t.Fatalf("aggregate-only evidence was classified or invented a track: %#v", failures)
	}
}

func TestOriginReviewOnlyRequestsEvidenceForUnmetPredicates(t *testing.T) {
	facts := mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{
			{
				ID:                  "original",
				ManifestFingerprint: "scan",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           []string{"Japanese"},
				DefaultKnown:        true,
				Default:             true,
				StreamOrderKnown:    true,
				StreamOrder:         0,
			},
			{
				ID:                  "unknown",
				ManifestFingerprint: "scan",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           []string{"German"},
				StreamOrderKnown:    true,
				StreamOrder:         1,
			},
		},
	})
	for _, policy := range []LanguagePolicy{{OriginalAudio: LanguageProhibited}, {OriginalDefault: LanguageProhibited}, {OriginalFirst: LanguageProhibited}, {EnglishSubtitles: "foreign_without_dub", MissingSubtitles: LanguageAdvisory}} {
		for _, failure := range EvaluateLanguagePolicy(api.TrackerValidationSubject{LanguageFacts: facts}, policy) {
			if failure.Rule == "language_origin" {
				t.Fatalf("irrelevant unknown origin became a question: %#v", failure)
			}
		}
	}
	facts.OriginalLanguages = []string{"English"}
	for _, failure := range EvaluateLanguagePolicy(api.TrackerValidationSubject{LanguageFacts: facts}, LanguagePolicy{EnglishSubtitles: "foreign_without_dub", MissingSubtitles: LanguageProhibited}) {
		if failure.Rule == "language_origin" {
			t.Fatalf("foreign-only predicate asked about English-original source: %#v", failure)
		}
	}
}

func TestUncertainDubDoesNotPromoteSubtitleAdviceToStrict(t *testing.T) {
	facts := mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{{
			ID:                  "audio",
			ManifestFingerprint: "scan",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			Languages:           []string{"Japanese", "English"},
		}},
	})
	failures := EvaluateLanguagePolicy(api.TrackerValidationSubject{LanguageFacts: facts}, LanguagePolicy{EnglishSubtitles: "foreign_without_dub", MissingSubtitles: LanguageAdvisory})
	found := false
	for _, failure := range failures {
		if failure.Rule == "language_subtitles" {
			found = true
		}
		if failure.Disposition != api.RuleDispositionAdvisory || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("uncertainty promoted advice: %#v", failure)
		}
	}
	if !found {
		t.Fatal("subtitle guidance disappeared")
	}
}
