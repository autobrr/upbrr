// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTAudioDefectClassification(t *testing.T) {
	for _, test := range []struct {
		name      string
		programme []string
		label     string
	}{
		{"dub only", []string{"German"}, "Non-Original Non-English Dub"},
		{"extra dub", []string{"Japanese", "English", "German"}, "Redundant Audio Tracks"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.LanguageFacts = lstTestLanguageFacts("Japanese", test.programme, []string{"English"})
			subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "trumpable_audio_eligibility"): "yes"}
			found := false
			for _, failure := range languageAssessment(subject) {
				if strings.Contains(failure.Reason, test.label) && failure.Disposition == api.RuleDispositionAdvisory {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", test.label, languageAssessment(subject))
			}
		})
	}
}

func TestLSTAlternateAndSecondaryGuidance(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleAlternateMix, api.AudioRoleDescription} {
		t.Run(string(role), func(t *testing.T) {
			subject := lstValidationSubject()
			track := subject.LanguageFacts.Tracks[0]
			track.ID, track.Role, track.Default = "secondary", role, false
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
			question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{LanguageFacts: subject.LanguageFacts}})
			if question != nil {
				t.Fatalf("source reassurance question: %+v", question)
			}
			rule := "language_alternate_mix"
			if role == api.AudioRoleDescription {
				rule = "language_track_justification"
			}
			requireLSTSourceFailure(t, languageAssessment(subject), rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		})
	}
}

func TestLSTUnverifiedSubtitlePresentationIsAdvisory(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:1]
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	if question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{LanguageFacts: subject.LanguageFacts}}); question != nil {
		t.Fatalf("subtitle reassurance question: %+v", question)
	}
}

func TestLSTSourceAnswersCannotInventUniquenessOrReplacementEligibility(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleAlternateMix, api.AudioRoleDescription} {
		subject := lstValidationSubject()
		track := subject.LanguageFacts.Tracks[0]
		track.ID, track.Role, track.Default = "mix", role, false
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
		rule := "language_alternate_mix"
		if role == api.AudioRoleDescription {
			rule = "language_track_justification"
		}
		for _, answer := range []string{"", "unique", "duplicate"} {
			for _, eligible := range []string{"", "yes", "no"} {
				subject.QuestionnaireAnswers = map[string]string{
					trackers.LanguageQuestionKey(subject, "alternate_mix_mix"):           answer,
					trackers.LanguageQuestionKey(subject, "trumpable_audio_eligibility"): eligible,
				}
				requireLSTSourceFailure(t, languageAssessment(subject), rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
				for _, failure := range languageAssessment(subject) {
					if failure.Disposition != api.RuleDispositionAdvisory {
						t.Fatalf("source answer created a gate: %+v", failure)
					}
				}
			}
		}
	}
}

func TestLSTCompatibilitySourceQualityIsAdvisory(t *testing.T) {
	for _, codec := range []string{"AC-3", "DD", "DD+", "DD+ Atmos", "E-AC-3", "AAC"} {
		t.Run(codec, func(t *testing.T) {
			for _, answer := range []string{"", "dd_core", "web_not_inferior", "web_inferior"} {
				subject := lstValidationSubject()
				subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
				subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
ID: "compat",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleCompatibility,
 Codec: codec,
 Languages: []string{"English"},
})
				subject.QuestionnaireAnswers = map[string]string{
					trackers.LanguageQuestionKey(subject, "compatibility_source_compat"): answer,
					trackers.LanguageQuestionKey(subject, "compatibility_mix_compat"):    "audio-0",
				}
				failures := languageAssessment(subject)
				requireLSTSourceFailure(t, failures, "language_compatibility_source", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
				if codec == "AAC" {
					requireLSTSourceFailure(t, failures, "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
				} else {
					for _, failure := range failures {
						if failure.Disposition != api.RuleDispositionAdvisory {
							t.Fatalf("source quality became a gate: %+v", failure)
						}
					}
				}
				question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}})
				if question == nil || len(question.Fields) != 1 || !strings.HasPrefix(question.Fields[0].Key, "compatibility_mix_") {
					t.Fatalf("only measured association input should remain: %+v", question)
				}
				subject.Type = "DISC"
				if got := languageAssessment(subject); len(got) != 0 {
					t.Fatalf("full disc gained guidance: %+v", got)
				}
			}
		})
	}
}

func TestLSTCompatibilityEmbeddedAndWrongMix(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks[0].EmbeddedCompatibility = true
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("embedded core denied: %+v", got)
	}
	subject.LanguageFacts.Tracks[0].EmbeddedCompatibility = false
	requireLSTSourceFailure(t, languageAssessment(subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	track := subject.LanguageFacts.Tracks[0]
	track.ID = "other"
	track.ResourceID = "different-file"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track, api.MediaTrackFacts{
		ID:        "compat",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Codec:     "AC-3",
		Languages: []string{"English"},
	})
	subject.QuestionnaireAnswers = map[string]string{
		trackers.LanguageQuestionKey(subject, "compatibility_source_compat"): "dd_core",
		trackers.LanguageQuestionKey(subject, "compatibility_mix_compat"):    "other",
	}
	requireLSTSourceFailure(t, languageAssessment(subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestLSTSubtitleGuidanceCannotClearIndependentBlocks(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks[1].ResourceID = "external-subtitle-file"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	subject.PersonalRelease = true
	subject.LanguageFacts.Tracks[1].Default = false
	failures := languageAssessment(subject)
	requireLSTSourceFailure(t, failures, "language_subtitle_default", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	for _, failure := range failures {
		if failure.Rule == "language_subtitle_default" && !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, true) {
			t.Fatal("subtitle guidance cleared personal requirement")
		}
	}
	subject.LanguageFacts.Tracks[1].ResourceID = ""
	if needsSubtitlePresentation(subject) {
		t.Fatal("inspected embedded English subtitles still need guidance")
	}
}

func requireLSTSourceFailure(t *testing.T, failures []api.RuleFailure, rule string, disposition api.RuleDisposition, status api.MetadataEvidenceStatus) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule && failure.Disposition == disposition && failure.EvidenceStatus == status {
			return
		}
	}
	t.Fatalf("missing %s/%s/%s: %+v", rule, disposition, status, failures)
}

func TestLSTKnownHardcodedEnglishIsNotExternal(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:           "Japanese",
		HardcodedSubtitleLanguages: []string{"English"},
		TrackCoverageComplete:      true,
		PrimaryAudioTrackID:        "audio-0",
		Tracks: []api.MediaTrackFacts{{
			ID:         "audio-0",
			ResourceID: "media-1",
			Kind:       api.MediaTrackAudio,
			Role:       api.AudioRoleProgramme,
			Languages:  []string{"Japanese"},
			Codec:      "AC-3",
			Default:    true,
		}},
	}
	meta := api.UploadSubject{
		LanguageFacts:              mediafacts.ResolveLanguages(media),
		HardcodedSubs:              true,
		HardcodedSubtitleLanguages: []string{"English"},
		Type:                       "WEBDL",
	}
	subject := api.NewTrackerValidationSubject(meta, "LST")
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_subtitle_presentation" || failure.Rule == "language_external_subtitles" {
			t.Fatalf("known burned-in English misclassified: %+v", failure)
		}
	}
	if questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); questionnaire != nil {
		t.Fatalf("known burned-in English requires a false source answer: %+v", questionnaire)
	}
	subject.PersonalRelease = true
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_default", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestLSTSubtitlePresentationCoversEveryProgrammeResource(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese", "Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks[0].ResourceID = "media-1"
	subject.LanguageFacts.Tracks[1].ResourceID = "media-2"
	subject.LanguageFacts.Tracks[2].ResourceID = "media-1"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	meta := api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}
	if question := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); question != nil {
		t.Fatalf("unverified presentation became a question: %+v", question)
	}
	subtitle := subject.LanguageFacts.Tracks[2]
	subtitle.ID, subtitle.ResourceID = "subtitle-2", "media-2"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, subtitle)
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_subtitle_presentation" {
			t.Fatalf("all resources embedded: %+v", failure)
		}
	}
}

func TestLSTIncompleteSubtitlePresentationStaysUnverified(t *testing.T) {
	for _, status := range []api.MetadataEvidenceStatus{api.MetadataEvidenceStatusPartial, api.MetadataEvidenceStatusContradictory} {
		subject := lstValidationSubject()
		subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
		subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:1]
		subject.LanguageFacts.SubtitleStatus = status
		requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	}
}

func TestLSTSubtitlePresentationKeepsResourceSpecificEnglishDub(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese", "English"}, []string{"English"})
	subject.LanguageFacts.Tracks[0].ResourceID = "foreign-media"
	subject.LanguageFacts.Tracks[1].ResourceID = "english-media"
	subject.LanguageFacts.Tracks[2].ResourceID = "english-media"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.Tracks[1].ResourceID = "foreign-media"
	subject.LanguageFacts.Tracks[2].ResourceID = "foreign-media"
	if needsSubtitlePresentation(subject) {
		t.Fatal("same-resource English dub lost its exemption")
	}
}

func TestLSTHardcodedPresentationDoesNotInferPackCoverage(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:1]
	subject.HardcodedSubtitleLanguages = []string{"English"}
	if needsSubtitlePresentation(subject) {
		t.Fatal("known hardcoded English on one resource needs review")
	}
	track := subject.LanguageFacts.Tracks[0]
	track.ID, track.ResourceID, track.Default = "audio-2", "media-2", false
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
Type: subject.Type,
 LanguageFacts: subject.LanguageFacts,
 HardcodedSubtitleLanguages: subject.HardcodedSubtitleLanguages,
}})
	if question != nil {
		t.Fatalf("unverified pack coverage became reassurance: %+v", question)
	}
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired presentation rule: %+v", failures)
	}
	subject.Type = "REMUX"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
}

func TestLSTEnglishDubExemptionPrecedesUnrelatedSubtitleEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		programme  []string
		resources  []string
		wantReview bool
	}{
		{"single file with English dub", []string{"Japanese", "English"}, []string{"media-1", "media-1"}, false},
		{"single file without English dub", []string{"Japanese"}, []string{"media-1"}, true},
		{"every resource has English", []string{"English", "English"}, []string{"media-1", "media-2"}, false},
		{"English in another resource is insufficient", []string{"Japanese", "English"}, []string{"media-1", "media-2"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := lstTestLanguageFacts("Japanese", test.programme, []string{"English"})
			for i, resource := range test.resources {
				facts.Tracks[i].ResourceID = resource
			}
			media := api.MediaFacts{
				OriginalLanguage:      "Japanese",
				SubtitleLanguages:     []string{"English"},
				TrackCoverageComplete: true,
				PrimaryAudioTrackID:   facts.PrimaryAudioTrackID,
				Tracks: append(facts.Tracks, api.MediaTrackFacts{
					ID:         "unidentified-subtitle",
					Kind:       api.MediaTrackSubtitle,
					ResourceID: "media-1",
				}),
			}
			subject := lstValidationSubject()
			subject.LanguageFacts = mediafacts.ResolveLanguages(media)
			if subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || subject.LanguageFacts.SubtitleStatus != api.MetadataEvidenceStatusPartial {
				t.Fatalf("invalid regression fixture: %+v", subject.LanguageFacts)
			}
			if got := needsSubtitlePresentation(subject); got != test.wantReview {
				t.Fatalf("needs presentation review=%t, want %t", got, test.wantReview)
			}
			if test.wantReview {
				requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
			} else {
				for _, failure := range languageAssessment(subject) {
					if failure.Rule == "language_subtitle_presentation" || failure.Rule == "language_external_subtitles" {
						t.Fatalf("English alternative needs unrelated subtitle evidence: %+v", failure)
					}
				}
				questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}})
				if questionnaire != nil {
					for _, field := range questionnaire.Fields {
						if strings.HasPrefix(field.Key, "english_subtitle_presentation") {
							t.Fatalf("English alternative has an unnecessary presentation question: %+v", field)
						}
					}
				}
			}
		})
	}
}

func TestLSTKnownEnglishCoveragePrecedesUnrelatedUnknownTracks(t *testing.T) {
	for _, englishAudio := range []bool{false, true} {
		subject := lstValidationSubject()
		subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
		for i := range subject.LanguageFacts.Tracks {
			subject.LanguageFacts.Tracks[i].ResourceID = "media-1"
		}
		if englishAudio {
			track := subject.LanguageFacts.Tracks[0]
			track.ID, track.Languages, track.Default = "dub", []string{"English"}, false
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
		}
		subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
		subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
ID: "unknown",
 ResourceID: "media-1",
 Kind: api.MediaTrackSubtitle,
})
		if needsSubtitlePresentation(subject) {
			t.Fatalf("known relevant English coverage requires unrelated evidence: %+v", subject.LanguageFacts)
		}
	}
}
