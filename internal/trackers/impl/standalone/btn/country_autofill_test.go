// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func btnCountryAutofillSubject() api.UploadSubject {
	return api.UploadSubject{
		SourcePath:    "btn-country-evidence",
		Type:          "WEBDL",
		Source:        "WEB",
		LanguageFacts: btnLanguageFacts("Japanese", "Japanese"),
		Identity: api.ExternalIdentity{
			SourcePath: "btn-country-evidence",
			Generation: 1,
			TVDBID:     100001,
			TMDBID:     100002,
			IMDBID:     100003,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "btn-country-evidence",
			Generation: 1,
			TVDB: &api.TVDBMetadata{
				TVDBID:           100001,
				OriginalLanguage: "jpn",
				OriginalCountry:  "jpn",
			},
		},
	}
}

func requireBTNCountryResolution(t *testing.T, meta api.UploadSubject, wantCountry string, wantQuestion bool) *api.TrackerQuestionnaire {
	t.Helper()
	if foreign, country := btnLanguagePayload(meta); !foreign || country != wantCountry {
		t.Fatalf("language payload = (%v, %q), want (true, %q)", foreign, country, wantCountry)
	}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if (question != nil) != wantQuestion {
		t.Fatalf("country questionnaire = %#v, want present = %v", question, wantQuestion)
	}
	if question != nil {
		key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "BTN"), "primary_audio_country")
		if len(question.Fields) != 1 || question.Fields[0].Key != key || !question.Fields[0].Required {
			t.Fatalf("country question is not bound to the current evidence: %#v", question)
		}
	}
	failures := languageAssessment(api.NewTrackerValidationSubject(meta, "BTN"))
	hasCountryFailure := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_primary_country"
	})
	if hasCountryFailure != (wantCountry == "") {
		t.Fatalf("country assessment disagrees with payload country %q: %#v", wantCountry, failures)
	}
	for _, failure := range failures {
		if failure.Rule == "language_primary_country" &&
			(failure.Disposition != api.RuleDispositionStrict || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial) {
			t.Fatalf("unresolved country no longer blocks upload: %#v", failure)
		}
	}
	return question
}

func TestBTNCountryAutofillUsesConsistentProviderEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		tvdb    *api.TVDBMetadata
		tmdb    *api.TMDBMetadata
		imdb    *api.IMDBMetadata
		country string
	}{
		{
			name:    "TVDB alpha3",
			tvdb:    &api.TVDBMetadata{OriginalCountry: "jpn"},
			country: "17",
		},
		{
			name:    "TMDB alpha2",
			tmdb:    &api.TMDBMetadata{OriginCountry: []string{"JP"}},
			country: "17",
		},
		{
			name:    "IMDb complete list",
			imdb:    &api.IMDBMetadata{CountryList: "Japan", Country: "United States"},
			country: "17",
		},
		{
			name:    "IMDb country fallback",
			imdb:    &api.IMDBMetadata{Country: "Japan"},
			country: "17",
		},
		{
			name:    "equivalent provider values",
			tvdb:    &api.TVDBMetadata{OriginalCountry: "jpn"},
			tmdb:    &api.TMDBMetadata{OriginCountry: []string{"JP"}},
			imdb:    &api.IMDBMetadata{CountryList: " Japan "},
			country: "17",
		},
		{
			name:    "blank provider values fall through",
			tvdb:    &api.TVDBMetadata{OriginalCountry: " "},
			tmdb:    &api.TMDBMetadata{OriginCountry: []string{"", "JP"}},
			imdb:    &api.IMDBMetadata{CountryList: " ", Country: "Japan"},
			country: "17",
		},
		{name: "no country", tvdb: &api.TVDBMetadata{}},
		{
			name: "conflicting providers",
			tvdb: &api.TVDBMetadata{OriginalCountry: "jpn"},
			tmdb: &api.TMDBMetadata{OriginCountry: []string{"US"}},
		},
		{name: "multiple TMDB countries", tmdb: &api.TMDBMetadata{OriginCountry: []string{"JP", "US"}}},
		{name: "IMDb list cannot be replaced by first country", imdb: &api.IMDBMetadata{CountryList: "Japan, United States", Country: "Japan"}},
		{name: "multiple fallback countries", imdb: &api.IMDBMetadata{Country: "Japan, United States"}},
		{
			name: "unsupported country cannot be ignored",
			tvdb: &api.TVDBMetadata{OriginalCountry: "unknown-country"},
			tmdb: &api.TMDBMetadata{OriginCountry: []string{"JP"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := btnCountryAutofillSubject()
			meta.ProviderMetadata.TVDB = test.tvdb
			meta.ProviderMetadata.TMDB = test.tmdb
			meta.ProviderMetadata.IMDB = test.imdb
			if test.tvdb != nil {
				test.tvdb.TVDBID = meta.Identity.TVDBID
			}
			if test.tmdb != nil {
				test.tmdb.TMDBID = meta.Identity.TMDBID
			}
			if test.imdb != nil {
				test.imdb.IMDBID = meta.Identity.IMDBID
			}
			requireBTNCountryResolution(t, meta, test.country, test.country == "")
		})
	}
}

func TestBTNCountryAutofillRequiresCurrentProviderEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.UploadSubject)
	}{
		{"absent metadata", func(meta *api.UploadSubject) { meta.ProviderMetadata = api.SourceScopedMetadata{} }},
		{"different provider source", func(meta *api.UploadSubject) { meta.ProviderMetadata.SourcePath = "other-country-evidence" }},
		{"different identity source", func(meta *api.UploadSubject) { meta.Identity.SourcePath = "other-country-evidence" }},
		{"stale generation", func(meta *api.UploadSubject) { meta.Identity.Generation++ }},
		{"missing provider generation", func(meta *api.UploadSubject) { meta.ProviderMetadata.Generation = 0 }},
		{"different TVDB identity", func(meta *api.UploadSubject) { meta.ProviderMetadata.TVDB.TVDBID++ }},
		{"different TMDB identity", func(meta *api.UploadSubject) {
			meta.ProviderMetadata.TMDB = &api.TMDBMetadata{TMDBID: 100004, OriginCountry: []string{"JP"}}
		}},
		{"different IMDb identity", func(meta *api.UploadSubject) {
			meta.ProviderMetadata.IMDB = &api.IMDBMetadata{IMDBID: 100005, CountryList: "Japan"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := btnCountryAutofillSubject()
			test.change(&meta)
			requireBTNCountryResolution(t, meta, "", true)
		})
	}
}

func TestBTNCountryAutofillPreservesLegacyScopeCompatibility(t *testing.T) {
	meta := btnCountryAutofillSubject()
	meta.Identity.TVDBID = 0
	requireBTNCountryResolution(t, meta, "17", false)
	meta.Identity = api.ExternalIdentity{}
	meta.ProviderMetadata.SourcePath = ""
	meta.ProviderMetadata.Generation = 0
	requireBTNCountryResolution(t, meta, "17", false)
}

func TestBTNCountryAutofillRespectsCorrectedOriginalLanguage(t *testing.T) {
	for _, test := range []struct {
		name       string
		original   string
		provenance api.FactProvenance
		country    string
		staffDub   bool
	}{
		{"original primary", "Japanese", api.FactProvenanceManual, "17", false},
		{"corrected to English original", "English", api.FactProvenanceManual, "", true},
		{"original language cleared", "", api.FactProvenanceManualEmpty, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := btnCountryAutofillSubject()
			meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:           test.original,
				OriginalLanguageProvenance: test.provenance,
				TrackCoverageComplete:      true,
				PrimaryAudioTrackID:        "audio-0",
				Tracks:                     meta.LanguageFacts.Tracks,
			})
			requireBTNCountryResolution(t, meta, test.country, test.country == "")
			failures := languageAssessment(api.NewTrackerValidationSubject(meta, "BTN"))
			if staffDub := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_staff_dub" && f.Disposition == api.RuleDispositionStrict
			}); staffDub != test.staffDub {
				t.Fatalf("corrected original language did not control the dub boundary: %#v", failures)
			}
		})
	}

	meta := btnCountryAutofillSubject()
	meta.LanguageFacts.OriginalLanguages = []string{"Japanese", "English"}
	requireBTNCountryResolution(t, meta, "", true)
}

func TestBTNCountryAutofillCannotResolveIncompletePrimaryEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.LanguageFacts)
		rule   string
	}{
		{"partial programme", func(facts *api.LanguageFacts) { facts.ProgrammeStatus = api.MetadataEvidenceStatusPartial }, "language_evidence"},
		{"missing primary", func(facts *api.LanguageFacts) { facts.PrimaryAudioTrackID = "missing" }, "language_primary_evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := btnCountryAutofillSubject()
			test.change(&meta.LanguageFacts)
			if foreign, country := btnLanguagePayload(meta); foreign || country != "" {
				t.Fatalf("provider country substituted for primary evidence: (%v, %q)", foreign, country)
			}
			if question := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); question != nil {
				t.Fatalf("country question precedes primary evidence: %#v", question)
			}
			failures := languageAssessment(api.NewTrackerValidationSubject(meta, "BTN"))
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == test.rule && f.Disposition == api.RuleDispositionStrict
			}) || slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_primary_country" }) {
				t.Fatalf("wrong unresolved evidence gate: %#v", failures)
			}
		})
	}
}

func TestBTNCountryAutofillPreservesExplicitAnswers(t *testing.T) {
	for _, test := range []struct {
		name, answer, country, value string
		mismatchedProvider           bool
	}{
		{"valid override", "Canada", "5", "Canada", false},
		{"valid override with mismatched provider", "Canada", "5", "Canada", true},
		{"invalid answer", "unknown-country", "", "", false},
		{"cleared answer", "", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := btnCountryAutofillSubject()
			if test.mismatchedProvider {
				meta.ProviderMetadata.TVDB.TVDBID++
			}
			key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "BTN"), "primary_audio_country")
			meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"BTN": {key: test.answer}}
			question := requireBTNCountryResolution(t, meta, test.country, true)
			if question.Fields[0].Value != test.value {
				t.Fatalf("explicit country answer = %q, want %q", question.Fields[0].Value, test.value)
			}
		})
	}
}

func TestBTNCountryAutofillIgnoresAnswersForChangedEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*api.UploadSubject)
		country string
	}{
		{
			name: "new generation with current provider evidence",
			change: func(meta *api.UploadSubject) {
				meta.Identity.Generation++
				meta.ProviderMetadata.Generation = meta.Identity.Generation
			},
			country: "17",
		},
		{
			name:   "dub becomes primary",
			change: func(meta *api.UploadSubject) { meta.LanguageFacts.PrimaryAudioTrackID = "audio-1" },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := btnCountryAutofillSubject()
			meta.LanguageFacts = btnLanguageFacts("Japanese", "Japanese", "German")
			key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "BTN"), "primary_audio_country")
			meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"BTN": {key: "Canada"}}
			requireBTNCountryResolution(t, meta, "5", true)
			test.change(&meta)
			if changedKey := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "BTN"), "primary_audio_country"); changedKey == key {
				t.Fatal("changed evidence retained the country answer key")
			}
			question := requireBTNCountryResolution(t, meta, test.country, test.country == "")
			if question != nil && question.Fields[0].Value != "" {
				t.Fatalf("changed evidence retained the country answer: %#v", question)
			}
		})
	}
}

func TestBTNCountryAutofillPreservesFullDiscPrecedence(t *testing.T) {
	meta := btnCountryAutofillSubject()
	meta.Type = "DISC"
	meta.LanguageFacts = btnLanguageFacts("English", "German")
	meta.ProviderMetadata.TMDB = &api.TMDBMetadata{TMDBID: meta.Identity.TMDBID, OriginCountry: []string{"US"}}
	if foreign, country := btnLanguagePayload(meta); !foreign || country != "17" {
		t.Fatalf("full-disc provider language or country precedence changed: (%v, %q)", foreign, country)
	}
	if question := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); question != nil {
		t.Fatalf("full disc acquired a country question: %#v", question)
	}
	if failures := languageAssessment(api.NewTrackerValidationSubject(meta, "BTN")); len(failures) != 0 {
		t.Fatalf("full disc acquired language gates: %#v", failures)
	}
}
