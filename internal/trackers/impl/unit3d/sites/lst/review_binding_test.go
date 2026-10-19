// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
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
	subject.QuestionnaireAnswers = map[string]string{exceptionQuestionKey(subject, "exception_note"): "This synthetic source uses its permitted profile-specific container."}
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("single-file HDR evidence lost: %+v", failures)
	}
}

func TestHybridConfirmationInvalidatesWhenSourceChanges(t *testing.T) {
	subject := lstValidationSubject()
	subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	subject.HDRFacts = api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatDolbyVision}}
	subject.QuestionnaireAnswers[assetQuestionKey(subject, "screenshot_preservation")] = "lossless_preserved"
	subject.QuestionnaireAnswers[assetQuestionKey(subject, "hybrid_will_add")] = "will_add_required_data"
	if failures := assetReviewFailures(subject); len(failures) != 0 {
		t.Fatalf("current artifact review: %+v", failures)
	}
	subject.Identity.Generation++
	if failures := assetReviewFailures(subject); len(failures) == 0 {
		t.Fatal("changed source retained confirmation")
	}
}

func TestPresentationQuestionnaireBindsFinalNameAndSource(t *testing.T) {
	meta := api.UploadSubject{
		Type:            "WEBDL",
		PersonalRelease: true,
		ReleaseName:     "canonical",
		LanguageFacts:   lstTestLanguageFacts("English", []string{"English"}, []string{"English"}),
	}
	input := trackers.PreparationInput{Meta: meta, Projection: &api.TrackerReleaseProjection{UploadReleaseName: "reviewed-service-name"}}
	fields := languageQuestionnaire(input).Fields
	index := slices.IndexFunc(fields, func(f api.TrackerQuestionnaireField) bool { return strings.Contains(f.Key, "web_source_naming") })
	if index < 0 {
		t.Fatal("missing naming review")
	}
	key := fields[index].Key
	input.Meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"LST": {key: "reviewed_correct"}}
	if got := languageQuestionnaire(input); !slices.ContainsFunc(got.Fields, func(f api.TrackerQuestionnaireField) bool { return f.Key == key && f.Value == "reviewed_correct" }) {
		t.Fatal("current review not retained")
	}
	input.Projection.UploadReleaseName = "changed-service-name"
	if got := languageQuestionnaire(input); slices.ContainsFunc(got.Fields, func(f api.TrackerQuestionnaireField) bool { return f.Key == key }) {
		t.Fatal("final-name edit retained source review")
	}
	subject := lstValidationSubject()
	subject.Type = "REMUX"
	subject.PersonalRelease = true
	for _, f := range presentationReviewQuestions(subject) {
		if strings.Contains(f.Key, "source_documentation") {
			subject.QuestionnaireAnswers[f.Key] = "will_add_required_data"
		}
	}
	for _, f := range presentationReviewQuestions(subject) {
		if strings.Contains(f.Key, "source_documentation") && (f.Value != "will_add_required_data" || f.NeedsReview) {
			t.Fatalf("current description confirmation not retained: %+v", f)
		}
	}
	subject.Identity.Generation++
	for _, f := range presentationReviewQuestions(subject) {
		if strings.Contains(f.Key, "source_documentation") && (f.Value != "" || !f.NeedsReview) {
			t.Fatalf("changed source retained stale description confirmation: %+v", f)
		}
	}
}

func TestDuplicateConfirmationsBindReferencedEvidence(t *testing.T) {
	meta := api.UploadSubject{Tag: "GRP", TrackerQuestionnaireAnswers: map[string]map[string]string{"LST": {}}}
	set := func(key, value string) {
		subject := api.NewTrackerValidationSubject(meta, "LST")
		meta.TrackerQuestionnaireAnswers["LST"][duplicateEvidenceKey(subject, key)] = value
	}
	set("source", "https://example.test/source-a")
	set("internal_status", "internal")
	set("source_reviewed", "yes")
	if evidence, _ := duplicateEvidence(meta); evidence.InternalStatus != "internal" {
		t.Fatal("current source review missing")
	}
	set("source", "https://example.test/source-b")
	if evidence, _ := duplicateEvidence(meta); evidence.InternalStatus != "" {
		t.Fatal("changed source retained internal authority")
	}
	candidate := api.DupeMatchProjection{
		ID:                  "123",
		Name:                "Existing release",
		EvidenceFingerprint: "current-a",
		Relation:            api.DupeRelationManualReview,
		Reason:              "lst_internal_comparison_required",
	}
	meta.TrackerDuplicateReviewCandidates = map[string][]api.DupeMatchProjection{"LST": {candidate}}
	prefix := duplicateCandidatePrefix(candidate)
	set(prefix+"comparison_source", "https://example.test/comparison")
	set(prefix+"quality", "superior")
	set(prefix+"comparison_reviewed", "yes")
	if _, proofs := duplicateEvidence(meta); len(proofs) != 1 || !proofs[0].Reviewed {
		t.Fatal("current comparison review missing")
	}
	for _, field := range []string{"quality", "comparison_source"} {
		set(prefix+field, "changed")
		if _, proofs := duplicateEvidence(meta); len(proofs) != 1 || proofs[0].Reviewed {
			t.Fatalf("changed %s retained comparison authority", field)
		}
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
	if len(failures) != 1 || !failures[0].QuestionnaireInput || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("missing source review not actionable: %+v", failures)
	}
	subject.QuestionnaireAnswers[sourceQuestionKey(subject, "resource_source_measured")] = "HDTV"
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "exception_note")] = "This resource was captured from the reviewed broadcast source."
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("reviewed broadcast exception: %+v", failures)
	}
	subject.Identity.Generation++
	if len(exceptionValidationFailures(subject)) == 0 {
		t.Fatal("stale broadcast evidence reused")
	}
}
