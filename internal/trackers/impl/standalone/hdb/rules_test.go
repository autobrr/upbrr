// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func hdbLanguageSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:    "HDB",
		Type:       "REMUX",
		SourcePath: "hdb-language",
		Identity:   api.ExternalIdentity{Generation: 1},
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			PrimaryAudioTrackID:   "main",
			Tracks: []api.MediaTrackFacts{{
				ID:           "main",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"English"},
				Channels:     2,
				Default:      true,
				DefaultKnown: true,
			}},
		}),
	}
}

func hdbLanguageAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = make(map[string]string)
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func TestHDBSourceMixPairsAreNotInferredFromChannels(t *testing.T) {
	for _, channels := range []int{0, 2, 6} {
		subject := hdbLanguageSubject()
		subject.LanguageFacts.Tracks[0].Channels = channels
		subject.LanguageFacts.Tracks[0].Default = false
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
			ID:        "mix",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleAlternateMix,
			Languages: []string{"English"},
			Channels:  6,
		})
		hdbLanguageAnswer(&subject, "source_audio", "source_mix_pair_retained")
		for _, failure := range languageAssessment(subject) {
			if failure.Rule == "language_source_mix_pair" || failure.Rule == "language_original_mix_default" {
				t.Fatalf("channel counts invented source-mix provenance: %+v", failure)
			}
		}
	}
}

func TestHDBForcedSubtitleDefaultUsesInspectedTracks(t *testing.T) {
	for _, test := range []struct {
		name                             string
		forced, defaultSub, defaultKnown bool
		disposition                      api.RuleDisposition
		status                           api.MetadataEvidenceStatus
	}{
		{"full subtitles do not prove forced need", false, true, true, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial},
		{"forced track not default", true, false, true, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete},
		{"forced track unknown default", true, false, false, api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial},
		{"forced track default", true, true, true, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hdbLanguageSubject()
			subject.Type = "WEBDL"
			subject.LanguageFacts.SubtitleLanguages = []string{"English"}
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
				Kind:         api.MediaTrackSubtitle,
				Languages:    []string{"English"},
				Forced:       test.forced,
				Default:      test.defaultSub,
				DefaultKnown: test.defaultKnown,
			})
			hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
			failures := languageAssessment(subject)
			if test.disposition == "" {
				if len(failures) != 0 {
					t.Fatalf("known forced/default track rejected: %+v", failures)
				}
				return
			}
			requireHDBValidationFailure(t, failures, "language_foreign_dialogue_subtitles", test.disposition, test.status)
		})
	}
}

func TestHDBLanguageReviewCannotWaiveIndependentStrictRules(t *testing.T) {
	subject := hdbLanguageSubject()
	subject.Type = "WEBDL"
	subject.LanguageFacts.OriginalLanguages = []string{"Japanese"}
	subject.EffectiveMetadata.Genres = []string{"Drama"}
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleDescription,
		Languages: []string{"English"},
	})
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
	failures := languageAssessment(subject)
	for _, rule := range []string{"language_english_dub", "language_track_role"} {
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == rule && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
		}) {
			t.Fatalf("review waived %s: %#v", rule, failures)
		}
	}
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc acquired language gates: %#v", failures)
	}
}

func TestHDBEnglishDubUsesFinalizedManualGenres(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		genres     []string
		provenance api.FactProvenance
		want       api.MetadataEvidenceStatus
	}{
		{"manual animation", []string{"Animation"}, api.FactProvenanceManual, ""},
		{"manual drama", []string{"Drama"}, api.FactProvenanceManual, api.MetadataEvidenceStatusComplete},
		{"manual clear", nil, api.FactProvenanceManualEmpty, api.MetadataEvidenceStatusPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hdbLanguageSubject()
			subject.Type = "WEBDL"
			subject.LanguageFacts.OriginalLanguages = []string{"Japanese"}
			subject.ProviderMetadata = api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "English", Genres: "Animation"}}
			subject.EffectiveMetadata = api.EffectiveMetadata{
				OriginalLanguage:           "Japanese",
				OriginalLanguageProvenance: api.FactProvenanceManual,
				Genres:                     test.genres,
				GenresProvenance:           test.provenance,
			}
			hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
			failures := languageAssessment(subject)
			if test.want == "" {
				if slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Disposition != api.RuleDispositionAdvisory }) {
					t.Fatalf("manual animation rejected: %#v", failures)
				}
				return
			}
			requireHDBValidationFailure(t, failures, "language_english_dub", api.RuleDispositionStrict, test.want)
		})
	}
}

func TestHDBLanguageReviewHasNoQuestionnaire(t *testing.T) {
	subject := hdbLanguageSubject()
	for _, releaseType := range []string{"REMUX", "WEBDL", "DISC"} {
		meta := api.UploadSubject{
			Type:          releaseType,
			DiscType:      "BDMV",
			LanguageFacts: subject.LanguageFacts,
		}
		if question := New().ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta}); question != nil {
			t.Fatalf("language guidance acquired questions: %+v", question)
		}
		if releaseType == "DISC" && len(languageAssessment(api.NewTrackerValidationSubject(meta, "HDB"))) != 0 {
			t.Fatal("full disc acquired language findings")
		}
	}
}

func TestHDBSubtitleEvidenceAndClearPreservation(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:      "English",
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "main",
		SubtitleLanguages:     []string{"English (Forced)"},
		Tracks: []api.MediaTrackFacts{
			{
				ID:           "main",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"English"},
				Default:      true,
				DefaultKnown: true,
			},
			{
				Kind:         api.MediaTrackSubtitle,
				Languages:    []string{"English (Forced)"},
				Default:      true,
				DefaultKnown: true,
			},
		},
	}
	subject := hdbLanguageSubject()
	subject.Type = "WEBDL"
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "included")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("explicit forced correction lost: %#v", failures)
	}
	media.SubtitleLanguages, media.SubtitleLanguagesProvenance = nil, api.FactProvenanceManualEmpty
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if found, _, _ := forcedDefaultSubtitleEvidence(subject.LanguageFacts); found {
		t.Fatal("manual subtitle clear resurrected stale track language")
	}
	requireHDBValidationFailure(t, languageAssessment(subject), "language_foreign_dialogue_subtitles", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
	requireHDBValidationFailure(t, languageAssessment(subject), "language_foreign_dialogue_subtitles", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.AudioAbsent = true
	if needsForeignDialogueReview(subject.LanguageFacts) {
		t.Fatal("absent audio acquired foreign-dialogue guidance")
	}
}

func TestHDBSourceAndHypotheticalSubtitleWarningsNeedNoAnswers(t *testing.T) {
	for _, answer := range []string{"", "best_original_retained", "source_mix_pair_retained", "incomplete"} {
		subject := hdbLanguageSubject()
		hdbLanguageAnswer(&subject, "source_audio", answer)
		hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "missing")
		failures := languageAssessment(subject)
		for _, rule := range []string{"language_source_audio", "language_foreign_dialogue_subtitles"} {
			requireHDBValidationFailure(t, failures, rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		}
		if slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, false)
		}) {
			t.Fatalf("source history answer %q created a gate: %+v", answer, failures)
		}
	}
}
