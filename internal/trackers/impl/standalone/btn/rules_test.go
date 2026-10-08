// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func btnLanguageFacts(original string, audio ...string) api.LanguageFacts {
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
			Languages: []string{language},
 Default: i == 0,
		})
	}
	return mediafacts.ResolveLanguages(media)
}

func TestBTNPrimaryLanguagePayloadAndCountryEvidence(t *testing.T) {
	meta := api.UploadSubject{
		Type: "WEBDL",
 Source: "WEB-DL",
 Container: "MKV",
 VideoEncode: "x265",
		Release:          api.ReleaseInfo{Resolution: "1080p"},
		Identity:         api.ExternalIdentity{SourcePath: "btn-language", Generation: 1},
		LanguageFacts:    btnLanguageFacts("Japanese", "Japanese", "English"),
		ProviderMetadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{OriginalLanguage: "en", OriginalCountry: "US"}},
	}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if question == nil || len(question.Fields) != 1 {
		t.Fatal("foreign primary audio did not request its country")
	}
	key := question.Fields[0].Key
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"BTN": {key: "Japan"}}
	input := trackers.PreparationInput{
Tracker: "BTN",
 Meta: meta,
 Projection: &api.TrackerReleaseProjection{
		TrackerID: "BTN",
 UploadReleaseName: "Example.Show.S01E01.1080p.WEB-DL.H.265-GRP",
 UploadReady: true,
 Readiness: api.ReadinessStatusReady,
	},
}
	payload, err := buildBTNUploadPayload(input, nil)
	if err != nil || payload["foreign"] != "on" || payload["country"] != "17" {
		t.Fatalf("primary Japanese payload = %#v, %v", payload, err)
	}
	input.Meta.LanguageFacts.PrimaryAudioTrackID = "audio-1"
	payload, err = buildBTNUploadPayload(input, nil)
	if err != nil || payload["foreign"] != "" || payload["country"] != "" {
		t.Fatalf("primary English retained foreign-original flags: %#v, %v", payload, err)
	}
	for _, change := range []func(*api.UploadSubject){
		func(m *api.UploadSubject) { m.Identity.Generation++ },
		func(m *api.UploadSubject) { m.LanguageFacts = btnLanguageFacts("Japanese", "German") },
	} {
		changed := meta
		change(&changed)
		question := languageQuestionnaire(trackers.PreparationInput{Meta: changed})
		if question == nil || question.Fields[0].Key == key || question.Fields[0].Value != "" {
			t.Fatalf("changed evidence reused country answer: %#v", question)
		}
		failures := languageAssessment(api.NewTrackerValidationSubject(changed, "BTN"))
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == "language_primary_country" && f.Disposition == api.RuleDispositionStrict
		}) {
			t.Fatalf("changed country evidence was not blocked: %#v", failures)
		}
	}
	meta.Type = "DISC"
	meta.ProviderMetadata.TVDB.OriginalLanguage = "jpn"
	if foreign, country := btnLanguagePayload(meta); !foreign || country != "2" {
		t.Fatalf("full-disc legacy flags changed: foreign=%v country=%q", foreign, country)
	}
	if languageQuestionnaire(trackers.PreparationInput{Meta: meta}) != nil {
		t.Fatal("full disc acquired a country question")
	}
}

func TestBTNCountryAnswerCannotWaiveStaffOrUnknownPrimary(t *testing.T) {
	meta := api.UploadSubject{Type: "WEBDL", LanguageFacts: btnLanguageFacts("English", "German")}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"BTN": {question.Fields[0].Key: "Germany"}}
	failures := languageAssessment(api.NewTrackerValidationSubject(meta, "BTN"))
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_staff_dub" && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
	}) {
		t.Fatalf("country answer waived staff permission: %#v", failures)
	}
	meta.LanguageFacts.PrimaryAudioTrackID = "missing"
	if failures := languageAssessment(api.NewTrackerValidationSubject(meta, "BTN")); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_primary_evidence" }) {
		t.Fatalf("unknown primary silently accepted: %#v", failures)
	}
}

func TestBTNUnresolvedProgrammeAndClearsStayBlocking(t *testing.T) {
	for _, status := range []api.MetadataEvidenceStatus{api.MetadataEvidenceStatusPartial, api.MetadataEvidenceStatusContradictory} {
		subject := api.TrackerValidationSubject{
Tracker: "BTN",
 Type: "WEBDL",
 LanguageFacts: btnLanguageFacts("English", "English"),
}
		subject.LanguageFacts.ProgrammeStatus = status
		subject.LanguageFacts.ProgrammeLanguages = nil
		if failures := languageAssessment(subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == "language_evidence" && f.Disposition == api.RuleDispositionStrict && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
		}) {
			t.Fatalf("%s programme evidence silently accepted: %#v", status, failures)
		}
	}
	media := api.MediaFacts{
		OriginalLanguage: "English",
 TrackCoverageComplete: true,
 AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
		Tracks: []api.MediaTrackFacts{{
Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
 Languages: []string{"English"},
}},
	}
	subject := api.TrackerValidationSubject{
Tracker: "BTN",
 Type: "WEBDL",
 LanguageFacts: mediafacts.ResolveLanguages(media),
}
	if failures := languageAssessment(subject); len(failures) == 0 {
		t.Fatal("manual audio clear silently accepted")
	}
	media.AudioLanguagesProvenance = api.FactProvenanceAutomatic
	media.OriginalLanguage = ""
	media.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if failures := languageAssessment(subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_original_evidence" && f.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("manual original clear silently accepted: %#v", failures)
	}
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired language gates: %#v", failures)
	}
	subject.Type, subject.DiscType = "REMUX", "BDMV"
	if failures := languageAssessment(subject); len(failures) == 0 {
		t.Fatal("disc-sourced remux bypassed evidence assessment")
	}
}
