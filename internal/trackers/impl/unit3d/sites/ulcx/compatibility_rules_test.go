// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXCompatibilityAssociations(t *testing.T) {
	for _, test := range []struct{ name, first, second, want string }{
		{"two source-confirmed mixes", "audio-0", "audio-1", ""},
		{"unanswered association", "", "audio-1", "language_compatibility_mix"},
		{"explicitly unresolved", "unresolved", "audio-1", "language_compatibility_mix"},
		{"unknown mix identity", "other", "audio-1", "language_compatibility_mix"},
		{"two companions cannot cover same mix twice", "audio-0", "audio-0", "language_compatibility_missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ulcxCompatibilitySubject()
			ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-0", test.first)
			ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-1", test.second)
			failures := languageAssessment(subject)
			if test.want != "" {
				requireULCXSourceFailure(t, failures, test.want, api.RuleDispositionStrict)
				return
			}
			if len(failures) != 0 {
				t.Fatalf("source-confirmed unique mixes blocked: %+v", failures)
			}
		})
	}
}

func TestULCXCompatibilityMeasuredBounds(t *testing.T) {
	for _, test := range []struct {
		name, want string
		mutate     func(*api.TrackerValidationSubject)
	}{
		{"missing companion", "language_compatibility_missing", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:3]
		}},
		{"compatibility for another codec is prohibited", "language_compatibility_mix", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks[1].Codec = "DTS-HD MA"
			subject.LanguageFacts.Tracks[1].Languages = []string{"Japanese"}
			subject.LanguageFacts.Tracks[3].Languages = []string{"Japanese"}
		}},
		{"another file cannot supply compatibility", "language_compatibility_mix", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks[3].ResourceID = "another-resource"
		}},
		{"unidentified companion stays unresolved", "language_compatibility_evidence", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks[3].ID = ""
		}},
		{"unknown compatibility language stays unresolved", "language_compatibility_evidence", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks[3].Languages = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ulcxCompatibilitySubject()
			test.mutate(&subject)
			ulcxCompatibilityAnswer(&subject, "alternate_mix_audio-1", "unique")
			ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
			ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-1", "audio-1")
			requireULCXSourceFailure(t, languageAssessment(subject), test.want, api.RuleDispositionStrict)
		})
	}
}

func TestULCXCompatibilityDoesNotImportOtherTrackerRules(t *testing.T) {
	subject := ulcxCompatibilitySubject()
	subject.Source, subject.Type = "WEB", "WEBDL"
	subject.LanguageFacts.Tracks[2].Codec, subject.LanguageFacts.Tracks[3].Codec = "AAC", "DD+"
	ulcxCompatibilityAnswer(&subject, "alternate_mix_audio-1", "unique")
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-1", "audio-1")
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("ULCX gained an unestablished codec or provenance restriction: %+v", got)
	}
	subject.LanguageFacts.Tracks = slices.Delete(subject.LanguageFacts.Tracks, 1, 2)
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-1", "audio-0")
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("ULCX gained HHD's one-companion-per-mix limit: %+v", got)
	}
}

func TestULCXCompatibilityQuestionnaireAndStaleAnswers(t *testing.T) {
	subject := ulcxCompatibilitySubject()
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-1", "audio-1")
	questionnaire := ulcxCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal)
	fields := 0
	for _, field := range questionnaire.Fields {
		if !strings.HasPrefix(field.Key, "compatibility_mix_") {
			continue
		}
		fields++
		if !field.Required || field.Value == "" || !slices.Contains(field.Options, "audio-0") || !slices.Contains(field.Options, "audio-1") {
			t.Fatalf("association field lost source choices/current answer: %+v", field)
		}
	}
	if fields != 2 {
		t.Fatalf("compatibility review unreachable: fields=%d questionnaire=%+v", fields, questionnaire)
	}
	subject.Identity.Generation++
	requireULCXSourceFailure(t, languageAssessment(subject), "language_compatibility_mix", api.RuleDispositionStrict)
	questionnaire = ulcxCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal)
	for _, field := range questionnaire.Fields {
		if strings.HasPrefix(field.Key, "compatibility_mix_") && field.Value != "" {
			t.Fatalf("stale answer reused: %+v", field)
		}
	}
}

func TestULCXCompatibilityDiscAndDebugBoundaries(t *testing.T) {
	subject := ulcxCompatibilitySubject()
	subject.Type, subject.DiscType = "DISC", "BDMV"
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("full disc gained language rules: %+v", got)
	}
	if got := ulcxCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal); got != nil {
		t.Fatalf("full disc gained review: %+v", got)
	}
	subject.Type = "REMUX"
	failures := languageAssessment(subject)
	requireULCXSourceFailure(t, failures, "language_compatibility_mix", api.RuleDispositionStrict)
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		blocking, err := trackers.FirstBlockingRuleFailure("ULCX", failures, mode, nil)
		if err != nil {
			t.Fatal(err)
		}
		if (blocking == nil) != (mode == api.WorkflowExecutionModeDebug) {
			t.Fatalf("mode=%s blocking=%+v", mode, blocking)
		}
		questionnaire := ulcxCompatibilityQuestionnaire(subject, mode)
		fields := 0
		for _, field := range questionnaire.Fields {
			if strings.HasPrefix(field.Key, "compatibility_mix_") {
				fields++
			}
			if strings.HasPrefix(field.Key, "compatibility_mix_") && field.Required == (mode == api.WorkflowExecutionModeDebug) {
				t.Fatalf("mode=%s required=%t", mode, field.Required)
			}
		}
		if fields != 2 {
			t.Fatalf("mode=%s lost compatibility reviews", mode)
		}
	}
}

func TestULCXCompatibilityAnswersCannotWaiveOtherRules(t *testing.T) {
	subject := ulcxCompatibilitySubject()
	subject.PersonalRelease = true
	subject.LanguageFacts.OriginalLanguages = []string{"Japanese"}
	subject.LanguageFacts.ProgrammeLanguages = []string{"English", "German"}
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
	ulcxCompatibilityAnswer(&subject, "compatibility_mix_compat-1", "audio-1")
	failures := languageAssessment(subject)
	requireULCXSourceFailure(t, failures, "language_original", api.RuleDispositionStrict)
	requireULCXSourceFailure(t, failures, "language_extra_dub", api.RuleDispositionStrict)
}

func ulcxCompatibilitySubject() api.TrackerValidationSubject {
	subject := api.TrackerValidationSubject{
		Tracker:       "ULCX",
		Type:          "REMUX",
		Source:        "BluRay",
		Identity:      api.ExternalIdentity{Generation: 1},
		LanguageFacts: ulcxTestLanguageFacts("English", []string{"English", "English"}, nil),
	}
	subject.LanguageFacts.Tracks[0].Codec, subject.LanguageFacts.Tracks[1].Codec = "TrueHD", "TrueHD"
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks,
		api.MediaTrackFacts{
			ID:        "compat-0",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleCompatibility,
			Languages: []string{"English"},
			Codec:     "AC-3",
		},
		api.MediaTrackFacts{
			ID:        "compat-1",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleCompatibility,
			Languages: []string{"English"},
			Codec:     "E-AC-3",
		},
	)
	ulcxCompatibilityAnswer(&subject, "alternate_mix_audio-1", "unique")
	return subject
}

func ulcxCompatibilityAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = map[string]string{}
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func ulcxCompatibilityQuestionnaire(subject api.TrackerValidationSubject, mode api.WorkflowExecutionMode) *api.TrackerQuestionnaire {
	return Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{
		ExecutionMode: mode,
		Meta: api.UploadSubject{
			Type:                        subject.Type,
			Source:                      subject.Source,
			DiscType:                    subject.DiscType,
			Identity:                    subject.Identity,
			LanguageFacts:               subject.LanguageFacts,
			PersonalRelease:             subject.PersonalRelease,
			TrackerQuestionnaireAnswers: map[string]map[string]string{"ULCX": subject.QuestionnaireAnswers},
		},
	})
}

func TestULCXUnidentifiedMatchingMixRemainsUnresolved(t *testing.T) {
	for _, sameResource := range []bool{true, false} {
		resource := "media"
		if !sameResource {
			resource = "other-media"
		}
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{
				{
					Kind:       api.MediaTrackAudio,
					Role:       api.AudioRoleProgramme,
					Languages:  []string{"English"},
					Codec:      "TrueHD",
					ResourceID: resource,
				},
				{
					ID:         "companion",
					Kind:       api.MediaTrackAudio,
					Role:       api.AudioRoleCompatibility,
					Languages:  []string{"English"},
					Codec:      "AC-3",
					ResourceID: "media",
				},
			},
		})
		if facts.AudioStatus != api.MetadataEvidenceStatusComplete {
			t.Fatal("fixture must demonstrate that language coverage does not establish identity")
		}
		failures := compatibilityFailures(api.TrackerValidationSubject{Tracker: "ULCX", LanguageFacts: facts})
		i := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_compatibility_mix" })
		want := api.MetadataEvidenceStatusComplete
		if sameResource {
			want = api.MetadataEvidenceStatusPartial
		}
		if i < 0 || failures[i].EvidenceStatus != want {
			t.Fatalf("sameResource=%t: identity classified as absence: %+v", sameResource, failures)
		}
	}
}
