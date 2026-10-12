// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func ordinaryCompatibilitySubject() api.TrackerValidationSubject {
	subject := aitherPassingSubject()
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks[:1], api.MediaTrackFacts{
		ID:        "standalone-dd",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleProgramme,
		Codec:     "DD",
		Title:     "AC-3 5.1-EX",
		Languages: []string{"Japanese"},
	})
	return subject
}

func TestAitherStandaloneCandidateBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
		want   bool
	}{
		{name: "ordinary programme", want: true},
		{
			name:   "unknown role",
			want:   true,
			mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = "" },
		},
		{name: "commentary", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = api.AudioRoleCommentary }},
		{name: "commentary evidence", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Commentary = true }},
		{name: "alternate mix", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix }},
		{name: "embedded core", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].EmbeddedCompatibility = true }},
		{name: "embedded core title", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[1].Role = api.AudioRoleCompatibility
			s.LanguageFacts.Tracks[1].Title = "Embedded core"
		}},
		{name: "other resource", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ResourceID = "other-file" }},
		{name: "same stream identity", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ID = s.LanguageFacts.Tracks[0].ID }},
		{
			name: "primary programme remains reviewable",
			want: true,
			mutate: func(s *api.TrackerValidationSubject) {
				s.LanguageFacts.PrimaryAudioTrackID = s.LanguageFacts.Tracks[1].ID
			},
		},
		{name: "different language", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Languages = []string{"German"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			if candidate := compatibilityCandidate(subject.LanguageFacts, subject.LanguageFacts.Tracks[1]); candidate != test.want {
				t.Fatalf("candidate=%v, want %v", candidate, test.want)
			}
			if question := aitherCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal); question != nil {
				t.Fatalf("clear or excluded candidate requires association review: %+v", question)
			}
			if test.want {
				if failures := compatibilityFailures(subject); len(failures) != 0 {
					t.Fatalf("unambiguous companion rejected: %+v", failures)
				}
			} else {
				assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			}
		})
	}
}

func TestAitherReviewCanKeepAStandaloneStreamAsProgrammeAudio(t *testing.T) {
	t.Parallel()
	subject := ordinaryCompatibilitySubject()
	key := trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd")
	subject.QuestionnaireAnswers = map[string]string{key: "not_compatibility"}
	assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	if additional := additionalMainAudio(subject); len(additional) != 1 || additional[0].ID != "standalone-dd" {
		t.Fatalf("declined companion lost its programme identity: %+v", additional)
	}
	subject.QuestionnaireAnswers[key] = subject.LanguageFacts.Tracks[0].ID
	if failures := compatibilityFailures(subject); len(failures) != 0 {
		t.Fatalf("confirmed same-resource companion: %+v", failures)
	}
	if additional := additionalMainAudio(subject); len(additional) != 0 {
		t.Fatalf("confirmed companion is counted as an extra mix: %+v", additional)
	}
	// Discovery must not depend on container order relative to its TrueHD mix.
	slices.Reverse(subject.LanguageFacts.Tracks)
	key = trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd")
	subject.QuestionnaireAnswers = map[string]string{key: subject.LanguageFacts.Tracks[1].ID}
	if additional := additionalMainAudio(subject); len(additional) != 0 {
		t.Fatalf("earlier companion displaced the real main track: %+v", additional)
	}
}

func TestAitherDiscoveredCompanionsRemainBoundToDistinctMixes(t *testing.T) {
	t.Parallel()
	subject := ordinaryCompatibilitySubject()
	firstMix := subject.LanguageFacts.Tracks[0].ID
	otherMix := subject.LanguageFacts.Tracks[0]
	otherMix.ID = "commentary-truehd"
	otherMix.Role = api.AudioRoleCommentary
	otherMix.Commentary = true
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, otherMix)
	question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{LanguageFacts: subject.LanguageFacts}})
	if question == nil || len(question.Fields) != 1 || !slices.Contains(question.Fields[0].Options, firstMix) || !slices.Contains(question.Fields[0].Options, otherMix.ID) {
		t.Fatalf("distinct mixes not offered for explicit source review: %+v", question)
	}
	subject.QuestionnaireAnswers = map[string]string{question.Fields[0].Key: firstMix}
	assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)

	secondCompanion := subject.LanguageFacts.Tracks[1]
	secondCompanion.ID = "second-standalone-dd"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, secondCompanion)
	subject.QuestionnaireAnswers = map[string]string{
		trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd"):        firstMix,
		trackers.LanguageQuestionKey(subject, "compatibility_mix_second-standalone-dd"): otherMix.ID,
	}
	if failures := compatibilityFailures(subject); len(failures) != 0 {
		t.Fatalf("separately confirmed mix companions rejected: %+v", failures)
	}
	// Corrected track evidence invalidates both source associations.
	subject.LanguageFacts.Tracks[1].Title = "Corrected source description"
	assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestAitherSourceAssociationDoesNotInventUnknownTrackRoles(t *testing.T) {
	t.Parallel()
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].Role = ""
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   subject.LanguageFacts.Tracks[0].ID,
		Tracks:                subject.LanguageFacts.Tracks,
	})
	before := subject.LanguageFacts.Clone()
	if failures := compatibilityFailures(subject); len(failures) != 0 {
		t.Fatalf("unambiguous source association rejected: %+v", failures)
	}
	if question := aitherCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal); question != nil {
		t.Fatalf("unknown role created an unnecessary association question: %+v", question)
	}
	assertAitherFailure(t, languageAssessment(subject), "language_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	if !reflect.DeepEqual(before, subject.LanguageFacts) || subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
		t.Fatal("source association invented canonical role evidence")
	}
}

func TestAitherAutomaticCompanionAssociationPreservesCanonicalFacts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
	}{
		{name: "standalone second"},
		{name: "standalone first", mutate: func(s *api.TrackerValidationSubject) { slices.Reverse(s.LanguageFacts.Tracks) }},
		{name: "DD plus", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Codec = "DD+" }},
		{name: "unknown TrueHD role", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = "" }},
		{name: "explicit compatibility role", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[1].Role = api.AudioRoleCompatibility
			s.LanguageFacts.Tracks[1].Commentary = true // The producer also uses this flag to exclude compatibility audio from programme aggregation.
		}},
		{name: "normalized language", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Languages = []string{"ja"} }},
		{name: "matching language sets", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"Japanese", "Korean"}
			s.LanguageFacts.Tracks[1].Languages = []string{"Korean", "Japanese", "Japanese"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			before := subject.LanguageFacts.Clone()
			if failures := compatibilityFailures(subject); len(failures) != 0 {
				t.Fatalf("unambiguous companion rejected: %+v", failures)
			}
			if additional := additionalMainAudio(subject); len(additional) != 0 {
				t.Fatalf("inferred companion counted as an additional mix: %+v", additional)
			}
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				if question := aitherCompatibilityQuestionnaire(subject, mode); question != nil {
					for _, field := range question.Fields {
						if strings.HasPrefix(field.Key, "compatibility_mix_") {
							t.Fatalf("inferred companion still asks for association: %+v", field)
						}
					}
				}
			}
			if !reflect.DeepEqual(before, subject.LanguageFacts) {
				t.Fatal("inference changed canonical roles, primary audio, or language evidence")
			}
		})
	}
}

func TestAitherAmbiguousOrDistinctMixesRequireReview(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
	}{
		{name: "multiple matching TrueHD mixes", mutate: func(s *api.TrackerValidationSubject) {
			other := s.LanguageFacts.Tracks[0]
			other.ID = "other-truehd"
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, other)
		}},
		{name: "unidentified matching TrueHD mix", mutate: func(s *api.TrackerValidationSubject) {
			other := s.LanguageFacts.Tracks[0]
			other.ID = ""
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, other)
		}},
		{name: "unknown-language competing TrueHD mix", mutate: func(s *api.TrackerValidationSubject) {
			other := s.LanguageFacts.Tracks[0]
			other.ID = "other-truehd"
			other.Languages = nil
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, other)
		}},
		{name: "only unidentified TrueHD mix", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].ID = "" }},
		{name: "TrueHD commentary", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleCommentary }},
		{name: "TrueHD commentary flag", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Commentary = true }},
		{name: "TrueHD alternate mix", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleAlternateMix }},
		{name: "TrueHD isolated score", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleIsolatedScore }},
		{name: "partial language overlap", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"Japanese", "Korean"}
		}},
		{name: "unknown language in companion", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[1].Languages = []string{"Japanese", "unknown"}
		}},
		{name: "unknown language in TrueHD", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"Japanese", "und"}
		}},
		{name: "unspecified multiple languages", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"Multiple Languages"}
			s.LanguageFacts.Tracks[1].Languages = []string{"Multiple Languages"}
		}},
		{name: "missing standalone identity", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[1].Role = api.AudioRoleCompatibility
			s.LanguageFacts.Tracks[1].ID = ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			test.mutate(&subject)
			assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			question := aitherCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal)
			if question == nil || len(question.Fields) == 0 {
				t.Fatal("unresolved source association lost its review question")
			}
			if slices.Contains(question.Fields[0].Options, "") {
				t.Fatal("unidentified mix became a selectable association")
			}
		})
	}
}

func TestAitherCurrentManualAssociationsRemainAuthoritativeAndEditable(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{"audio-0", "not_compatibility", "unresolved", "", "invalid-mix"} {
		t.Run(answer, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			key := trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd")
			subject.QuestionnaireAnswers = map[string]string{key: answer}
			failures := compatibilityFailures(subject)
			switch answer {
			case "audio-0":
				if len(failures) != 0 {
					t.Fatalf("current explicit association rejected: %+v", failures)
				}
			case "not_compatibility":
				assertAitherFailure(t, failures, "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			default:
				assertAitherFailure(t, failures, "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			}
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				question := aitherCompatibilityQuestionnaire(subject, mode)
				if question == nil || len(question.Fields) != 1 || question.Fields[0].Key != key {
					t.Fatalf("current choice is no longer editable: %+v", question)
				}
				field := question.Fields[0]
				if field.Required != (mode != api.WorkflowExecutionModeDebug) || slices.Contains(field.Options, answer) && field.Value != answer {
					t.Fatalf("manual choice or debug policy changed: %+v", field)
				}
			}
		})
	}
}

func TestAitherStaleAssociationsCannotAuthorizeAmbiguousEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
	}{
		{name: "new generation", mutate: func(s *api.TrackerValidationSubject) { s.Identity.Generation++ }},
		{name: "changed facts", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Title = "Corrected source title" }},
		{name: "changed source", mutate: func(s *api.TrackerValidationSubject) { s.SourcePath = "Other.Example.2026.mkv" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			other := subject.LanguageFacts.Tracks[0]
			other.ID = "other-truehd"
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, other)
			key := trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd")
			subject.QuestionnaireAnswers = map[string]string{key: subject.LanguageFacts.Tracks[0].ID}
			test.mutate(&subject)
			assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			question := aitherCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal)
			if question == nil || len(question.Fields) != 1 || question.Fields[0].Key == key || question.Fields[0].Value != "" {
				t.Fatalf("stale source choice survived changed evidence: %+v", question)
			}

			// A stale rejection also cannot override a fresh, unambiguous inference.
			subject = ordinaryCompatibilitySubject()
			key = trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd")
			subject.QuestionnaireAnswers = map[string]string{key: "not_compatibility"}
			test.mutate(&subject)
			if failures := compatibilityFailures(subject); len(failures) != 0 {
				t.Fatalf("stale rejection blocked fresh unambiguous facts: %+v", failures)
			}
			if question := aitherCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal); question != nil {
				t.Fatalf("stale choice kept an unnecessary question visible: %+v", question)
			}
		})
	}
}

func aitherCompatibilityQuestionnaire(subject api.TrackerValidationSubject, mode api.WorkflowExecutionMode) *api.TrackerQuestionnaire {
	return Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{
		ExecutionMode: mode,
		Meta: api.UploadSubject{
			Type:                        subject.Type,
			Source:                      subject.Source,
			SourcePath:                  subject.SourcePath,
			DiscType:                    subject.DiscType,
			Identity:                    subject.Identity,
			LanguageFacts:               subject.LanguageFacts,
			PersonalRelease:             subject.PersonalRelease,
			Anime:                       subject.Anime,
			TrackerQuestionnaireAnswers: map[string]map[string]string{"AITHER": subject.QuestionnaireAnswers},
		},
	})
}

func TestAITHERUnknownCodecCannotEstablishCompatibility(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	other := subject.LanguageFacts.Tracks[0]
	other.ID, other.Codec = "unknown-codec", ""
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, other)
	companion := subject.LanguageFacts.Tracks[1]
	for _, manual := range []bool{false, true} {
		if manual {
			subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "compatibility_mix_"+companion.ID): other.ID}
		}
		if failures := compatibilityFailures(subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == "language_compatibility_mix" && f.Disposition == api.RuleDispositionStrict
		}) {
			t.Fatalf("unknown-codec mix accepted, manual=%t: %+v", manual, failures)
		}
		question := aitherCompatibilityQuestionnaire(subject, api.WorkflowExecutionModeNormal)
		if question == nil {
			t.Fatal("unknown competing codec created a false unique match")
		}
		for _, field := range question.Fields {
			if slices.Contains(field.Options, other.ID) {
				t.Fatalf("unknown codec offered as a confirmed source: %+v", field)
			}
		}
	}
}
