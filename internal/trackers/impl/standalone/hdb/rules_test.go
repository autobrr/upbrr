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
		Tracker: "HDB",
 Type: "REMUX",
 SourcePath: "hdb-language",
 Identity: api.ExternalIdentity{Generation: 1},
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage: "English",
 TrackCoverageComplete: true,
 PrimaryAudioTrackID: "main",
			Tracks: []api.MediaTrackFacts{{
ID: "main",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
 Languages: []string{"English"},
 Channels: 2,
 Default: true,
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

func TestHDBSourceAudioRequiresApplicableEvidence(t *testing.T) {
	for _, test := range []struct {
		answer string
		want   api.MetadataEvidenceStatus
	}{
		{"", api.MetadataEvidenceStatusPartial},
		{"unresolved", api.MetadataEvidenceStatusPartial},
		{"incomplete", api.MetadataEvidenceStatusComplete},
	} {
		subject := hdbLanguageSubject()
		hdbLanguageAnswer(&subject, "source_audio", test.answer)
		hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
		requireHDBValidationFailure(t, languageAssessment(subject), "language_source_audio", api.RuleDispositionStrict, test.want)
	}
	subject := hdbLanguageSubject()
	hdbLanguageAnswer(&subject, "source_audio", "best_original_retained")
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("reviewed best original blocked: %#v", failures)
	}
	subject.Type = "WEBDL"
	subject.QuestionnaireAnswers = nil
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("WEB upload acquired source-mix gate: %#v", failures)
	}
}

func TestHDBReviewedMixPairChecksRetainedTracksAndDefault(t *testing.T) {
	for _, test := range []struct {
		name                      string
		surround, defaultOriginal bool
		wantRule                  string
	}{
		{"retained original default", true, true, ""},
		{"missing source upmix", false, true, "language_source_mix_pair"},
		{"wrong default", true, false, "language_original_mix_default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hdbLanguageSubject()
			subject.LanguageFacts.Tracks[0].Default = test.defaultOriginal
			if test.surround {
				subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
ID: "mix",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleAlternateMix,
 Languages: []string{"English"},
 Channels: 6,
})
			}
			hdbLanguageAnswer(&subject, "source_audio", "source_mix_pair_retained")
			hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
			failures := languageAssessment(subject)
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("valid mix pair blocked: %#v", failures)
				}
				return
			}
			requireHDBValidationFailure(t, failures, test.wantRule, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
		})
	}
	// Unknown channels cannot prove an omitted source mix.
	subject := hdbLanguageSubject()
	subject.LanguageFacts.Tracks[0].Channels = 0
	hdbLanguageAnswer(&subject, "source_audio", "source_mix_pair_retained")
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
	requireHDBValidationFailure(t, languageAssessment(subject), "language_source_mix_pair", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestHDBForeignDialogueRequiresReviewedForcedDefaultSubtitles(t *testing.T) {
	for _, test := range []struct {
		name, answer       string
		forced, defaultSub bool
		want               api.MetadataEvidenceStatus
	}{
		{"need is unknown", "", false, false, api.MetadataEvidenceStatusPartial},
		{"required subtitles missing", "missing", false, false, api.MetadataEvidenceStatusComplete},
		{"required track not forced", "included", false, true, api.MetadataEvidenceStatusComplete},
		{"required track not default", "included", true, false, api.MetadataEvidenceStatusComplete},
		{"required forced default present", "included", true, true, ""},
		{"no required foreign dialogue", "not_required", false, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := hdbLanguageSubject()
			subject.Type = "WEBDL"
			subject.LanguageFacts.SubtitleLanguages = []string{"English"}
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
Kind: api.MediaTrackSubtitle,
 Languages: []string{"English"},
 Forced: test.forced,
 Default: test.defaultSub,
})
			hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", test.answer)
			failures := languageAssessment(subject)
			if test.want == "" {
				if len(failures) != 0 {
					t.Fatalf("subtitle exception/compliance blocked: %#v", failures)
				}
				return
			}
			requireHDBValidationFailure(t, failures, "language_foreign_dialogue_subtitles", api.RuleDispositionStrict, test.want)
		})
	}
}

func TestHDBLanguageReviewCannotWaiveIndependentStrictRules(t *testing.T) {
	subject := hdbLanguageSubject()
	subject.Type = "WEBDL"
	subject.LanguageFacts.OriginalLanguages = []string{"Japanese"}
	subject.EffectiveMetadata.Genres = []string{"Drama"}
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
Kind: api.MediaTrackAudio,
 Role: api.AudioRoleDescription,
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
				OriginalLanguage: "Japanese",
 OriginalLanguageProvenance: api.FactProvenanceManual,
				Genres: test.genres,
 GenresProvenance: test.provenance,
			}
			hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "not_required")
			failures := languageAssessment(subject)
			if test.want == "" {
				if len(failures) != 0 {
					t.Fatalf("manual animation rejected: %#v", failures)
				}
				return
			}
			requireHDBValidationFailure(t, failures, "language_english_dub", api.RuleDispositionStrict, test.want)
		})
	}
}

func TestHDBReviewInvalidationAndDiscBoundaries(t *testing.T) {
	subject := hdbLanguageSubject()
	meta := api.UploadSubject{
SourcePath: subject.SourcePath,
 Type: "REMUX",
 DiscType: "BDMV",
 Identity: subject.Identity,
 LanguageFacts: subject.LanguageFacts,
}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if question == nil || len(question.Fields) != 2 {
		t.Fatalf("disc-sourced remux question scope: %#v", question)
	}
	answers := map[string]string{question.Fields[0].Key: "best_original_retained", question.Fields[1].Key: "not_required"}
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"HDB": answers}
	for _, change := range []func(*api.UploadSubject){
		func(m *api.UploadSubject) { m.Identity.Generation++ },
		func(m *api.UploadSubject) {
			m.LanguageFacts = m.LanguageFacts.Clone()
			m.LanguageFacts.Tracks[0].Default = false
		},
		func(m *api.UploadSubject) {
			m.LanguageFacts.ProgrammeLanguages = nil
			m.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
		},
	} {
		changed := meta
		change(&changed)
		question := languageQuestionnaire(trackers.PreparationInput{Meta: changed})
		if question == nil || slices.ContainsFunc(question.Fields, func(field api.TrackerQuestionnaireField) bool { return field.Value != "" }) {
			t.Fatalf("changed evidence reused answers: %#v", question)
		}
		if failures := languageAssessment(api.NewTrackerValidationSubject(changed, "HDB")); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Disposition == api.RuleDispositionStrict && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
		}) {
			t.Fatalf("changed facts accepted: %#v", failures)
		}
	}
	for _, disc := range []string{"", "DVD", "BDMV"} {
		meta.Type, meta.DiscType = "DISC", disc
		if question := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); question != nil {
			t.Fatalf("full disc acquired questions: %#v", question)
		}
		if failures := languageAssessment(api.NewTrackerValidationSubject(meta, "HDB")); len(failures) != 0 {
			t.Fatalf("full disc acquired rules: %#v", failures)
		}
	}
}

func TestHDBSubtitleEvidenceAndClearPreservation(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage: "English",
 TrackCoverageComplete: true,
 PrimaryAudioTrackID: "main",
 SubtitleLanguages: []string{"English (Forced)"},
		Tracks: []api.MediaTrackFacts{
			{
ID: "main",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
 Languages: []string{"English"},
 Default: true,
},
			{
Kind: api.MediaTrackSubtitle,
 Languages: []string{"English (Forced)"},
 Default: true,
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
	if hasForcedDefaultSubtitle(subject.LanguageFacts) {
		t.Fatal("manual subtitle clear resurrected stale track language")
	}
	requireHDBValidationFailure(t, languageAssessment(subject), "language_foreign_dialogue_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "included")
	requireHDBValidationFailure(t, languageAssessment(subject), "language_foreign_dialogue_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
	hdbLanguageAnswer(&subject, "foreign_dialogue_subtitles", "included")
	requireHDBValidationFailure(t, languageAssessment(subject), "language_foreign_dialogue_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.AudioAbsent = true
	if needsForeignDialogueReview(subject.LanguageFacts) {
		t.Fatal("absent audio acquired a foreign-speech question")
	}
}
