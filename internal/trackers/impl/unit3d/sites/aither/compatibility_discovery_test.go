// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"slices"
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
			question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}})
			if (question != nil) != test.want {
				t.Fatalf("candidate question=%+v, want discoverable=%v", question, test.want)
			}
			if test.want {
				assertAitherFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
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
	subject.QuestionnaireAnswers = map[string]string{
		trackers.LanguageQuestionKey(subject, "compatibility_mix_standalone-dd"): subject.LanguageFacts.Tracks[0].ID,
	}
	if failures := compatibilityFailures(subject); len(failures) != 0 {
		t.Fatalf("explicit source association rejected: %+v", failures)
	}
	assertAitherFailure(t, languageAssessment(subject), "language_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	if subject.LanguageFacts.Tracks[1].Role != "" || subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
		t.Fatal("source association invented canonical role evidence")
	}
}
