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
				if strings.Contains(failure.Reason, test.label) && failure.Disposition == api.RuleDispositionWaivable {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", test.label, languageAssessment(subject))
			}
		})
	}
}

func TestLSTAlternateAndSecondaryReview(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleAlternateMix, api.AudioRoleDescription} {
		t.Run(string(role), func(t *testing.T) {
			subject := lstValidationSubject()
			track := subject.LanguageFacts.Tracks[0]
			track.ID = "secondary"
			track.Role = role
			track.Default = false
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
			questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{LanguageFacts: subject.LanguageFacts}})
			if questionnaire == nil {
				t.Fatal("source review is unreachable")
			}
			found := false
			for _, failure := range languageAssessment(subject) {
				if failure.EvidenceStatus == api.MetadataEvidenceStatusPartial && failure.Disposition == api.RuleDispositionStrict {
					found = true
				}
			}
			if !found {
				t.Fatalf("title label accepted without source evidence: %+v", languageAssessment(subject))
			}
		})
	}
}

func TestLSTExternalSubtitlesRemainPartial(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:1]
	subject.QuestionnaireAnswers = map[string]string{subtitlePresentationQuestionKey(subject): "external"}
	found := false
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_external_subtitles" && failure.Disposition == api.RuleDispositionWaivable {
			found = true
		}
	}
	if !found {
		t.Fatalf("external subtitles treated as embedded: %+v", languageAssessment(subject))
	}
}

func TestLSTAlternateMixUniquenessAndSecondaryConditionalReview(t *testing.T) {
	subject := lstValidationSubject()
	track := subject.LanguageFacts.Tracks[0]
	track.ID = "mix"
	track.Role = api.AudioRoleAlternateMix
	track.Default = false
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "alternate_mix_mix"): "unique"}
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("unique original mix: %+v", got)
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "alternate_mix_mix")] = "duplicate"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_redundant_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "trumpable_audio_eligibility")] = "yes"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_redundant_mix", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.Identity.Generation++
	requireLSTSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].Role = api.AudioRoleDescription
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "trumpable_audio_eligibility")] = "yes"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_track_justification", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "trumpable_audio_eligibility")] = "no"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_track_justification", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestLSTCompatibilitySourceQualityAndMixReview(t *testing.T) {
	for _, test := range []struct {
		name, codec, source string
		wantRule            string
		disposition         api.RuleDisposition
		status              api.MetadataEvidenceStatus
	}{
		{"disc core", "AC-3", "dd_core", "", "", ""},
		{"web DD", "DD", "web_not_inferior", "", "", ""},
		{"web DD plus", "DD+", "web_not_inferior", "", "", ""},
		{"web Atmos", "DD+ Atmos", "web_not_inferior", "", "", ""},
		{"inferior web", "E-AC-3", "web_inferior", "language_compatibility_quality", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete},
		{"unknown provenance", "DD+", "", "language_compatibility_source", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial},
		{"DD plus is not core", "DD+", "dd_core", "language_compatibility_source", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial},
		{"unsupported codec", "AAC", "web_not_inferior", "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
				ID:        "compat",
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleCompatibility,
				Codec:     test.codec,
				Languages: []string{"English"},
			})
			subject.QuestionnaireAnswers = map[string]string{
				trackers.LanguageQuestionKey(subject, "compatibility_source_compat"): test.source,
				trackers.LanguageQuestionKey(subject, "compatibility_mix_compat"):    "audio-0",
			}
			failures := languageAssessment(subject)
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("permitted compatibility audio: %+v", failures)
				}
			} else {
				requireLSTSourceFailure(t, failures, test.wantRule, test.disposition, test.status)
			}
			subject.Source = "BluRay"
			requireLSTSourceFailure(t, languageAssessment(subject), "language_compatibility_source", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			subject.Type = "DISC"
			if got := languageAssessment(subject); len(got) != 0 {
				t.Fatalf("disc acquired source gates: %+v", got)
			}
			subject.Type = "REMUX"
			requireLSTSourceFailure(t, languageAssessment(subject), "language_compatibility_source", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
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

func TestLSTExternalSubtitleReviewCannotClearIndependentBlocks(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks[1].ResourceID = "external-subtitle-file"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.QuestionnaireAnswers = map[string]string{subtitlePresentationQuestionKey(subject): "external"}
	requireLSTSourceFailure(t, languageAssessment(subject), "language_external_subtitles", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.PersonalRelease = true
	subject.LanguageFacts.Tracks[1].Default = false
	subject.QuestionnaireAnswers[subtitlePresentationQuestionKey(subject)] = "external"
	failures := languageAssessment(subject)
	requireLSTSourceFailure(t, failures, "language_subtitle_default", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	for _, failure := range failures {
		if failure.Rule == "language_subtitle_default" && !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, true) {
			t.Fatal("external-subtitle waiver cleared personal requirement")
		}
	}
	subject.LanguageFacts.Tracks[1].ResourceID = ""
	if needsSubtitlePresentation(subject) {
		t.Fatal("inspected embedded English subtitles still need external review")
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
	subject.QuestionnaireAnswers = map[string]string{subtitlePresentationQuestionKey(subject): "external"}
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
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	meta := api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}
	questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil {
		t.Fatal("uncovered resource has no presentation review")
	}
	for _, field := range questionnaire.Fields {
		if strings.HasPrefix(field.Key, "english_subtitle_presentation") {
			subject.QuestionnaireAnswers = map[string]string{field.Key: "external"}
		}
	}
	requireLSTSourceFailure(t, languageAssessment(subject), "language_external_subtitles", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subtitle := subject.LanguageFacts.Tracks[2]
	subtitle.ID = "subtitle-2"
	subtitle.ResourceID = "media-2"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, subtitle)
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_subtitle_presentation" || failure.Rule == "language_external_subtitles" {
			t.Fatalf("all resources embedded: %+v", failure)
		}
	}
}

func TestLSTExternalAnswerCannotClearIncompleteSubtitleEvidence(t *testing.T) {
	for _, status := range []api.MetadataEvidenceStatus{api.MetadataEvidenceStatusPartial, api.MetadataEvidenceStatusContradictory} {
		subject := lstValidationSubject()
		subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
		subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:1]
		subject.LanguageFacts.SubtitleStatus = status
		subject.QuestionnaireAnswers = map[string]string{subtitlePresentationQuestionKey(subject): "external"}
		requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	}
}

func TestLSTSubtitlePresentationKeepsResourceSpecificEnglishDub(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese", "English"}, []string{"English"})
	subject.LanguageFacts.Tracks[0].ResourceID = "foreign-media"
	subject.LanguageFacts.Tracks[1].ResourceID = "english-media"
	subject.LanguageFacts.Tracks[2].ResourceID = "english-media"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.Tracks[1].ResourceID = "foreign-media"
	subject.LanguageFacts.Tracks[2].ResourceID = "foreign-media"
	if needsSubtitlePresentation(subject) {
		t.Fatal("same-resource English dub lost its exemption")
	}
}

func TestLSTHardcodedPresentationIsGenerationBoundWithoutInferredPackCoverage(t *testing.T) {
	subject := lstValidationSubject()
	subject.LanguageFacts = lstTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:1]
	key := subtitlePresentationQuestionKey(subject)
	subject.QuestionnaireAnswers = map[string]string{key: "external"}
	subject.HardcodedSubtitleLanguages = []string{"English"}
	if subtitlePresentationQuestionKey(subject) == key {
		t.Fatal("hardcoded evidence did not invalidate presentation answer")
	}
	if needsSubtitlePresentation(subject) {
		t.Fatal("known hardcoded English on one resource needs external review")
	}
	track := subject.LanguageFacts.Tracks[0]
	track.ID = "audio-2"
	track.ResourceID = "media-2"
	track.Default = false
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
	subject.QuestionnaireAnswers[subtitlePresentationQuestionKey(subject)] = "external"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
		Type:                       subject.Type,
		LanguageFacts:              subject.LanguageFacts,
		HardcodedSubtitleLanguages: subject.HardcodedSubtitleLanguages,
	}})
	for _, field := range questionnaire.Fields {
		if strings.HasPrefix(field.Key, "english_subtitle_presentation") && slices.Contains(field.Options, "external") {
			t.Fatal("ambiguous multi-resource hardcoded coverage permits a contradictory external answer")
		}
	}
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired presentation rule: %+v", failures)
	}
	subject.Type = "REMUX"
	requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
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
				requireLSTSourceFailure(t, languageAssessment(subject), "language_subtitle_presentation", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
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
