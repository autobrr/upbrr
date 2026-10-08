// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAitherAdditionalOriginalAudioIsSourceGuidance(t *testing.T) {
	subject := aitherPassingSubject()
	subject.LanguageFacts = aitherTestLanguageFacts("Japanese", []string{"Japanese", "Japanese"}, []string{"English"})
	assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_additional_main_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	if Profile().Site.ProjectionQuestionnaire == nil {
		t.Fatal("music-video source evidence is unreachable")
	}
}

func TestAitherCompatibilityRequiresSourceAssociation(t *testing.T) {
	subject := aitherPassingSubject()
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "compat",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Codec:     "AC-3",
		Languages: []string{"Japanese"},
	})
	assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_compatibility_mix", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestAitherMusicVideoGuidanceDoesNotWaiveExtraDubs(t *testing.T) {
	subject := aitherPassingSubject()
	subject.LanguageFacts = aitherTestLanguageFacts("Japanese", []string{"Japanese", "Japanese", "English", "German"}, []string{"English"})
	for _, answer := range []string{"", "original_music_video", "not_original_music_video"} {
		subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "music_video_audio_audio-1"): answer}
		assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_additional_main_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_extra_dub", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
	question := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}})
	if question != nil {
		t.Fatalf("music-video source history requires reassurance: %+v", question)
	}
	subject.Type = "DISC"
	if got := evaluateAitherEvidence(t, subject); len(got) != 0 {
		t.Fatalf("full disc: %+v", got)
	}
	subject.Type = "REMUX"
	assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_extra_dub", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestAitherCompatibilityAssociationCannotCoverAnotherMix(t *testing.T) {
	subject := aitherPassingSubject()
	subject.LanguageFacts = aitherTestLanguageFacts("Japanese", []string{"Japanese", "Japanese"}, []string{"English"})
	for i := range 2 {
		subject.LanguageFacts.Tracks[i].Codec = "TrueHD"
	}
	for _, id := range []string{"compat-1", "compat-2"} {
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
			ID:        id,
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleCompatibility,
			Codec:     "DD+",
			Languages: []string{"Japanese"},
		})
	}
	subject.QuestionnaireAnswers = map[string]string{
		trackers.LanguageQuestionKey(subject, "music_video_audio_audio-1"):  "original_music_video",
		trackers.LanguageQuestionKey(subject, "compatibility_mix_compat-1"): "audio-0",
		trackers.LanguageQuestionKey(subject, "compatibility_mix_compat-2"): "audio-0",
	}
	assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_compat-2")] = "audio-1"
	for _, failure := range evaluateAitherEvidence(t, subject) {
		if failure.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("source-confirmed standalone tracks: %+v", failure)
		}
	}
	subject.LanguageFacts.Tracks[0].EmbeddedCompatibility = true
	subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[:3]
	assertAitherFailure(t, evaluateAitherEvidence(t, subject), "language_compatibility_missing", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestAitherProgrammeTracksInDifferentFilesAreNotAdditionalMixes(t *testing.T) {
	subject := aitherPassingSubject()
	subject.LanguageFacts = aitherTestLanguageFacts("Japanese", []string{"Japanese", "Japanese"}, []string{"English"})
	subject.LanguageFacts.Tracks[0].ResourceID = "episode-1"
	subject.LanguageFacts.Tracks[1].ResourceID = "episode-2"
	if got := additionalMainAudio(subject); len(got) != 0 {
		t.Fatalf("distinct resources treated as duplicate mixes: %+v", got)
	}
}

func TestAitherMultilingualBalanceNamingUsesSourceEvidence(t *testing.T) {
	subject := api.UploadSubject{Type: "REMUX", LanguageFacts: aitherTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"})}
	subject.LanguageFacts.Tracks[0].Languages = []string{"Japanese", "Korean"}
	subject.LanguageFacts.ProgrammeLanguages = []string{"Japanese", "Korean"}
	validation := api.NewTrackerValidationSubject(subject, "AITHER")
	key := trackers.LanguageQuestionKey(validation, "multilingual_balance_audio-0")
	if got := aitherLanguage(subject); got != "" {
		t.Fatalf("unresolved balance guessed marker %q", got)
	}
	assertAitherFailure(t, evaluateAitherEvidence(t, validation), "language_multilingual_balance", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: subject})
	found := false
	for _, field := range questionnaire.Fields {
		if field.Key == key && slices.Contains(field.Options, "predominant:Korean") {
			found = true
		}
	}
	if !found {
		t.Fatalf("balance source review unreachable: %+v", questionnaire)
	}
	for _, test := range []struct{ answer, want string }{{"evenly_split", "MULTIPLE LANGUAGES"}, {"predominant:Korean", "KOREAN"}, {"predominant:German", ""}, {"unresolved", ""}} {
		subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"AITHER": {key: test.answer}}
		if got := aitherLanguage(subject); got != test.want {
			t.Fatalf("answer=%s marker=%q want=%q", test.answer, got, test.want)
		}
	}
	subject.TrackerQuestionnaireAnswers["AITHER"][key] = "evenly_split"
	validation = api.NewTrackerValidationSubject(subject, "AITHER")
	assertAitherFailure(t, evaluateAitherEvidence(t, validation), "language_extra_dub", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Identity.Generation++
	if got := aitherLanguage(subject); got != "" {
		t.Fatalf("stale balance answer reused: %q", got)
	}
	subject.Type = "DISC"
	subject.AudioLanguages = []string{"Japanese"}
	if got := aitherLanguage(subject); got != "JAPANESE" {
		t.Fatalf("full disc changed: %q", got)
	}
}

func TestAitherMultilingualSourceAnswerReachesStructuredName(t *testing.T) {
	subject := aitherGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Film",
		Year:       2026,
		Resolution: "1080p",
		Source:     "WEB",
		VideoCodec: "H.264",
		Audio:      "DD 2.0",
		Tag:        "GRP",
	}, []string{"Japanese"})
	subject.LanguageFacts.Tracks[0].Languages = []string{"Japanese", "Korean"}
	subject.LanguageFacts.ProgrammeLanguages = []string{"Japanese", "Korean"}
	subject.LanguageFacts.OriginalLanguages = []string{"Japanese", "Korean"}
	questionnaire := Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: subject})
	subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"AITHER": {}}
	for _, field := range questionnaire.Fields {
		if strings.HasPrefix(field.Key, "multilingual_balance_") {
			subject.TrackerQuestionnaireAnswers["AITHER"][field.Key] = "evenly_split"
		}
	}
	if got := aitherReviewedName(t, subject, nil); !strings.Contains(got, "MULTIPLE LANGUAGES") {
		t.Fatalf("source answer absent from structured name: %q", got)
	}
	if failures := languageAssessment(api.NewTrackerValidationSubject(subject, "AITHER")); len(failures) != 0 {
		t.Fatalf("resolved multilingual original audio: %+v", failures)
	}
	subject.Identity.Generation++
	if got := aitherReviewedName(t, subject, nil); strings.Contains(got, "MULTIPLE LANGUAGES") || strings.Contains(got, "JAPANESE") {
		t.Fatalf("stale source answer guessed a language marker: %q", got)
	}
}
