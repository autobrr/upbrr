// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func bhdSourceAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = make(map[string]string)
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func requireBHDSourceWarnings(t *testing.T, subject api.TrackerValidationSubject, rules ...string) {
	t.Helper()
	failures := languageAssessment(subject)
	if len(failures) != len(rules) {
		t.Fatalf("warnings = %#v, want %v", failures, rules)
	}
	for _, rule := range rules {
		requireBHDValidationFailure(t, failures, rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	}
	for _, failure := range failures {
		if trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false) {
			t.Fatalf("source warning blocked execution: %#v", failure)
		}
	}
}

func TestBHDSourceHistoryWarningIgnoresSavedAnswers(t *testing.T) {
	for _, answer := range []string{"", "unresolved", "unchanged_or_new", "tracks_or_chapters_added", "staff_approved"} {
		t.Run(answer, func(t *testing.T) {
			subject := bhdValidationSubject()
			bhdSourceAnswer(&subject, "existing_release", answer)
			requireBHDSourceWarnings(t, subject, "language_existing_release")
			subject.Identity.Generation++
			requireBHDSourceWarnings(t, subject, "language_existing_release")
		})
	}
	if Profile().ProjectionQuestionnaire != nil {
		t.Fatal("BHD source warnings still request answers")
	}
}

func TestBHDAlternateMixNeedsContentEvidence(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleProgramme, api.AudioRoleAlternateMix} {
		for _, answer := range []string{"", "distinct_mixes", "duplicate_main_mix"} {
			subject := bhdValidationSubject()
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
				ID:        "second",
				Kind:      api.MediaTrackAudio,
				Role:      role,
				Languages: []string{"English"},
				Title:     "Alternate Mix",
				Codec:     "FLAC",
			})
			bhdSourceAnswer(&subject, "programme_mixes", answer)
			requireBHDSourceWarnings(t, subject, "language_existing_release", "language_redundant_original")
		}
	}
	// Separate pack resources do not require within-resource mix comparison.
	subject := bhdValidationSubject()
	second := subject.LanguageFacts.Tracks[0]
	second.ResourceID = "episode-2"
	subject.LanguageFacts.Tracks[0].ResourceID = "episode-1"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, second)
	requireBHDSourceWarnings(t, subject, "language_existing_release")
}

func TestBHDRemuxRecommendationsStayAdvisory(t *testing.T) {
	for _, answers := range [][2]string{
		{"", ""},
		{"unresolved", "unresolved"},
		{"incomplete", "missing"},
		{"retained_or_unavailable", "included"},
		{"retained_or_unavailable", "not_required"},
	} {
		subject := bhdValidationSubject()
		subject.Type, subject.DiscType = "REMUX", "BDMV"
		bhdSourceAnswer(&subject, "source_extras", answers[0])
		bhdSourceAnswer(&subject, "foreign_dialogue_subtitles", answers[1])
		requireBHDSourceWarnings(t, subject, "language_existing_release", "language_source_extras", "language_foreign_dialogue_subtitles")
		// A forced flag does not establish coverage of the programme's dialogue.
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
			Kind:      api.MediaTrackSubtitle,
			Languages: []string{"English"},
			Forced:    true,
		})
		requireBHDSourceWarnings(t, subject, "language_existing_release", "language_source_extras", "language_foreign_dialogue_subtitles")
		subject.Type = "DISC"
		requireBHDSourceWarnings(t, subject)
	}
	subject := bhdValidationSubject()
	subject.Type = "REMUX"
	subject.LanguageFacts = bhdTestLanguageFacts("ZXX", []string{"ZXX"}, nil)
	requireBHDSourceWarnings(t, subject, "language_existing_release", "language_source_extras")
	subject.LanguageFacts = bhdTestLanguageFacts("English", nil, nil)
	subject.LanguageFacts.AudioAbsent = true
	if slices.ContainsFunc(languageAssessment(subject), func(f api.RuleFailure) bool { return f.Rule == "language_foreign_dialogue_subtitles" }) {
		t.Fatal("absent audio acquired hypothetical subtitle guidance")
	}
	requireBHDValidationFailure(t, languageAssessment(subject), "language_primary_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestBHDAnimationPreferenceAndMeasuredRestrictions(t *testing.T) {
	subject := bhdValidationSubject()
	subject.LanguageFacts = bhdTestLanguageFacts("Japanese", []string{"Japanese"}, nil)
	subject.EffectiveMetadata.Genres = []string{"Animation"}
	requireBHDValidationFailure(t, languageAssessment(subject), "language_animated_dual_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
	subject.LanguageFacts = bhdTestLanguageFacts("Japanese", []string{"Japanese", "English", "German"}, nil)
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_extra_dub", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)

	subject = bhdValidationSubject()
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "compatibility",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Languages: []string{"English"},
		Codec:     "AC-3",
	})
	requireBHDSourceWarnings(t, subject, "language_existing_release")
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].Codec = "DTS"
	bhdSourceAnswer(&subject, "programme_mixes", "distinct_mixes")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestBHDSourceHistoryDoesNotGateProjection(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		VideoCodec: "H.264",
		Audio:      "DD 2.0",
		Tag:        "GRP",
	})
	subject.Source, subject.Type, subject.Container = "WEB", "WEBDL", "mkv"
	subject.Identity = api.ExternalIdentity{
		Category: api.CanonicalCategoryMovie,
		TMDBID:   1234567,
		IMDBID:   1234567,
	}
	subject.EffectiveMetadata.Title, subject.EffectiveMetadata.Year = "Example Release", 2026
	subject.Release.Category = "MOVIE"
	subject.Audio, subject.VideoCodec = "DD 2.0", "H.264"
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusNotApplicable
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		for _, answer := range []string{"", "unchanged_or_new", "tracks_or_chapters_added"} {
			key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(subject, "BHD"), "existing_release")
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"BHD": {key: answer}}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker:             "BHD",
				Meta:                subject,
				ExecutionMode:       mode,
				RequestedUploadName: &subject.ReleaseName,
			}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			if !projection.DupeReady || len(projection.Questionnaire) != 0 {
				t.Fatalf("source history gated %s projection: %#v", mode, projection)
			}
			if slices.ContainsFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
				return action.Kind == api.RequiredActionAnswerQuestionnaire
			}) {
				t.Fatal("source history requested questionnaire answers")
			}
		}
	}
}

func TestBHDOrdinaryCompatibilityIsNotARedundantProgrammeMix(t *testing.T) {
	subject := bhdValidationSubject()
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "standalone",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleProgramme,
		Codec:     "DD",
		Languages: []string{"English"},
	})
	requireBHDSourceWarnings(t, subject, "language_existing_release")
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].Codec = "DD+"
	if failures := languageAssessment(subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_compatibility_format" && f.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("BHD AC-3-only constraint lost: %+v", failures)
	}
}
