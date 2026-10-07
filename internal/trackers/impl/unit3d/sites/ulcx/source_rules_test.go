// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXOriginalAudioRecommendation(t *testing.T) {
	subject := api.TrackerValidationSubject{Tracker: "ULCX", LanguageFacts: ulcxTestLanguageFacts("Japanese", []string{"English"}, nil)}
	failures := languageAssessment(subject)
	requireULCXSourceFailure(t, failures, "language_original_recommendation", api.RuleDispositionAdvisory)
	for _, failure := range failures {
		if failure.Disposition == api.RuleDispositionStrict {
			t.Fatalf("ordinary English dub prohibited: %+v", failures)
		}
	}
	subject.PersonalRelease = true
	requireULCXSourceFailure(t, languageAssessment(subject), "language_original", api.RuleDispositionStrict)
	subject.Type = "DISC"
	if got := languageAssessment(subject); len(got) != 0 {
		t.Fatalf("full disc: %+v", got)
	}
}

func TestULCXAlternateMixSourceReview(t *testing.T) {
	subject := api.TrackerValidationSubject{
		Tracker:       "ULCX",
		Type:          "REMUX",
		LanguageFacts: ulcxTestLanguageFacts("English", []string{"English", "English"}, nil),
	}
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix
	requireULCXSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionAdvisory)
	question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{LanguageFacts: subject.LanguageFacts, Type: subject.Type}})
	if question == nil {
		t.Fatal("alternate mix source review is unreachable")
	}
	subject.QuestionnaireAnswers = map[string]string{}
	for _, field := range question.Fields {
		if strings.HasPrefix(field.Key, "alternate_mix_") {
			subject.QuestionnaireAnswers[field.Key] = "unique"
		}
	}
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_alternate_mix" {
			t.Fatalf("source-confirmed unique mix: %+v", failure)
		}
	}
}

func requireULCXSourceFailure(t *testing.T, failures []api.RuleFailure, rule string, disposition api.RuleDisposition) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule && failure.Disposition == disposition {
			return
		}
	}
	t.Fatalf("missing %s/%s: %+v", rule, disposition, failures)
}

func TestULCXAlternateMixReviewNeverPermitsExtraDub(t *testing.T) {
	subject := api.TrackerValidationSubject{Tracker: "ULCX", LanguageFacts: ulcxTestLanguageFacts("Japanese", []string{"Japanese", "English", "German"}, nil)}
	subject.LanguageFacts.Tracks[2].Role = api.AudioRoleAlternateMix
	subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "alternate_mix_audio-2"): "unique"}
	requireULCXSourceFailure(t, languageAssessment(subject), "language_extra_dub", api.RuleDispositionStrict)
	subject.PersonalRelease = true
	requireULCXSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionAdvisory)
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "alternate_mix_audio-2")] = "duplicate"
	requireULCXSourceFailure(t, languageAssessment(subject), "language_alternate_mix", api.RuleDispositionAdvisory)
}

func TestULCXNonForeignSubtitleDefaultEvidence(t *testing.T) {
	for _, personal := range []bool{false, true} {
		for _, test := range []struct {
			name                   string
			first, second          api.MediaTrackFacts
			wantKnown, wantUnknown bool
		}{
			{"known non-defaults", api.MediaTrackFacts{DefaultKnown: true}, api.MediaTrackFacts{DefaultKnown: true}, false, false},
			{"unknown false flag", api.MediaTrackFacts{}, api.MediaTrackFacts{DefaultKnown: true}, false, true},
			{"unknown true flag", api.MediaTrackFacts{Default: true}, api.MediaTrackFacts{DefaultKnown: true}, false, true},
			{"known default and unknown", api.MediaTrackFacts{Default: true, DefaultKnown: true}, api.MediaTrackFacts{}, true, true},
			{"known default and non-default", api.MediaTrackFacts{Default: true, DefaultKnown: true}, api.MediaTrackFacts{DefaultKnown: true}, true, false},
		} {
			t.Run(fmt.Sprintf("%s/personal=%t", test.name, personal), func(t *testing.T) {
				subject := api.TrackerValidationSubject{
Tracker: "ULCX",
 PersonalRelease: personal,
 Type: "WEBDL",
 LanguageFacts: ulcxTestLanguageFacts("English", []string{"English"}, nil),
}
				for i, track := range []api.MediaTrackFacts{test.first, test.second} {
					track.ID, track.Kind, track.Languages = fmt.Sprintf("subtitle-%d", i), api.MediaTrackSubtitle, []string{"English"}
					subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
				}
				failures := languageAssessment(subject)
				disposition := api.RuleDispositionAdvisory
				if personal {
					disposition = api.RuleDispositionStrict
				}
				if test.wantKnown {
					requireULCXValidationFailure(t, failures, "language_subtitle_default", disposition, api.MetadataEvidenceStatusComplete)
				}
				if test.wantUnknown {
					requireULCXValidationFailure(t, failures, "language_subtitle_default", disposition, api.MetadataEvidenceStatusPartial)
				}
				wantCount := 0
				if test.wantKnown {
					wantCount++
				}
				if test.wantUnknown {
					wantCount++
				}
				if len(failures) != wantCount {
					t.Fatalf("default evidence produced wrong findings: %+v", failures)
				}
				for _, failure := range failures {
					if !failure.DebugBypass {
						t.Fatalf("lost language-only debug bypass: %+v", failure)
					}
					if personal && failure.EvidenceStatus == api.MetadataEvidenceStatusPartial && !strings.Contains(failure.Reason, "Unresolved") {
						t.Fatalf("unknown default became known prohibited: %+v", failure)
					}
				}
				subject.Type, subject.DiscType = "DISC", "BDMV"
				if got := languageAssessment(subject); len(got) != 0 {
					t.Fatalf("full disc gained default review: %+v", got)
				}
				subject.Type = "REMUX"
				if got := languageAssessment(subject); len(got) != wantCount {
					t.Fatalf("disc-sourced remux escaped default assessment: %+v", got)
				}
			})
		}
	}
}
