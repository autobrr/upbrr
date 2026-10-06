// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func azTestLanguageFacts(original string, audio ...string) api.LanguageFacts {
	media := api.MediaFacts{
OriginalLanguage: original,
 TrackCoverageComplete: true,
 PrimaryAudioTrackID: "audio-0",
}
	for i, language := range audio {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID: "audio-" + strconv.Itoa(i),
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
			Languages: []string{languageutil.NormalizeLanguageLabel(language)},
 Default: i == 0,
		})
	}
	return mediafacts.ResolveLanguages(media)
}

func TestAZRegionalDialectEvidenceAndIndependentRules(t *testing.T) {
	meta := api.UploadSubject{
Type: "WEBDL",
 Identity: api.ExternalIdentity{SourcePath: "az-language", Generation: 1},
 LanguageFacts: azTestLanguageFacts("Chinese", "Cantonese"),
}
	question := New("AZ").ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if question == nil || len(question.Fields) != 1 {
		t.Fatal("regional dialect exception was not reviewable")
	}
	key := question.Fields[0].Key
	for _, answer := range []string{"", "no", "yes"} {
		meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"AZ": {key: answer}}
		failures := evaluateAZLanguageRules(siteFor("AZ"), api.NewTrackerValidationSubject(meta, "AZ"))
		if answer == "yes" {
			if len(failures) != 0 {
				t.Fatalf("established regional dialect blocked: %#v", failures)
			}
		} else if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == "language_regional_dialect" && f.Disposition == api.RuleDispositionStrict
		}) {
			t.Fatalf("unestablished regional dialect accepted: %#v", failures)
		}
	}
	for _, change := range []func(*api.UploadSubject){
		func(m *api.UploadSubject) { m.Identity.Generation++ },
		func(m *api.UploadSubject) { m.LanguageFacts = azTestLanguageFacts("Japanese", "German") },
	} {
		changed := meta
		change(&changed)
		question := New("AZ").ProjectionQuestionnaire(trackers.PreparationInput{Meta: changed})
		if question == nil || question.Fields[0].Key == key || question.Fields[0].Value != "" {
			t.Fatalf("changed facts reused dialect answer: %#v", question)
		}
	}
	meta.LanguageFacts.Tracks = append(meta.LanguageFacts.Tracks, api.MediaTrackFacts{
Kind: api.MediaTrackAudio,
 Role: api.AudioRoleVoiceOver,
 Languages: []string{"Chinese"},
})
	question = New("AZ").ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	meta.TrackerQuestionnaireAnswers["AZ"][question.Fields[0].Key] = "yes"
	failures := evaluateAZLanguageRules(siteFor("AZ"), api.NewTrackerValidationSubject(meta, "AZ"))
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_track_role" && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
	}) {
		t.Fatalf("dialect answer waived prohibited voice-over: %#v", failures)
	}
	meta.Type = "DISC"
	if failures := evaluateAZLanguageRules(siteFor("AZ"), api.NewTrackerValidationSubject(meta, "AZ")); len(failures) != 0 {
		t.Fatalf("full disc assessed: %#v", failures)
	}
	if New("AZ").ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta}) != nil {
		t.Fatal("full disc acquired dialect question")
	}
}

func TestAZCZRemuxAndManualClearBoundaries(t *testing.T) {
	for _, site := range []string{"AZ", "CZ"} {
		subject := api.TrackerValidationSubject{
			Tracker: site,
 Type: "REMUX",
 DiscType: "BDMV",
 LanguageFacts: azTestLanguageFacts("Japanese", "Japanese", "English"),
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "German"}},
			EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "Japanese", OriginalLanguageProvenance: api.FactProvenanceManual},
		}
		if failures := evaluateAZLanguageRules(siteFor(site), subject); len(failures) != 0 {
			t.Fatalf("%s original and English remux blocked: %#v", site, failures)
		}
		for _, clearOriginal := range []bool{true, false} {
			media := api.MediaFacts{
OriginalLanguage: "Japanese",
 TrackCoverageComplete: true,
 Tracks: subject.LanguageFacts.Tracks,
}
			if clearOriginal {
				media.OriginalLanguage = ""
				media.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
			} else {
				media.AudioLanguagesProvenance = api.FactProvenanceManualEmpty
			}
			cleared := subject
			cleared.LanguageFacts = mediafacts.ResolveLanguages(media)
			if clearOriginal {
				cleared.EffectiveMetadata.OriginalLanguage = ""
				cleared.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
			}
			if failures := evaluateAZLanguageRules(siteFor(site), cleared); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Disposition == api.RuleDispositionStrict && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
			}) {
				t.Fatalf("%s manual clear accepted: %#v", site, failures)
			}
		}
		subject.LanguageFacts = azTestLanguageFacts("Japanese", "Japanese", "Japanese")
		failures := evaluateAZLanguageRules(siteFor(site), subject)
		want := "language_duplicate_language"
		if site == "CZ" {
			want = "language_additional_original"
		}
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == want && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
		}) {
			t.Fatalf("%s extra original not strictly assessed: %#v", site, failures)
		}
	}
	subject := api.TrackerValidationSubject{
Tracker: "AZ",
 Type: "REMUX",
 LanguageFacts: azTestLanguageFacts("Japanese", "Japanese", "German", "French"),
}
	if failures := evaluateAZLanguageRules(siteFor("AZ"), subject); len(failures) != 0 {
		t.Fatalf("per-language remux dub allowance lost: %#v", failures)
	}
	// The general English-dub exception remains applicable to remuxes; the
	// one-original-track limit does not establish a mandatory-original rule.
	subject.LanguageFacts = azTestLanguageFacts("Japanese", "English")
	if failures := evaluateAZLanguageRules(siteFor("AZ"), subject); len(failures) != 0 {
		t.Fatalf("general English-dub exception lost for remux: %#v", failures)
	}
	subject.LanguageFacts = azTestLanguageFacts("Japanese", "German")
	if failures := evaluateAZLanguageRules(siteFor("AZ"), subject); len(failures) == 0 {
		t.Fatal("remux allowance expanded to an unestablished non-original, non-English dub")
	}
	subject.Tracker = "CZ"
	subject.LanguageFacts = azTestLanguageFacts("Japanese", "English")
	if failures := evaluateAZLanguageRules(siteFor("CZ"), subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_original" && f.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("AZ exception leaked into CZ: %#v", failures)
	}
	subject.LanguageFacts = azTestLanguageFacts("Japanese", "Japanese", "English")
	subject.LanguageFacts.Tracks[0].Default = false
	if failures := evaluateAZLanguageRules(siteFor("CZ"), subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_original_default" && f.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("CZ original-default requirement lost: %#v", failures)
	}
}

func TestAZRemuxUnknownLanguageEvidenceIsBlocking(t *testing.T) {
	for _, facts := range []api.LanguageFacts{
		{},
		{
OriginalLanguages: []string{"Japanese"},
 OriginalLanguagesKnown: true,
 ProgrammeStatus: api.MetadataEvidenceStatusPartial,
},
	} {
		subject := api.TrackerValidationSubject{
Tracker: "AZ",
 Type: "REMUX",
 DiscType: "BDMV",
 LanguageFacts: facts,
}
		failures := evaluateAZLanguageRules(siteFor("AZ"), subject)
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Disposition == api.RuleDispositionStrict && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
		}) {
			t.Fatalf("unknown language facts were not unresolved: %#v", failures)
		}
		subject.Type = "DISC"
		if failures := evaluateAZLanguageRules(siteFor("AZ"), subject); len(failures) != 0 {
			t.Fatalf("full disc acquired language gates: %#v", failures)
		}
	}
}

func evaluateSiteRules(t *testing.T, tracker string, meta api.RuleSubject) []api.RuleFailure {
	t.Helper()
	failures, err := New(tracker).evaluateRules(
		context.Background(),
		api.NewTrackerValidationSubjectFromRuleSubject(meta, tracker),
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("evaluate %s rules: %v", tracker, err)
	}
	return failures
}

func TestEvaluateRulesAZRedirectsEnglishTerritories(t *testing.T) {
	t.Parallel()

	failures := evaluateSiteRules(t, "AZ", api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginCountry: []string{"US"}},
		},
	})
	if len(failures) == 0 {
		t.Fatal("expected AZ rule failure")
	}
}

func TestEvaluateRulesCZRejectsAsianContent(t *testing.T) {
	t.Parallel()

	failures := evaluateSiteRules(t, "CZ", api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginCountry: []string{"JP"}},
		},
	})
	if len(failures) == 0 {
		t.Fatal("expected CZ rule failure")
	}
}

func TestEvaluateRulesCountryRestrictionsUseCorrectDisposition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		tracker         string
		country         string
		rule            string
		wantDisposition api.RuleDisposition
	}{
		{
			name:            "AZ redirect",
			tracker:         "AZ",
			country:         "US",
			rule:            "country_redirect",
			wantDisposition: api.RuleDispositionWaivable,
		},
		{
			name:            "CZ redirect",
			tracker:         "CZ",
			country:         "JP",
			rule:            "country_redirect",
			wantDisposition: api.RuleDispositionWaivable,
		},
		{
			name:            "PHD redirect",
			tracker:         "PHD",
			country:         "JP",
			rule:            "country_redirect",
			wantDisposition: api.RuleDispositionWaivable,
		},
		{
			name:            "CZ block",
			tracker:         "CZ",
			country:         "AQ",
			rule:            "country_block",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name:            "PHD block",
			tracker:         "PHD",
			country:         "AQ",
			rule:            "country_block",
			wantDisposition: api.RuleDispositionStrict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failures := evaluateSiteRules(t, test.tracker, api.RuleSubject{
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{OriginCountry: []string{test.country}},
				},
			})
			for _, failure := range failures {
				if failure.Rule == test.rule {
					if failure.Disposition != test.wantDisposition {
						t.Fatalf("%s disposition = %q, want %q", test.rule, failure.Disposition, test.wantDisposition)
					}
					return
				}
			}
			t.Fatalf("missing %s failure: %#v", test.rule, failures)
		})
	}
}

func TestEvaluateRulesPHDRejectsSDAndBlockedGroup(t *testing.T) {
	t.Parallel()

	failures := evaluateSiteRules(t, "PHD", api.RuleSubject{
		Identity:  api.ExternalIdentity{Category: "MOVIE"},
		Release:   api.ReleaseInfo{Resolution: "480p"},
		Container: "avi",
		Tag:       "-RARBG",
	})
	if len(failures) < 2 {
		t.Fatalf("expected multiple PHD failures, got %v", failures)
	}
}
