// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func ordinaryCompatibilitySubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:  "LST",
		Type:     "REMUX",
		Source:   "BluRay",
		Identity: api.ExternalIdentity{Generation: 1},
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			PrimaryAudioTrackID:   "main",
			Tracks: []api.MediaTrackFacts{
				{
					ID:               "main",
					ResourceID:       "media",
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleProgramme,
					Codec:            "TrueHD Atmos",
					Languages:        []string{"English"},
					Default:          true,
					DefaultKnown:     true,
					StreamOrder:      1,
					StreamOrderKnown: true,
				},
				{
					ID:               "companion",
					ResourceID:       "media",
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleProgramme,
					Codec:            "AC-3",
					Languages:        []string{"English"},
					DefaultKnown:     true,
					StreamOrder:      2,
					StreamOrderKnown: true,
				},
			},
		}),
	}
}

func ordinaryCompatibilityQuestionnaire(subject api.TrackerValidationSubject) *api.TrackerQuestionnaire {
	return languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
		Type:                        subject.Type,
		Source:                      subject.Source,
		DiscType:                    subject.DiscType,
		Identity:                    subject.Identity,
		LanguageFacts:               subject.LanguageFacts,
		TrackerQuestionnaireAnswers: map[string]map[string]string{"LST": subject.QuestionnaireAnswers},
	}})
}

func TestLSTOrdinaryCompatibilityIsAutomatic(t *testing.T) {
	for _, codec := range []string{"DD", "AC-3", "DD+", "DDP", "E-AC3", "E-AC-3", "DD+ Atmos"} {
		for _, role := range []api.AudioTrackRole{api.AudioRoleProgramme, ""} {
			t.Run(codec+"/"+string(role), func(t *testing.T) {
				subject := ordinaryCompatibilitySubject()
				subject.LanguageFacts.Tracks[1].Codec, subject.LanguageFacts.Tracks[1].Role = codec, role
				companion := subject.LanguageFacts.Tracks[1]
				if got := compatibilityMix(subject, companion, compatibilityMixes(subject.LanguageFacts, companion)); got != "main" {
					t.Fatalf("clear measured relationship not resolved: %q", got)
				}
				failures := compatibilityFailures(subject)
				for _, failure := range failures {
					if failure.Disposition != api.RuleDispositionAdvisory {
						t.Fatalf("ordinary companion blocked: %+v", failure)
					}
				}
				if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
					return f.Rule == "language_compatibility_source" && strings.HasPrefix(f.Reason, "Guidance")
				}) {
					t.Fatalf("companion lost passive visible guidance: %+v", failures)
				}
				for _, defect := range audioDefects(subject) {
					if defect.key == "redundant_programme" {
						t.Fatalf("accepted companion treated as redundant programme audio: %+v", defect)
					}
				}
				if question := ordinaryCompatibilityQuestionnaire(subject); question != nil {
					t.Fatalf("clear measured relationship asks for input: %+v", question)
				}
				if subject.LanguageFacts.Tracks[1].Role != role {
					t.Fatal("tracker policy changed canonical role")
				}
			})
		}
	}
}

func TestLSTAutomaticCompatibilityKeepsUncertainty(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
	}{
		{"different language", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Languages = []string{"French"} }},
		{"partial companion language", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[1].Languages = []string{"English", "und"}
		}},
		{"unknown parent language", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Languages = nil }},
		{"overlapping language set", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"English", "French"}
		}},
		{"different resource", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ResourceID = "another-media" }},
		{"same identity", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ID = "main" }},
		{"missing companion identity", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ID = "" }},
		{"commentary flag", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Commentary = true }},
		{"commentary role", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = api.AudioRoleCommentary }},
		{"isolated score", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = api.AudioRoleIsolatedScore }},
		{"embedded companion", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].EmbeddedCompatibility = true }},
		{"embedded title", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Title = "Embedded core" }},
		{"unidentified plausible mix", func(s *api.TrackerValidationSubject) {
			track := s.LanguageFacts.Tracks[0]
			track.ID = ""
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, track)
		}},
		{"distinct mix", func(s *api.TrackerValidationSubject) {
			track := s.LanguageFacts.Tracks[0]
			track.ID, track.Role = "alternate", api.AudioRoleAlternateMix
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, track)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			test.mutate(&subject)
			failures := compatibilityFailures(subject)
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_compatibility_missing" && f.Disposition == api.RuleDispositionStrict
			}) {
				t.Fatalf("uncertain or ineligible companion covered TrueHD: %+v", failures)
			}
			if question := ordinaryCompatibilityQuestionnaire(subject); question != nil {
				for _, field := range question.Fields {
					if slices.Contains(field.Options, "") {
						t.Fatalf("unidentified mix exposed as an empty choice: %+v", field)
					}
				}
			}
		})
	}
}

func TestLSTAutomaticCompatibilityPreservesManualAnswers(t *testing.T) {
	for _, answer := range []string{"", "unresolved", "not_compatibility", "missing", "main"} {
		t.Run(answer, func(t *testing.T) {
			subject := ordinaryCompatibilitySubject()
			key := trackers.LanguageQuestionKey(subject, "compatibility_mix_companion")
			subject.QuestionnaireAnswers = map[string]string{key: answer}
			failures := compatibilityFailures(subject)
			blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Disposition == api.RuleDispositionStrict })
			if blocked != (answer != "main") {
				t.Fatalf("manual answer %q overridden: %+v", answer, failures)
			}
			question := ordinaryCompatibilityQuestionnaire(subject)
			if question == nil || len(question.Fields) != 1 || question.Fields[0].Key != key {
				t.Fatalf("manual association is not editable: %+v", question)
			}
			if slices.Contains(question.Fields[0].Options, answer) && question.Fields[0].Value != answer {
				t.Fatalf("manual choice not shown: %+v", question.Fields[0])
			}
		})
	}
}

func TestLSTAutomaticCompatibilityDoesNotReuseStaleAnswers(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	alternate := subject.LanguageFacts.Tracks[0]
	alternate.ID = "alternate"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, alternate)
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "compatibility_mix_companion"): "main"}
	subject.Identity.Generation++
	companion := subject.LanguageFacts.Tracks[1]
	if got := compatibilityMix(subject, companion, compatibilityMixes(subject.LanguageFacts, companion)); got != "" {
		t.Fatalf("stale association reused: %q", got)
	}
	question := ordinaryCompatibilityQuestionnaire(subject)
	if question == nil || question.Fields[0].Value != "" {
		t.Fatalf("stale answer appeared in questionnaire: %+v", question)
	}
}

func TestLSTExplicitCompatibilityKeepsLegacyCommentaryFlag(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleCompatibility
	subject.LanguageFacts.Tracks[1].Commentary = true
	for _, failure := range compatibilityFailures(subject) {
		if failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("title-labeled compatibility lost its legacy flag support: %+v", failure)
		}
	}
}

func TestLSTAutomaticCompatibilityPreservesFullDiscBoundary(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.Type, subject.DiscType = "DISC", "BDMV"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired language policy: %+v", failures)
	}
	if question := ordinaryCompatibilityQuestionnaire(subject); question != nil {
		t.Fatalf("full disc acquired compatibility input: %+v", question)
	}
}

func TestLSTManualAssociationKeepsSpecialSourceMixes(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleAlternateMix, api.AudioRoleCommentary} {
		subject := ordinaryCompatibilitySubject()
		subject.LanguageFacts.Tracks[0].Role, subject.LanguageFacts.Tracks[0].Commentary = role, true
		companion := subject.LanguageFacts.Tracks[1]
		if got := compatibilityMix(subject, companion, compatibilityMixes(subject.LanguageFacts, companion)); got != "" {
			t.Fatalf("special source mix inferred automatically: %q", got)
		}
		subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "compatibility_mix_companion"): "main"}
		for _, failure := range compatibilityFailures(subject) {
			if failure.Disposition != api.RuleDispositionAdvisory {
				t.Fatalf("explicit special source association rejected: %+v", failure)
			}
		}
		question := ordinaryCompatibilityQuestionnaire(subject)
		if question == nil || !slices.Contains(question.Fields[0].Options, "main") || question.Fields[0].Value != "main" {
			t.Fatalf("special source option removed: %+v", question)
		}
	}
}

func TestLSTDifferentKnownLanguagesDoNotCreateFalseAmbiguity(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	other := subject.LanguageFacts.Tracks[0]
	other.ID, other.Languages = "french-mix", []string{"French"}
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, other)
	companion := subject.LanguageFacts.Tracks[1]
	if got := compatibilityMix(subject, companion, compatibilityMixes(subject.LanguageFacts, companion)); got != "main" {
		t.Fatalf("unrelated language mix created ambiguity: %q", got)
	}
}

func TestLSTRejectedCompanionRetainsRedundantProgrammeGuidance(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "compatibility_mix_companion"): "not_compatibility"}
	if !slices.ContainsFunc(audioDefects(subject), func(defect audioDefect) bool { return defect.key == "redundant_programme" }) {
		t.Fatal("rejected companion still exempted from redundancy guidance")
	}
}

func TestLSTClearMeasuredReleaseNeedsNoCompatibilityReview(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].Codec = "DD+ Atmos"
	for _, failure := range languageAssessment(subject) {
		if failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("clear measured release blocked: %+v", failure)
		}
	}
	if question := ordinaryCompatibilityQuestionnaire(subject); question != nil {
		t.Fatalf("clear measured release requires input: %+v", question)
	}
}

func TestLSTUnknownCodecCannotEstablishCompatibility(t *testing.T) {
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
		question := ordinaryCompatibilityQuestionnaire(subject)
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
