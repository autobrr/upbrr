// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPresentationGuidanceDoesNotRequirePersonalChecklist(t *testing.T) {
	for _, personal := range []bool{false, true} {
		s := lstValidationSubject()
		s.PersonalRelease = personal
		s.Type = "REMUX"
		for range 2 {
			failures := presentationReviewFailures(s)
			if len(failures) != 2 {
				t.Fatalf("expected presentation and source guidance: %+v", failures)
			}
			for _, f := range failures {
				if f.Disposition != api.RuleDispositionAdvisory || f.QuestionnaireInput || f.EvidenceStatus != api.MetadataEvidenceStatusPartial {
					t.Fatalf("guidance not passive: %+v", f)
				}
			}
			s.Identity.Generation++
		}
	}
}

func TestPersonalPresentationCannotOverrideMissingLanguages(t *testing.T) {
	for _, kind := range []api.MediaTrackKind{api.MediaTrackAudio, api.MediaTrackSubtitle} {
		s := lstValidationSubject()
		s.PersonalRelease = true
		for i := range s.LanguageFacts.Tracks {
			if s.LanguageFacts.Tracks[i].Kind == kind {
				s.LanguageFacts.Tracks[i].Languages = nil
			}
		}
		s.QuestionnaireAnswers[assetQuestionKey(s, "track_notes")] = "Reviewed English track"
		failures := presentationReviewFailures(s)
		requireLSTValidationFailure(t, failures, "language_presentation_tags", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	}
}

func TestLSTAudioTitlesAreGuidance(t *testing.T) {
	for _, personal := range []bool{false, true} {
		for _, title := range []string{"", "   ", "Programme audio"} {
			t.Run(fmt.Sprintf("personal=%t/title=%q", personal, title), func(t *testing.T) {
				s := lstValidationSubject()
				s.PersonalRelease = personal
				s.LanguageFacts.Tracks[0].Title = title
				found := false
				for _, f := range presentationReviewFailures(s) {
					if f.Rule == "language_audio_titles" {
						found = true
						if f.Disposition != api.RuleDispositionAdvisory || f.QuestionnaireInput || !strings.Contains(f.Reason, "audio-0") {
							t.Fatalf("bad title guidance: %+v", f)
						}
					}
				}
				if found != (strings.TrimSpace(title) == "") {
					t.Fatalf("title guidance present=%t", found)
				}
				s.Type = "DISC"
				if failures := presentationReviewFailures(s); len(failures) != 0 {
					t.Fatalf("full-disc track titles reviewed: %+v", failures)
				}
			})
		}
	}
}

func TestLSTWEBSourceNamingUsesResolvedService(t *testing.T) {
	for _, releaseType := range []string{"WEBDL", "WEB-DL", "ENCODE"} {
		for _, service := range []string{"ATVP", " "} {
			for _, personal := range []bool{false, true} {
				meta := api.UploadSubject{
					Type:            releaseType,
					Service:         service,
					PersonalRelease: personal,
				}
				schema := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
				if schema != nil {
					for _, field := range schema.Fields {
						if strings.Contains(field.Key, "web_source_naming") {
							t.Fatalf("WEB source reassurance remains: %+v", field)
						}
					}
				}
				found := false
				for _, f := range presentationReviewFailures(api.NewTrackerValidationSubject(meta, "LST")) {
					if f.Rule == "language_web_source_naming" {
						found = true
						if f.Disposition != api.RuleDispositionAdvisory || f.QuestionnaireInput {
							t.Fatalf("WEB guidance blocked: %+v", f)
						}
						if service == "ATVP" && !strings.Contains(f.Reason, service) {
							t.Fatal("resolved service lost")
						}
					}
				}
				if found != (releaseType != "ENCODE") {
					t.Fatalf("WEB guidance present=%t", found)
				}
			}
		}
	}
}

func withoutLSTTrackPresentationGuidance(t *testing.T, failures []api.RuleFailure) []api.RuleFailure {
	t.Helper()
	for i, failure := range failures {
		if failure.Rule != "language_track_presentation" {
			continue
		}
		if failure.Disposition != api.RuleDispositionAdvisory || failure.QuestionnaireInput || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("track presentation guidance requires an answer: %+v", failure)
		}
		return append(failures[:i], failures[i+1:]...)
	}
	t.Fatalf("missing ordinary-release track presentation guidance: %+v", failures)
	return nil
}
