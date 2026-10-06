// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func btnSourceSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:       "BTN",
		Type:          "REMUX",
		Source:        "BluRay",
		LanguageFacts: btnLanguageFacts("English", "English"),
	}
}
func btnSourceAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = make(map[string]string)
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}
func requireBTNSourceFailure(t *testing.T, subject api.TrackerValidationSubject, rule string, disposition api.RuleDisposition, status api.MetadataEvidenceStatus) {
	t.Helper()
	failures := languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == rule && f.Disposition == disposition && f.EvidenceStatus == status
	}) {
		t.Fatalf("missing %s / %s / %s: %#v", rule, disposition, status, failures)
	}
}

func TestBTNSourceRetentionMandatoryAndSoundtrackPairTrumpable(t *testing.T) {
	subject := btnSourceSubject()
	for _, key := range []string{"source_audio", "source_extras", "broadcast_soundtrack"} {
		requireBTNSourceFailure(t, subject, "language_"+key, api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	}
	btnSourceAnswer(&subject, "source_audio", "incomplete")
	btnSourceAnswer(&subject, "source_extras", "incomplete")
	btnSourceAnswer(&subject, "broadcast_soundtrack", "missing")
	requireBTNSourceFailure(t, subject, "language_source_audio", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	requireBTNSourceFailure(t, subject, "language_source_extras", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	requireBTNSourceFailure(t, subject, "language_broadcast_soundtrack", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	for _, f := range languageAssessment(subject) {
		if (f.Rule == "language_source_audio" || f.Rule == "language_source_extras") && !trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true) {
			t.Fatal("soundtrack waiver cleared independent strict source omission")
		}
	}
	btnSourceAnswer(&subject, "source_audio", "best_original_retained")
	btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
	btnSourceAnswer(&subject, "broadcast_soundtrack", "same_or_both_retained")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("reviewed source blocked: %#v", failures)
	}
	subject.Identity.Generation++
	requireBTNSourceFailure(t, subject, "language_source_audio", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc assessed: %#v", failures)
	}
}

func TestBTNSourceQuestionsHaveBoundedApplicability(t *testing.T) {
	for _, test := range []struct {
		typ, source string
		keys        []string
	}{
		{"WEBDL", "WEB", nil},
		{"HDTV", "HDTV", nil},
		{"ENCODE", "UHDTV", nil},
		{"ENCODE", "BluRay", []string{"source_extras_", "broadcast_soundtrack_"}},
		{"ENCODE", "DVD", []string{"source_extras_"}},
		{"REMUX", "BluRay", []string{"source_audio_", "source_extras_", "broadcast_soundtrack_"}},
		{"DISC", "BluRay", nil},
	} {
		meta := api.UploadSubject{
			Type:          test.typ,
			Source:        test.source,
			LanguageFacts: btnLanguageFacts("English", "English"),
		}
		question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
		if len(test.keys) == 0 {
			if question != nil {
				t.Fatalf("unrelated type acquired source questions: %#v", question)
			}
			continue
		}
		if question == nil || len(question.Fields) != len(test.keys) {
			t.Fatalf("%s/%s questions = %#v", test.typ, test.source, question)
		}
		for _, key := range test.keys {
			if !slices.ContainsFunc(question.Fields, func(f api.TrackerQuestionnaireField) bool { return strings.HasPrefix(f.Key, key) && f.Required }) {
				t.Fatalf("missing %s: %#v", key, question)
			}
		}
	}
}

func TestBTNAnimationDubAndRetailSubtitleGuidance(t *testing.T) {
	subject := btnSourceSubject()
	subject.LanguageFacts = btnLanguageFacts("Japanese", "Japanese")
	subject.EffectiveMetadata.Genres = []string{"Animation"}
	btnSourceAnswer(&subject, "primary_audio_country", "Japan")
	requireBTNSourceFailure(t, subject, "language_retail_english_dub", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	requireBTNSourceFailure(t, subject, "language_retail_english_subtitles", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	btnSourceAnswer(&subject, "retail_english_dub", "available_missing")
	btnSourceAnswer(&subject, "retail_english_subtitles", "available_missing")
	requireBTNSourceFailure(t, subject, "language_retail_english_dub", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
	requireBTNSourceFailure(t, subject, "language_retail_english_subtitles", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
	btnSourceAnswer(&subject, "retail_english_dub", "unavailable")
	btnSourceAnswer(&subject, "retail_english_subtitles", "unavailable")
	if slices.ContainsFunc(languageAssessment(subject), func(f api.RuleFailure) bool {
		return f.Rule == "language_retail_english_dub" || f.Rule == "language_retail_english_subtitles"
	}) {
		t.Fatal("unavailable retail tracks were required")
	}
	subject.Type, subject.Source = "WEBDL", "WEB"
	subject.EffectiveMetadata.Genres = []string{"Drama"}
	meta := api.UploadSubject{
		Type:              subject.Type,
		Source:            subject.Source,
		LanguageFacts:     subject.LanguageFacts,
		EffectiveMetadata: subject.EffectiveMetadata,
	}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if question == nil || len(question.Fields) != 1 {
		t.Fatalf("live-action WEB acquired animation/remux questions: %#v", question)
	}
}

func TestBTNSourceReviewDoesNotWaiveOriginalOrStaffRules(t *testing.T) {
	subject := btnSourceSubject()
	subject.LanguageFacts = btnLanguageFacts("English", "German")
	btnSourceAnswer(&subject, "primary_audio_country", "Germany")
	btnSourceAnswer(&subject, "source_audio", "best_original_retained")
	btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
	btnSourceAnswer(&subject, "broadcast_soundtrack", "missing")
	for _, key := range []string{"language_staff_dub", "language_original", "language_original_primary"} {
		requireBTNSourceFailure(t, subject, key, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	requireBTNSourceFailure(t, subject, "language_broadcast_soundtrack", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	subject.Source = "WEB"
	btnSourceAnswer(&subject, "source_audio", "best_original_retained")
	btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
	for _, key := range []string{"language_staff_dub", "language_original", "language_original_primary"} {
		requireBTNSourceFailure(t, subject, key, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
}

func TestBTNSourceQuestionAnswersInvalidateWithTrackChanges(t *testing.T) {
	subject := btnSourceSubject()
	meta := api.UploadSubject{
		Type:          subject.Type,
		Source:        subject.Source,
		LanguageFacts: subject.LanguageFacts,
		Identity:      api.ExternalIdentity{Generation: 1},
	}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"BTN": {question.Fields[0].Key: "best_original_retained"}}
	question = languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if question.Fields[0].Value != "best_original_retained" {
		t.Fatal("current source answer not retained")
	}
	oldKey := question.Fields[0].Key
	meta.LanguageFacts.Tracks[0].Codec = "FLAC"
	question = languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if question.Fields[0].Key == oldKey || question.Fields[0].Value != "" {
		t.Fatal("changed tracks reused source answer")
	}
}

func TestBTNSourceAliasesKeepRequiredReviews(t *testing.T) {
	for _, source := range []string{"BLU RAY", "BLU-RAY 3D", "BD", "BDMV", "PAL DVD", "NTSC DVD", "HD DVD"} {
		t.Run(source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = "ENCODE", source
			requireBTNSourceFailure(t, subject, "language_source_extras", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
				Type:          subject.Type,
				Source:        source,
				LanguageFacts: subject.LanguageFacts,
			}})
			keys := []string{"source_extras"}
			if slices.Contains([]string{"BLU RAY", "BLU-RAY 3D", "BD", "BDMV"}, source) {
				keys = append(keys, "broadcast_soundtrack")
				requireBTNSourceFailure(t, subject, "language_broadcast_soundtrack", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			}
			if question == nil || len(question.Fields) != len(keys) {
				t.Fatalf("source reviews = %#v", question)
			}
			for _, key := range keys {
				if !slices.ContainsFunc(question.Fields, func(field api.TrackerQuestionnaireField) bool {
					return field.Key == trackers.LanguageQuestionKey(subject, key) && field.Required
				}) {
					t.Fatalf("missing required %s review: %#v", key, question)
				}
			}
		})
	}
}

func TestBTNSourceAnswersInvalidateOnSameGenerationSourceChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
	}{
		{"source", func(subject *api.TrackerValidationSubject) { subject.Source = "BLU RAY" }},
		{"type", func(subject *api.TrackerValidationSubject) { subject.Type = "ENCODE" }},
		{"disc type", func(subject *api.TrackerValidationSubject) { subject.DiscType = "BDMV" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := btnSourceSubject()
			btnSourceAnswer(&subject, "source_audio", "best_original_retained")
			btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
			btnSourceAnswer(&subject, "broadcast_soundtrack", "same_or_both_retained")
			if failures := languageAssessment(subject); len(failures) != 0 {
				t.Fatalf("current answers did not resolve source review: %#v", failures)
			}
			test.mutate(&subject)
			requireBTNSourceFailure(t, subject, "language_source_extras", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			requireBTNSourceFailure(t, subject, "language_broadcast_soundtrack", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
				Type:                        subject.Type,
				Source:                      subject.Source,
				DiscType:                    subject.DiscType,
				LanguageFacts:               subject.LanguageFacts,
				TrackerQuestionnaireAnswers: map[string]map[string]string{"BTN": subject.QuestionnaireAnswers},
			}})
			for _, field := range question.Fields {
				if field.Value != "" {
					t.Fatalf("stale source answer retained: %#v", field)
				}
			}
		})
	}
}

func TestBTNUnknownSourceApplicabilityStaysUnresolved(t *testing.T) {
	for _, source := range []string{"", "Mixed", "Unknown", "unrecognized source", "WEB-archive"} {
		t.Run(source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = "ENCODE", source
			btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
			btnSourceAnswer(&subject, "broadcast_soundtrack", "same_or_both_retained")
			requireBTNSourceFailure(t, subject, "language_source_kind", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			subject.Source = "WEB"
			if failures := languageAssessment(subject); len(failures) != 0 {
				t.Fatalf("known WEB acquired a source gate: %#v", failures)
			}
			subject.Source, subject.Type = source, "DISC"
			if failures := languageAssessment(subject); len(failures) != 0 {
				t.Fatalf("full disc acquired a source gate: %#v", failures)
			}
		})
	}
}

func TestBTNRemuxSourceKindNeedsPositiveEvidence(t *testing.T) {
	for _, discType := range []string{"", "Unknown", "BDMV", "Blu-Ray", "BLU RAY", "DVD", "HDDVD"} {
		t.Run(discType, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Source, subject.DiscType = "", discType
			btnSourceAnswer(&subject, "source_audio", "best_original_retained")
			btnSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
			if discType == "" || discType == "Unknown" {
				requireBTNSourceFailure(t, subject, "language_source_kind", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
				return
			}
			if discType == "BDMV" || discType == "Blu-Ray" || discType == "BLU RAY" {
				requireBTNSourceFailure(t, subject, "language_broadcast_soundtrack", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
				btnSourceAnswer(&subject, "broadcast_soundtrack", "same_or_both_retained")
			}
			if failures := languageAssessment(subject); len(failures) != 0 {
				t.Fatalf("known and reviewed disc kind stayed unresolved: %#v", failures)
			}
		})
	}
}

func TestBTNSupportedNonDiscSourcesNeedNoDiscReview(t *testing.T) {
	for _, source := range []string{"PDTV", "DSR", "TVRip", "VHSRip", "WEB-DL", "WEBDL", "WEBRip"} {
		t.Run(source, func(t *testing.T) {
			subject := btnSourceSubject()
			subject.Type, subject.Source = "ENCODE", source
			if failures := languageAssessment(subject); len(failures) != 0 {
				t.Fatalf("supported non-disc source acquired a language gate: %#v", failures)
			}
			if question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
				Type:          subject.Type,
				Source:        subject.Source,
				LanguageFacts: subject.LanguageFacts,
			}}); question != nil {
				t.Fatalf("supported non-disc source acquired disc questions: %#v", question)
			}
		})
	}
}
