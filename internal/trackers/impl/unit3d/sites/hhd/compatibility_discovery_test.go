// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func ordinaryCompatibilitySubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:  "HHD",
		Type:     "WEBDL",
		Source:   "WEB",
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
		TrackerQuestionnaireAnswers: map[string]map[string]string{"HHD": subject.QuestionnaireAnswers},
	}})
}

func TestHHDOrdinaryCompatibilityIsAutomatic(t *testing.T) {
	for _, codec := range []string{"DD", "AC-3", "DD+", "DDP", "E-AC3", "E-AC-3"} {
		for _, role := range []api.AudioTrackRole{api.AudioRoleProgramme, ""} {
			t.Run(codec+"/"+string(role), func(t *testing.T) {
				subject := ordinaryCompatibilitySubject()
				subject.LanguageFacts.Tracks[1].Codec, subject.LanguageFacts.Tracks[1].Role = codec, role
				companion := subject.LanguageFacts.Tracks[1]
				if got := compatibilityMix(subject, companion, compatibilityMixes(subject, companion)); got != "main" {
					t.Fatalf("clear measured relationship not resolved: %q", got)
				}
				failures := compatibilityFailures(subject)
				if codec != "DD" && codec != "AC-3" {
					if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
						return f.Rule == "language_compatibility_missing" && f.Disposition == api.RuleDispositionStrict
					}) {
						t.Fatalf("DD+ waived mandatory standalone AC-3: %+v", failures)
					}
				} else {
					hhdRequireNoBlockingFailures(t, failures)
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

func TestHHDAutomaticCompatibilityKeepsUncertainty(t *testing.T) {
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

func TestHHDAutomaticCompatibilityPreservesManualAnswers(t *testing.T) {
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

func TestHHDAutomaticCompatibilityDoesNotReuseStaleAnswers(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	alternate := subject.LanguageFacts.Tracks[0]
	alternate.ID = "alternate"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, alternate)
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "compatibility_mix_companion"): "main"}
	subject.Identity.Generation++
	companion := subject.LanguageFacts.Tracks[1]
	if got := compatibilityMix(subject, companion, compatibilityMixes(subject, companion)); got != "" {
		t.Fatalf("stale association reused: %q", got)
	}
	question := ordinaryCompatibilityQuestionnaire(subject)
	if question == nil || question.Fields[0].Value != "" {
		t.Fatalf("stale answer appeared in questionnaire: %+v", question)
	}
}

func TestHHDExplicitCompatibilityKeepsLegacyCommentaryFlag(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleCompatibility
	subject.LanguageFacts.Tracks[1].Commentary = true
	for _, failure := range compatibilityFailures(subject) {
		if failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("title-labeled compatibility lost its legacy flag support: %+v", failure)
		}
	}
}

func TestHHDAutomaticCompatibilityPreservesFullDiscBoundary(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.Type, subject.DiscType = "DISC", "BDMV"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired language policy: %+v", failures)
	}
	if question := ordinaryCompatibilityQuestionnaire(subject); question != nil {
		t.Fatalf("full disc acquired compatibility input: %+v", question)
	}
}

func TestHHDManualAssociationKeepsSpecialSourceMixes(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleAlternateMix, api.AudioRoleCommentary} {
		subject := ordinaryCompatibilitySubject()
		subject.LanguageFacts.Tracks[0].Role, subject.LanguageFacts.Tracks[0].Commentary = role, true
		companion := subject.LanguageFacts.Tracks[1]
		if got := compatibilityMix(subject, companion, compatibilityMixes(subject, companion)); got != "" {
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

func TestHHDDifferentKnownLanguagesDoNotCreateFalseAmbiguity(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	other := subject.LanguageFacts.Tracks[0]
	other.ID, other.Languages = "french-mix", []string{"French"}
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, other)
	companion := subject.LanguageFacts.Tracks[1]
	if got := compatibilityMix(subject, companion, compatibilityMixes(subject, companion)); got != "main" {
		t.Fatalf("unrelated language mix created ambiguity: %q", got)
	}
}

func TestHHDOrdinaryCompanionsKeepCountLimitWithoutFalseParentAmbiguity(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	second := subject.LanguageFacts.Tracks[1]
	second.ID = "second-companion"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, second)
	requireHHDValidationFailure(t, compatibilityFailures(subject), "language_compatibility_count", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.LanguageFacts.Tracks[2].Role, subject.LanguageFacts.Tracks[2].Codec = api.AudioRoleCompatibility, "AAC"
	failures := compatibilityFailures(subject)
	requireHHDValidationFailure(t, failures, "language_compatibility_count", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	for _, failure := range failures {
		if failure.Rule == "language_compatibility_mix" || failure.Rule == "language_compatibility_missing" {
			t.Fatalf("ordinary companion treated as a source mix: %+v", failure)
		}
	}
}

func TestHHDExplicitEmbeddedCompanionCannotCoverStandaloneRequirement(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleCompatibility
	subject.LanguageFacts.Tracks[1].EmbeddedCompatibility = true
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "compatibility_mix_companion"): "main"}
	requireHHDValidationFailure(t, compatibilityFailures(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestHHDExplicitCompatibilityKeepsNonTrueHDParents(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[0].Codec = "DD"
	subject.LanguageFacts.Tracks[1].Role, subject.LanguageFacts.Tracks[1].Codec = api.AudioRoleCompatibility, "AAC"
	hhdRequireNoBlockingFailures(t, compatibilityFailures(subject))
}

func TestHHDClearMeasuredReleaseNeedsNoCompatibilityReview(t *testing.T) {
	subject := ordinaryCompatibilitySubject()

	for _, failure := range languageAssessment(subject) {
		if failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("clear measured release blocked: %+v", failure)
		}
	}
	if question := ordinaryCompatibilityQuestionnaire(subject); question != nil {
		t.Fatalf("clear measured release requires input: %+v", question)
	}
}

func TestHHDManualNonCompatibilityStreamRemainsSourceMix(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	subject.LanguageFacts.Tracks[1].ID = "dd-a"
	companion := subject.LanguageFacts.Tracks[1]
	companion.ID = "dd-b"
	aac := companion
	aac.ID, aac.Codec, aac.Role = "aac", "AAC", api.AudioRoleCompatibility
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, companion, aac)
	hhdAnswer(&subject, "compatibility_mix_dd-a", "not_compatibility")
	hhdAnswer(&subject, "compatibility_mix_dd-b", "main")
	hhdAnswer(&subject, "compatibility_mix_aac", "dd-a")
	hhdRequireNoBlockingFailures(t, compatibilityFailures(subject))
	question := ordinaryCompatibilityQuestionnaire(subject)
	if question == nil {
		t.Fatal("manual source choices became inaccessible")
	}
	index := slices.IndexFunc(question.Fields, func(field api.TrackerQuestionnaireField) bool {
		return field.Key == trackers.LanguageQuestionKey(subject, "compatibility_mix_aac")
	})
	if index < 0 || !slices.Contains(question.Fields[index].Options, "dd-a") || question.Fields[index].Value != "dd-a" {
		t.Fatalf("manual non-compatibility parent removed: %+v", question)
	}
	hhdAnswer(&subject, "compatibility_mix_dd-a", "main")
	requireHHDValidationFailure(t, compatibilityFailures(subject), "language_compatibility_count", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestHHDUnknownCodecParentRemainsUnresolvedAndUnselectable(t *testing.T) {
	subject := ordinaryCompatibilitySubject()
	unknown := subject.LanguageFacts.Tracks[0]
	unknown.ID, unknown.Codec = "unknown", ""
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, unknown)
	hhdAnswer(&subject, "compatibility_mix_companion", "unknown")
	requireHHDValidationFailure(t, compatibilityFailures(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	question := ordinaryCompatibilityQuestionnaire(subject)
	if question == nil {
		t.Fatal("unknown source association lost review")
	}
	for _, field := range question.Fields {
		if slices.Contains(field.Options, "unknown") || field.Value == "unknown" {
			t.Fatalf("unknown codec exposed as a valid source choice: %+v", field)
		}
	}
}

func TestHHDUnidentifiedDolbyLanguageRemainsPossibleSourceMix(t *testing.T) {
	for _, languages := range [][]string{nil, {"English", "und"}} {
		subject := ordinaryCompatibilitySubject()
		subject.LanguageFacts.Tracks[1].Languages = languages
		aac := subject.LanguageFacts.Tracks[0]
		aac.ID, aac.Codec, aac.Role = "aac", "AAC", api.AudioRoleCompatibility
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, aac)
		hhdAnswer(&subject, "compatibility_mix_companion", "main")
		candidates := compatibilityMixes(subject, aac)
		if !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == "companion" }) {
			t.Fatalf("unknown-language Dolby parent silently removed: %+v", candidates)
		}
		requireHHDValidationFailure(t, compatibilityFailures(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	}
}
