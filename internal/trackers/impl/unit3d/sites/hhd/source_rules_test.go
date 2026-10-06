// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHHDSubtitleManagerReview(t *testing.T) {
	for _, test := range []struct {
		name, answer string
		status       api.MetadataEvidenceStatus
		want         string
		disposition  api.RuleDisposition
	}{
		{"confirmed manager coverage", "available", api.MetadataEvidenceStatusComplete, "", ""},
		{"missing from both", "missing", api.MetadataEvidenceStatusComplete, "language_subtitles", api.RuleDispositionStrict},
		{"unknown manager coverage", "unresolved", api.MetadataEvidenceStatusComplete, "language_subtitles", api.RuleDispositionStrict},
		{"unanswered manager coverage", "", api.MetadataEvidenceStatusComplete, "language_subtitles", api.RuleDispositionStrict},
		{"manager cannot resolve unknown local coverage", "missing", api.MetadataEvidenceStatusPartial, "language_subtitles", api.RuleDispositionStrict},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hhdValidationSubject()
			subject.Source = "WEB"
			subject.LanguageFacts = hhdTestLanguageFacts("Japanese", []string{"Japanese", "English"}, nil)
			subject.LanguageFacts.SubtitleStatus = test.status
			hhdAnswer(&subject, "english_subtitle_manager", test.answer)
			failures := languageAssessment(subject)
			if test.want == "" {
				hhdRequireNoFailures(t, failures)
				return
			}
			status := api.MetadataEvidenceStatusPartial
			if test.answer == "missing" && test.status == api.MetadataEvidenceStatusComplete {
				status = api.MetadataEvidenceStatusComplete
			}
			requireHHDValidationFailure(t, failures, test.want, test.disposition, status)
		})
	}
}

func TestHHDSourceDiscRetention(t *testing.T) {
	for _, test := range []struct {
		name, answer string
		personal     bool
		want         api.RuleDisposition
		status       api.MetadataEvidenceStatus
	}{
		{"retained primary material", "retained", true, "", ""},
		{"ordinary omission is guidance", "incomplete", false, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete},
		{"personal omission is prohibited", "incomplete", true, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete},
		{"ordinary unknown is visible guidance", "unresolved", false, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial},
		{"personal unknown is unresolved", "unresolved", true, api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial},
		{"disc cannot claim no disc source", "no_disc_source", true, api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hhdValidationSubject()
			subject.Type, subject.Source, subject.PersonalRelease = "REMUX", "BluRay", test.personal
			hhdAnswer(&subject, "source_disc_audio", test.answer)
			failures := languageAssessment(subject)
			if test.want == "" {
				hhdRequireNoFailures(t, failures)
				return
			}
			requireHHDValidationFailure(t, failures, "language_source_disc_audio", test.want, test.status)
		})
	}
}

func TestHHDCompatibilitySourceEvidence(t *testing.T) {
	for _, test := range []struct {
		name, source, main, compat, answer, want string
		status                                   api.MetadataEvidenceStatus
	}{
		{"untouched web HLS permitted", "WEB", "AAC", "AAC", "untouched_hls", "", ""},
		{"web provenance remains unknown", "WEB", "AAC", "AAC", "", "language_compatibility_source", api.MetadataEvidenceStatusPartial},
		{"HLS cannot excuse disc codec", "BluRay", "DTS-HD MA", "AAC", "untouched_hls", "language_compatibility_format", api.MetadataEvidenceStatusComplete},
		{"ordinary AC3 provenance", "WEB", "TrueHD", "AC-3", "not_duplicated_core", "", ""},
		{"duplicated embedded core prohibited", "WEB", "DTS-HD MA", "DD", "duplicated_core", "language_compatibility_core", api.MetadataEvidenceStatusComplete},
		{"HLS cannot replace TrueHD AC3", "WEB", "TrueHD", "AAC", "untouched_hls", "language_compatibility_missing", api.MetadataEvidenceStatusComplete},
		{"unsupported format source does not waive", "WEB", "AAC", "DTS", "not_duplicated_core", "language_compatibility_format", api.MetadataEvidenceStatusComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hhdCompatibilitySubject(test.main, test.compat)
			subject.Source = test.source
			if test.source != "WEB" {
				subject.Type = "ENCODE"
				hhdAnswer(&subject, "source_disc_audio", "retained")
			}
			hhdAnswer(&subject, "compatibility_source_compat-0", test.answer)
			failures := languageAssessment(subject)
			if test.want == "" {
				hhdRequireNoFailures(t, failures)
				return
			}
			requireHHDValidationFailure(t, failures, test.want, api.RuleDispositionStrict, test.status)
		})
	}
}

func TestHHDCompatibilityAssociation(t *testing.T) {
	subject := hhdCompatibilitySubject("TrueHD", "AC-3")
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks,
		api.MediaTrackFacts{
			ID:        "audio-1",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleAlternateMix,
			Languages: []string{"English"},
			Codec:     "TrueHD",
		},
		api.MediaTrackFacts{
			ID:        "compat-1",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleCompatibility,
			Languages: []string{"English"},
			Codec:     "AC-3",
		},
	)
	for _, id := range []string{"compat-0", "compat-1"} {
		hhdAnswer(&subject, "compatibility_source_"+id, "not_duplicated_core")
	}
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	hhdAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
	hhdAnswer(&subject, "compatibility_mix_compat-1", "audio-1")
	hhdRequireNoFailures(t, languageAssessment(subject))
	hhdAnswer(&subject, "compatibility_mix_compat-1", "audio-0")
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_count", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestHHDQuestionnaireReachabilityAndEvidenceBinding(t *testing.T) {
	subject := hhdCompatibilitySubject("AAC", "AAC")
	subject.LanguageFacts = hhdTestLanguageFacts("Japanese", []string{"Japanese", "English"}, nil)
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "compat-0",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Languages: []string{"Japanese"},
		Codec:     "AAC",
	})
	hhdAnswer(&subject, "english_subtitle_manager", "available")
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	hhdRequireNoFailures(t, languageAssessment(subject))
	questionnaire := hhdProjectedQuestionnaire(subject)
	if questionnaire == nil {
		t.Fatal("missing HHD source questionnaire")
	}
	for _, key := range []string{"english_subtitle_manager", "compatibility_source_compat-0"} {
		index := slices.IndexFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool {
			return field.Key == trackers.LanguageQuestionKey(subject, key)
		})
		if index < 0 || questionnaire.Fields[index].Value == "" {
			t.Fatalf("review %s is unreachable or lost: %+v", key, questionnaire)
		}
	}
	subject.Identity.Generation++
	requireHHDValidationFailure(t, languageAssessment(subject), "language_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.DiscType, subject.Type = "BDMV", "DISC"
	hhdRequireNoFailures(t, languageAssessment(subject))
	if got := hhdProjectedQuestionnaire(subject); got != nil {
		t.Fatalf("full disc gained questionnaire: %+v", got)
	}
	subject.Type = "REMUX"
	if got := hhdProjectedQuestionnaire(subject); got == nil {
		t.Fatal("disc-sourced remux escaped review")
	}
}

func hhdCompatibilitySubject(main, compat string) api.TrackerValidationSubject {
	subject := hhdValidationSubject()
	subject.Source = "WEB"
	subject.LanguageFacts.Tracks[0].Codec = main
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "compat-0",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Languages: []string{"English"},
		Codec:     compat,
	})
	return subject
}

func hhdAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = map[string]string{}
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func hhdProjectedQuestionnaire(subject api.TrackerValidationSubject) *api.TrackerQuestionnaire {
	callback := Profile().Site.ProjectionQuestionnaire
	if callback == nil {
		return nil
	}
	return callback(trackers.PreparationInput{Meta: api.UploadSubject{
		DiscType:                    subject.DiscType,
		Type:                        subject.Type,
		Source:                      subject.Source,
		SourcePath:                  subject.SourcePath,
		LanguageFacts:               subject.LanguageFacts,
		Identity:                    subject.Identity,
		PersonalRelease:             subject.PersonalRelease,
		TrackerQuestionnaireAnswers: map[string]map[string]string{"HHD": subject.QuestionnaireAnswers},
	}})
}

func hhdRequireNoFailures(t *testing.T, failures []api.RuleFailure) {
	t.Helper()
	if len(failures) != 0 {
		t.Fatalf("unexpected HHD findings: %+v", failures)
	}
}

func TestHHDSourceAnswersCannotWaiveProgrammeRules(t *testing.T) {
	subject := hhdValidationSubject()
	subject.Source = "WEB"
	subject.LanguageFacts = hhdTestLanguageFacts("Japanese", []string{"English", "German"}, nil)
	hhdAnswer(&subject, "english_subtitle_manager", "available")
	failures := languageAssessment(subject)
	for _, rule := range []string{"language_original", "language_extra_dub"} {
		requireHHDValidationFailure(t, failures, rule, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	for _, failure := range failures {
		if strings.Contains(failure.Rule, "subtitles") {
			t.Fatalf("manager coverage ignored: %+v", failure)
		}
	}
}

func TestHHDCompatibilityMeasuredBoundsSurviveAnswers(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
		want   string
		status api.MetadataEvidenceStatus
	}{
		{"no standalone AC3", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:2]
		}, "language_compatibility_missing", api.MetadataEvidenceStatusComplete},
		{"embedded AC3 is insufficient", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:2]
			subject.LanguageFacts.Tracks[0].EmbeddedCompatibility = true
		}, "language_compatibility_missing", api.MetadataEvidenceStatusComplete},
		{"missing codec evidence", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks[0].Codec = ""
		}, "language_compatibility_evidence", api.MetadataEvidenceStatusPartial},
		{"two compatibility tracks for one mix", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
				ID:        "compat-1",
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleCompatibility,
				Languages: []string{"English"},
				Codec:     "AC-3",
			})
		}, "language_compatibility_count", api.MetadataEvidenceStatusComplete},
		{"different resource cannot cover mix", func(subject *api.TrackerValidationSubject) {
			subject.LanguageFacts.Tracks[0].ResourceID = "episode-a"
			subject.LanguageFacts.Tracks[2].ResourceID = "episode-b"
		}, "language_compatibility_missing", api.MetadataEvidenceStatusComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hhdCompatibilitySubject("TrueHD", "AC-3")
			test.mutate(&subject)
			for _, id := range []string{"compat-0", "compat-1"} {
				hhdAnswer(&subject, "compatibility_source_"+id, "not_duplicated_core")
			}
			hhdAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
			requireHHDValidationFailure(t, languageAssessment(subject), test.want, api.RuleDispositionStrict, test.status)
		})
	}
}

func TestHHDQuestionnaireKeepsSourceAttestationsSeparate(t *testing.T) {
	subject := hhdCompatibilitySubject("TrueHD", "AC-3")
	subject.Type, subject.Source = "REMUX", "BluRay"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "audio-1",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleAlternateMix,
		Languages: []string{"English"},
		Codec:     "TrueHD",
	})
	questionnaire := hhdProjectedQuestionnaire(subject)
	for _, key := range []string{"source_disc_audio", "compatibility_source_compat-0", "compatibility_mix_compat-0"} {
		index := slices.IndexFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool {
			return field.Key == trackers.LanguageQuestionKey(subject, key)
		})
		if index < 0 {
			t.Fatalf("missing source review %s", key)
		}
		field := questionnaire.Fields[index]
		if field.Value != "" {
			t.Fatalf("invented source evidence: %+v", field)
		}
		if key == "source_disc_audio" {
			if field.Required || !strings.Contains(field.Help, "additional sources") {
				t.Fatalf("ordinary guidance became mandatory or lost additional sources: %+v", field)
			}
		} else if !field.Required {
			t.Fatalf("required compatibility review became optional: %+v", field)
		}
		if slices.Contains(field.Options, "untouched_hls") || slices.Contains(field.Options, "no_disc_source") {
			t.Fatalf("disc source exposes contradictory answers: %+v", field)
		}
	}
	subject.PersonalRelease = true
	questionnaire = hhdProjectedQuestionnaire(subject)
	if !questionnaire.Fields[0].Required {
		t.Fatal("personal source retention is not required")
	}
	hhdAnswer(&subject, "source_disc_audio", "retained")
	subject.LanguageFacts.Tracks[0].Title = "Updated source mix"
	requireHHDValidationFailure(t, languageAssessment(subject), "language_source_disc_audio", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestHHDSourceChangeInvalidatesAttestation(t *testing.T) {
	subject := hhdCompatibilitySubject("AAC", "AAC")
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	hhdRequireNoFailures(t, languageAssessment(subject))
	subject.Type = "ENCODE"
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_source", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestHHDUnknownCompatibilityCannotBecomeConfirmedAbsence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.MediaTrackFacts)
	}{
		{"unknown compatibility language", func(track *api.MediaTrackFacts) { track.Languages = nil }},
		{"unknown compatibility codec", func(track *api.MediaTrackFacts) { track.Codec = "" }},
		{"unidentified compatibility track", func(track *api.MediaTrackFacts) { track.ID = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hhdCompatibilitySubject("TrueHD", "AC-3")
			test.mutate(&subject.LanguageFacts.Tracks[2])
			hhdAnswer(&subject, "compatibility_source_"+subject.LanguageFacts.Tracks[2].ID, "not_duplicated_core")
			failures := languageAssessment(subject)
			requireHHDValidationFailure(t, failures, "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
		})
	}
}

func TestHHDDiscAliasesCannotDenyPersonalRetention(t *testing.T) {
	for _, source := range []string{"BLU RAY", "BLU-RAY 3D", "BD", "BDMV", "PAL DVD", "NTSC DVD", "HD DVD"} {
		t.Run(source, func(t *testing.T) {
			subject := hhdValidationSubject()
			subject.Type, subject.Source, subject.PersonalRelease = "ENCODE", source, true
			hhdAnswer(&subject, "source_disc_audio", "no_disc_source")
			requireHHDValidationFailure(t, languageAssessment(subject), "language_source_disc_audio", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			question := hhdProjectedQuestionnaire(subject)
			if question == nil || len(question.Fields) != 1 || !question.Fields[0].Required || slices.Contains(question.Fields[0].Options, "no_disc_source") {
				t.Fatalf("known disc permits contradictory source evidence: %#v", question)
			}
			hhdAnswer(&subject, "source_disc_audio", "retained")
			hhdRequireNoFailures(t, languageAssessment(subject))
			subject.Type = "DISC"
			hhdRequireNoFailures(t, languageAssessment(subject))
			if hhdProjectedQuestionnaire(subject) != nil {
				t.Fatal("full disc gained review")
			}
		})
	}
}

func TestHHDUnknownVideoSourceDoesNotProhibitPossibleHLS(t *testing.T) {
	for _, source := range []string{"", "Mixed", "Unknown", "unrecognized source", "WEB-archive"} {
		for _, answer := range []string{"", "untouched_hls", "not_duplicated_core"} {
			t.Run(source+"/"+answer, func(t *testing.T) {
				subject := hhdCompatibilitySubject("AAC", "AAC")
				subject.Type, subject.Source = "ENCODE", source
				hhdAnswer(&subject, "source_disc_audio", "no_disc_source")
				hhdAnswer(&subject, "compatibility_source_compat-0", answer)
				requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
				subject.Source = "WEB"
				hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
				hhdRequireNoFailures(t, languageAssessment(subject))
				subject.Source = "HDTV"
				hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
				requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			})
		}
	}
}

func TestHHDUnknownVideoSourceCannotWaiveKnownViolations(t *testing.T) {
	subject := hhdCompatibilitySubject("TrueHD", "AAC")
	subject.Type, subject.Source = "ENCODE", "Unknown"
	hhdAnswer(&subject, "source_disc_audio", "no_disc_source")
	hhdAnswer(&subject, "compatibility_source_compat-0", "duplicated_core")
	for _, rule := range []string{"language_compatibility_core", "language_compatibility_format", "language_compatibility_missing"} {
		requireHHDValidationFailure(t, languageAssessment(subject), rule, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Source = "WEB"
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestHHDKnownWebSourceFormsKeepHLSReview(t *testing.T) {
	for _, source := range []string{"WEB-DL", "WEBDL", "WEBRip", "WEB-RIP"} {
		t.Run(source, func(t *testing.T) {
			subject := hhdCompatibilitySubject("AAC", "AAC")
			subject.Type, subject.Source, subject.PersonalRelease = "ENCODE", source, true
			hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
			hhdRequireNoFailures(t, languageAssessment(subject))
			question := hhdProjectedQuestionnaire(subject)
			if question == nil || len(question.Fields) != 1 || !slices.Contains(question.Fields[0].Options, "untouched_hls") {
				t.Fatalf("known WEB source lost its bounded HLS review: %#v", question)
			}
		})
	}
}
