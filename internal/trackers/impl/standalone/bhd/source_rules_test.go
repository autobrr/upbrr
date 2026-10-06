// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"strings"
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

func TestBHDSourceHistoryIsStrictAndGenerationBound(t *testing.T) {
	subject := bhdValidationSubject()
	subject.Identity.Generation = 1
	requireBHDValidationFailure(t, languageAssessment(subject), "language_existing_release", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("new release blocked: %#v", failures)
	}
	subject.Identity.Generation++
	requireBHDValidationFailure(t, languageAssessment(subject), "language_existing_release", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	bhdSourceAnswer(&subject, "existing_release", "tracks_or_chapters_added")
	failures := languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_existing_release" && strings.Contains(f.Reason, "Staff approval required") && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
	}) {
		t.Fatalf("added content did not remain staff-only: %#v", failures)
	}
	subject.LanguageFacts = bhdTestLanguageFacts("Japanese", []string{"Japanese", "English", "German"}, nil)
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_extra_dub", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestBHDAlternateMixNeedsContentEvidence(t *testing.T) {
	for _, role := range []api.AudioTrackRole{api.AudioRoleProgramme, api.AudioRoleAlternateMix} {
		subject := bhdValidationSubject()
		subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
			ID:        "second",
			Kind:      api.MediaTrackAudio,
			Role:      role,
			Languages: []string{"English"},
			Title:     "Alternate Mix",
			Codec:     "FLAC",
		})
		bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
		requireBHDValidationFailure(t, languageAssessment(subject), "language_redundant_original", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
		bhdSourceAnswer(&subject, "programme_mixes", "duplicate_main_mix")
		requireBHDValidationFailure(t, languageAssessment(subject), "language_redundant_original", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
		bhdSourceAnswer(&subject, "programme_mixes", "distinct_mixes")
		if failures := languageAssessment(subject); len(failures) != 0 {
			t.Fatalf("distinct mixes blocked: %#v", failures)
		}
	}
	// Tracks from different pack resources are not duplicate versions of one mix.
	subject := bhdValidationSubject()
	second := subject.LanguageFacts.Tracks[0]
	second.ResourceID = "episode-2"
	subject.LanguageFacts.Tracks[0].ResourceID = "episode-1"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, second)
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("separate episodes treated as duplicate mixes: %#v", failures)
	}
}

func TestBHDRemuxRecommendationsStayAdvisory(t *testing.T) {
	subject := bhdValidationSubject()
	subject.Type, subject.DiscType = "REMUX", "BDMV"
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_source_extras", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	for _, answer := range []string{"", "unresolved", "incomplete"} {
		bhdSourceAnswer(&subject, "source_extras", answer)
		bhdSourceAnswer(&subject, "foreign_dialogue_subtitles", "missing")
		failures := languageAssessment(subject)
		for _, rule := range []string{"language_source_extras", "language_foreign_dialogue_subtitles"} {
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule && f.Disposition == api.RuleDispositionAdvisory }) {
				t.Fatalf("recommendation lost or made mandatory: %s: %#v", rule, failures)
			}
		}
	}
	bhdSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
	bhdSourceAnswer(&subject, "foreign_dialogue_subtitles", "included")
	if !slices.ContainsFunc(languageAssessment(subject), func(f api.RuleFailure) bool { return f.Rule == "language_foreign_dialogue_subtitles" }) {
		t.Fatal("unforced full subtitles proved separate forced coverage")
	}
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		Kind:      api.MediaTrackSubtitle,
		Languages: []string{"English"},
		Forced:    true,
	})
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	bhdSourceAnswer(&subject, "source_extras", "retained_or_unavailable")
	bhdSourceAnswer(&subject, "foreign_dialogue_subtitles", "included")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("reviewed remux blocked: %#v", failures)
	}
	subject.Type, subject.DiscType = "DISC", "BDMV"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc assessed: %#v", failures)
	}
}

func TestBHDAnimationAndQuestionApplicability(t *testing.T) {
	subject := bhdValidationSubject()
	subject.LanguageFacts = bhdTestLanguageFacts("Japanese", []string{"Japanese"}, nil)
	subject.EffectiveMetadata.Genres = []string{"Animation"}
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_animated_dual_audio", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
	questionnaire := Profile().ProjectionQuestionnaire
	if questionnaire == nil {
		t.Fatal("BHD source review is not wired")
	}
	for _, typ := range []string{"WEBDL", "REMUX", "DISC"} {
		meta := api.UploadSubject{Type: typ, LanguageFacts: subject.LanguageFacts}
		question := questionnaire(trackers.PreparationInput{Meta: meta})
		if typ == "DISC" {
			if question != nil {
				t.Fatal("full disc questions")
			}
			continue
		}
		if question == nil {
			t.Fatal("source history question missing")
		}
		if typ == "WEBDL" && len(question.Fields) != 1 {
			t.Fatalf("WEB acquired remux questions: %#v", question)
		}
		for _, field := range question.Fields {
			if !strings.HasPrefix(field.Key, "existing_release_") && !strings.HasPrefix(field.Key, "programme_mixes_") && field.Required {
				t.Fatalf("should guidance became a required prompt: %#v", field)
			}
		}
	}
}

// bhdNewReleaseFixture gives unrelated upload/payload fixtures their explicit
// source history; source-policy regressions call the assessment directly.
func bhdNewReleaseFixture(meta api.UploadSubject) api.UploadSubject {
	subject := api.NewTrackerValidationSubject(meta, "BHD")
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{
		"BHD": {trackers.LanguageQuestionKey(subject, "existing_release"): "unchanged_or_new"},
	}
	return meta
}

func TestBHDSourceAnswersDoNotWaiveCompatibilityOrAcceptUnknownOptions(t *testing.T) {
	subject := bhdValidationSubject()
	bhdSourceAnswer(&subject, "existing_release", "staff_approved")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_existing_release", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "compatibility",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCompatibility,
		Languages: []string{"English"},
		Codec:     "AC-3",
	})
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("valid TrueHD compatibility audio acquired mix question: %#v", failures)
	}
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].Codec = "DTS"
	bhdSourceAnswer(&subject, "existing_release", "unchanged_or_new")
	requireBHDValidationFailure(t, languageAssessment(subject), "language_compatibility_format", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestBHDSourceHistoryDebugProjection(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
Category: "MOVIE",
 Type: "WEBDL",
 Title: "Example Release",
 Year: 2026,
 Resolution: "1080p",
 Source: "Web",
 VideoCodec: "H.264",
 Audio: "DD 2.0",
 Tag: "GRP",
})
	subject.Source, subject.Type, subject.Container = "WEB", "WEBDL", "mkv"
	subject.Identity = api.ExternalIdentity{
Category: api.CanonicalCategoryMovie,
 TMDBID: 1234567,
 IMDBID: 1234567,
}
	subject.EffectiveMetadata.Title, subject.EffectiveMetadata.Year = "Example Release", 2026
	subject.Release.Category = "MOVIE"
	subject.Audio, subject.VideoCodec = "DD 2.0", "H.264"
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusNotApplicable
	for _, answer := range []string{"", "unchanged_or_new"} {
		subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"BHD": {trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(subject, "BHD"), "existing_release"): answer}}
		p, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
Tracker: "BHD",
 Meta: subject,
 ExecutionMode: api.WorkflowExecutionModeDebug,
 RequestedUploadName: &subject.ReleaseName,
}, "input", "catalog", "config")
		if failure != nil {
			t.Fatal(failure)
		}
		if answer == "" && !p.DupeReady {
			t.Error("debug source-history eligibility-only questionnaire reblocks duplicate preparation")
		}
		if answer != "" && !p.DupeReady {
			t.Error("control has an unrelated blocker")
		}
	}
}
