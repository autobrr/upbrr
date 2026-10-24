// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSingleFileValidationRetainsInspectedFields(t *testing.T) {
	meta := api.UploadSubject{
		SourcePath:        "/synthetic/movie.mkv",
		VideoPath:         "/synthetic/movie.mkv",
		FileList:          []string{"/synthetic/movie.mkv"},
		Type:              "ENCODE",
		Container:         "mkv",
		VideoEncoder:      "x264",
		HasEncodeSettings: true,
		Release:           api.ReleaseInfo{Resolution: "1080p"},
		LanguageFacts:     lstTestLanguageFacts("English", []string{"English"}, []string{"English"}),
	}
	subject := api.NewTrackerValidationSubject(meta, "LST")
	if len(subject.MediaFileFacts.Files) != 1 || !subject.MediaFileFacts.Files[0].EncodeSettingsKnown || !subject.MediaFileFacts.Files[0].HasEncodeSettings || subject.MediaFileFacts.Files[0].VideoEncoder != "x264" {
		t.Fatalf("single-file facts lost: %+v", subject.MediaFileFacts)
	}
	for _, failure := range sourceValidationFailures(subject) {
		if strings.Contains(failure.Rule, "settings") || strings.Contains(failure.Rule, "encoder") {
			t.Fatalf("inspected evidence lost at validation: %+v", failure)
		}
	}
	meta.Type = "WEBDL"
	meta.Source = "WEB-DL"
	meta.Container = "mp4"
	meta.HDRFacts = api.HDRFacts{
		Status:             api.HDREvidenceComplete,
		DolbyVisionProfile: "5",
		Formats:            []api.HDRFormat{api.HDRFormatDolbyVision},
	}
	subject = api.NewTrackerValidationSubject(meta, "LST")
	subject.QuestionnaireAnswers = map[string]string{exceptionQuestionKey(subject): "This synthetic source uses its permitted profile-specific container."}
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("single-file HDR evidence lost: %+v", failures)
	}
}

func TestHybridStaleConfirmationDoesNotSuppressGuidance(t *testing.T) {
	s := lstValidationSubject()
	s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	s.HDRFacts = api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatDolbyVision}}
	s.QuestionnaireAnswers[assetQuestionKey(s, "hybrid_will_add")] = "will_add_required_data"
	for range 2 {
		requireLSTValidationFailure(t, assetReviewFailures(s), "guidance_lst_hybrid_hdr_evidence", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		s.Identity.Generation++
	}
}

func TestMeasuredContainerAndSourceReview(t *testing.T) {
	subject := lstValidationSubject()
	subject.Container = "avi"
	subject.Release.Resolution = "2160p"
	subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{
		Primary:             true,
		ResourceID:          "measured",
		Container:           "mkv",
		Resolution:          "1080p",
		VideoEncoder:        "x264",
		HasEncodeSettings:   true,
		EncodeSettingsKnown: true,
	}}}
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("filename container overrode measurement: %+v", failures)
	}
	if subject.Container != "avi" || subject.Release.Resolution != "2160p" {
		t.Fatal("canonical naming facts mutated")
	}
	subject.Type = "HDTV"
	subject.MediaFileFacts.Files[0].Container = "ts"
	failures := exceptionValidationFailures(subject)
	if len(failures) != 1 || failures[0].Rule != "lst_exception_note" || !failures[0].QuestionnaireInput {
		t.Fatalf("unverified exception lost required explanation: %+v", failures)
	}
	subject.QuestionnaireAnswers[sourceQuestionKey(subject, "resource_source_measured")] = "HDTV"
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject)] = "The transport stream source needs review."
	failures = exceptionValidationFailures(subject)
	if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionAdvisory || failures[0].QuestionnaireInput {
		t.Fatalf("saved answer established broadcast provenance: %+v", failures)
	}
	subject.MediaFileFacts.Files[0].Source = "HDTV"
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject)] = "This resource uses an established broadcast source."
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("measured broadcast exception: %+v", failures)
	}
	subject.Identity.Generation++
	if len(exceptionValidationFailures(subject)) == 0 {
		t.Fatal("stale exception explanation reused")
	}
}
