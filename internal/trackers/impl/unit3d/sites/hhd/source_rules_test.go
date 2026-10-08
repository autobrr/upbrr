// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"encoding/hex"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHHDSubtitleStatusRequiresAcknowledgement(t *testing.T) {
	for _, status := range []api.MetadataEvidenceStatus{api.MetadataEvidenceStatusComplete, api.MetadataEvidenceStatusPartial} {
		for _, answer := range []string{"", "available", "missing", "unresolved"} {
			subject := hhdValidationSubject()
			subject.Source = "WEB"
			subject.LanguageFacts = hhdTestLanguageFacts("Japanese", []string{"Japanese", "English"}, nil)
			subject.LanguageFacts.SubtitleStatus = status
			hhdAnswer(&subject, "english_subtitle_manager", answer)
			failures := languageAssessment(subject)
			requireHHDValidationFailure(t, failures, "language_subtitles", api.RuleDispositionWaivable, status)
			fingerprint, err := hex.DecodeString(string(failures[0].EvidenceFingerprint))
			if err != nil || len(fingerprint) != 32 {
				t.Fatalf("invalid acknowledgement evidence fingerprint: %q", failures[0].EvidenceFingerprint)
			}
			if len(failures) != 1 || failures[0].EvidenceFingerprint == "" || !trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeNormal, false) || trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeNormal, true) {
				t.Fatalf("actual subtitle status did not use tracker acknowledgement: %+v", failures)
			}
			if got := hhdProjectedQuestionnaire(subject); got != nil {
				t.Fatalf("subtitle status became a questionnaire: %+v", got)
			}
			subject.Identity.Generation++
			if languageAssessment(subject)[0].EvidenceFingerprint == failures[0].EvidenceFingerprint {
				t.Fatal("changed generation reused acknowledgement evidence")
			}
			subject.LanguageFacts = hhdTestLanguageFacts("Japanese", []string{"Japanese", "English"}, []string{"English"})
			hhdRequireNoFailures(t, languageAssessment(subject))
		}
	}
}

func TestHHDSourceDiscRetentionIsAdvisory(t *testing.T) {
	for _, personal := range []bool{false, true} {
		for _, answer := range []string{"", "retained", "incomplete", "unresolved", "no_disc_source"} {
			subject := hhdValidationSubject()
			subject.Type, subject.Source, subject.PersonalRelease = "REMUX", "BluRay", personal
			hhdAnswer(&subject, "source_disc_audio", answer)
			failures := languageAssessment(subject)
			requireHHDValidationFailure(t, failures, "language_source_disc_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
			hhdRequireNoBlockingFailures(t, failures)
			if got := hhdProjectedQuestionnaire(subject); got != nil {
				t.Fatalf("retention became a questionnaire: %+v", got)
			}
		}
	}
}

func TestHHDCompatibilitySourceIsAdvisoryAndMeasuredRulesRemain(t *testing.T) {
	for _, test := range []struct{ name, source, main, compat, want string }{
		{"web HLS provenance", "WEB", "AAC", "AAC", ""},
		{"disc unsupported codec", "BluRay", "DTS-HD MA", "AAC", "language_compatibility_format"},
		{"standalone AC3", "WEB", "TrueHD", "AC-3", ""},
		{"possible duplicated core", "WEB", "DTS-HD MA", "DD", ""},
		{"HLS cannot replace TrueHD AC3", "WEB", "TrueHD", "AAC", "language_compatibility_missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, answer := range []string{"", "untouched_hls", "not_duplicated_core", "duplicated_core"} {
				subject := hhdCompatibilitySubject(test.main, test.compat)
				subject.Type, subject.Source = "ENCODE", test.source
				hhdAnswer(&subject, "compatibility_source_compat-0", answer)
				failures := languageAssessment(subject)
				requireHHDValidationFailure(t, failures, "language_compatibility_source", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
				if test.want != "" {
					requireHHDValidationFailure(t, failures, test.want, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
				} else {
					hhdRequireNoBlockingFailures(t, failures)
				}
			}
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
	hhdRequireNoBlockingFailures(t, languageAssessment(subject))
	hhdAnswer(&subject, "compatibility_mix_compat-1", "audio-0")
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_count", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestHHDQuestionnaireOnlyAsksAmbiguousMixAssociation(t *testing.T) {
	subject := hhdCompatibilitySubject("TrueHD", "AC-3")
	if question := hhdProjectedQuestionnaire(subject); question != nil {
		t.Fatalf("unambiguous mix gained reassurance: %+v", question)
	}
	track := subject.LanguageFacts.Tracks[0]
	track.ID, track.Role = "other-mix", api.AudioRoleAlternateMix
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
	question := hhdProjectedQuestionnaire(subject)
	if question == nil || len(question.Fields) != 1 || question.Fields[0].Key != trackers.LanguageQuestionKey(subject, "compatibility_mix_compat-0") || !question.Fields[0].Required {
		t.Fatalf("ambiguous mix question: %+v", question)
	}
	hhdAnswer(&subject, "compatibility_mix_compat-0", "audio-0")
	if hhdProjectedQuestionnaire(subject).Fields[0].Value != "audio-0" {
		t.Fatal("mix answer not retained")
	}
	subject.Identity.Generation++
	if hhdProjectedQuestionnaire(subject).Fields[0].Value != "" {
		t.Fatal("changed generation reused mix answer")
	}
	subject.Type, subject.DiscType = "DISC", "BDMV"
	hhdRequireNoFailures(t, languageAssessment(subject))
	if question := hhdProjectedQuestionnaire(subject); question != nil {
		t.Fatalf("full disc gained questionnaire: %+v", question)
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

func TestHHDAcknowledgementCannotWaiveProgrammeRules(t *testing.T) {
	subject := hhdValidationSubject()
	subject.Source = "WEB"
	subject.LanguageFacts = hhdTestLanguageFacts("Japanese", []string{"English", "German"}, nil)
	hhdAnswer(&subject, "english_subtitle_manager", "available")
	failures := languageAssessment(subject)
	for _, rule := range []string{"language_original", "language_extra_dub"} {
		requireHHDValidationFailure(t, failures, rule, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
		for _, failure := range failures {
			if failure.Rule == rule && !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, true) {
				t.Fatalf("acknowledgement waived strict programme rule: %+v", failure)
			}
		}
	}
	requireHHDValidationFailure(t, failures, "language_subtitles", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
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

func TestHHDPersonalRetentionStaysAdvisoryWhenTracksChange(t *testing.T) {
	subject := hhdCompatibilitySubject("TrueHD", "AC-3")
	subject.Type, subject.Source, subject.PersonalRelease = "REMUX", "BluRay", true
	hhdAnswer(&subject, "source_disc_audio", "retained")
	subject.LanguageFacts.Tracks[0].Title = "Updated source mix"
	requireHHDValidationFailure(t, languageAssessment(subject), "language_source_disc_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	if question := hhdProjectedQuestionnaire(subject); question != nil {
		t.Fatalf("personal retention asks for reassurance: %+v", question)
	}
}

func TestHHDChangedSourceReassessesMeasuredCodec(t *testing.T) {
	subject := hhdCompatibilitySubject("AAC", "AAC")
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	hhdRequireNoBlockingFailures(t, languageAssessment(subject))
	subject.Type, subject.Source = "ENCODE", "BluRay"
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
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

func TestHHDDiscAliasesKeepRetentionAdvisoryAndCodecStrict(t *testing.T) {
	for _, source := range []string{"BLU RAY", "BLU-RAY 3D", "BD", "BDMV", "PAL DVD", "NTSC DVD", "HD DVD"} {
		t.Run(source, func(t *testing.T) {
			subject := hhdCompatibilitySubject("AAC", "AAC")
			subject.Type, subject.Source, subject.PersonalRelease = "ENCODE", source, true
			hhdAnswer(&subject, "source_disc_audio", "no_disc_source")
			requireHHDValidationFailure(t, languageAssessment(subject), "language_source_disc_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
			requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			if hhdProjectedQuestionnaire(subject) != nil {
				t.Fatal("disc source gained reassurance question")
			}
			subject.Type = "DISC"
			hhdRequireNoFailures(t, languageAssessment(subject))
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
				requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_source", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
				subject.Source = "WEB"
				hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
				hhdRequireNoBlockingFailures(t, languageAssessment(subject))
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
	for _, rule := range []string{"language_compatibility_missing"} {
		requireHHDValidationFailure(t, languageAssessment(subject), rule, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Source = "WEB"
	hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
	requireHHDValidationFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestHHDKnownWebSourceFormsKeepHLSAdvisory(t *testing.T) {
	for _, source := range []string{"WEB-DL", "WEBDL", "WEBRip", "WEB-RIP"} {
		t.Run(source, func(t *testing.T) {
			subject := hhdCompatibilitySubject("AAC", "AAC")
			subject.Type, subject.Source, subject.PersonalRelease = "ENCODE", source, true
			hhdAnswer(&subject, "compatibility_source_compat-0", "untouched_hls")
			hhdRequireNoBlockingFailures(t, languageAssessment(subject))
			question := hhdProjectedQuestionnaire(subject)
			if question != nil {
				t.Fatalf("known WEB source gained HLS reassurance: %#v", question)
			}
		})
	}
}

func hhdRequireNoBlockingFailures(t *testing.T, failures []api.RuleFailure) {
	t.Helper()
	for _, failure := range failures {
		if failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("unexpected blocking HHD finding: %+v", failure)
		}
	}
}
