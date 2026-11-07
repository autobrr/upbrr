// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSpokenLanguageMatchDoesNotWaiveConfirmedDubRules(t *testing.T) {
	subject := languageSubject("German", "German", "French")
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "en",
		TitleLanguages:        []string{"de", "fr"},
		TrackCoverageComplete: true,
		Tracks:                subject.LanguageFacts.Tracks,
	})
	for _, tc := range []struct {
		tracker, rule string
		disposition   api.RuleDisposition
	}{
		{"BHD", "language_extra_dub", api.RuleDispositionStrict},
		{"AITHER", "language_extra_dub", api.RuleDispositionStrict},
		{"HHD", "language_extra_dub", api.RuleDispositionStrict},
		{"PTP", "language_redundant_audio", api.RuleDispositionAdvisory},
	} {
		t.Run(tc.tracker, func(t *testing.T) {
			failures := languageFailures(t, tc.tracker, subject)
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == tc.rule && f.Disposition == tc.disposition }) {
				t.Fatalf("a title-language match erased the confirmed dub: %#v", failures)
			}
		})
	}
}

func TestEnglishDialogueDoesNotHideMultipleEnglishDubs(t *testing.T) {
	subject := languageSubject("French", "French", "English", "English")
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "en",
		TitleLanguages:        []string{"en", "fr"},
		TrackCoverageComplete: true,
		Tracks:                subject.LanguageFacts.Tracks,
	})
	if !subject.LanguageFacts.HasEnglishDub() {
		t.Fatal("English dialogue erased separately classified English dubbing")
	}
	failures := languageFailures(t, "HHD", subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_english_dub_count" && api.IsStrictRuleFailure(f)
	}) {
		t.Fatalf("English dialogue bypassed the established dub-count limit: %#v", failures)
	}
}

func TestSpokenLanguageOriginalStillNeedsDefaultAndSubtitles(t *testing.T) {
	subject := languageSubject("English", "French")
	subject.LanguageFacts.Tracks[0].AudioOrigin = ""
	subject.LanguageFacts.Tracks[0].Default = false
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "en",
		TitleLanguages:        []string{"fr", "de"},
		TrackCoverageComplete: true,
		Tracks:                subject.LanguageFacts.Tracks[:1],
	})
	failures := trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{
		OriginalAudio:    trackers.LanguageProhibited,
		OriginalDefault:  trackers.LanguageProhibited,
		EnglishSubtitles: "foreign_without_dub",
		MissingSubtitles: trackers.LanguageProhibited,
	})
	for _, rule := range []string{"language_original_default", "language_subtitles"} {
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule && api.IsStrictRuleFailure(f) }) {
			t.Fatalf("preferred title evidence bypassed independent %s rule: %#v", rule, failures)
		}
	}
}

func TestListedEnglishDialogueDoesNotSupplyEnglishAccessibility(t *testing.T) {
	for _, audio := range [][]string{{"Japanese"}, {"Japanese", "English"}} {
		t.Run(strings.Join(audio, "+"), func(t *testing.T) {
			subject := languageSubject("Japanese", "Japanese")
			subject.LanguageFacts.Tracks[0].Languages = audio
			subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "ja",
				TitleLanguages:        []string{"ja", "en"},
				TrackCoverageComplete: true,
				Tracks:                subject.LanguageFacts.Tracks[:1],
			})
			if subject.LanguageFacts.HasEnglishDub() {
				t.Fatal("multilingual original audio became an English dub")
			}
			for _, tc := range []struct {
				tracker     string
				disposition api.RuleDisposition
			}{
				{"AITHER", api.RuleDispositionStrict}, {"ULCX", api.RuleDispositionStrict}, {"HHD", api.RuleDispositionWaivable},
			} {
				failures := languageFailures(t, tc.tracker, subject)
				if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_subtitles" && f.Disposition == tc.disposition }) {
					t.Fatalf("%s: English title metadata erased subtitle requirements without an English dub: %#v", tc.tracker, failures)
				}
			}
		})
	}
}
