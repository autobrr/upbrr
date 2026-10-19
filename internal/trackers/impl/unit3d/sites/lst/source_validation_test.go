// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func reviewedLSTEncode(t *testing.T) api.TrackerValidationSubject {
	t.Helper()
	subject := exceptionTestSubject(t, "mkv", "Blu-ray", "x264")
	subject.Type = "ENCODE"
	subject.Release.Resolution = "1080p"
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusPresent
	for _, field := range sourceReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = field.Options[1]
	}
	return subject
}

func TestLSTSourceReviewBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.TrackerValidationSubject)
		rule   string
		status api.MetadataEvidenceStatus
	}{
		{name: "reviewed encode"},
		{
			name:   "prohibited source",
			change: func(s *api.TrackerValidationSubject) { s.Source = "CAM" },
			rule:   "lst_prohibited_source",
			status: api.MetadataEvidenceStatusComplete,
		},
		{
			name:   "missing review",
			change: func(s *api.TrackerValidationSubject) { s.QuestionnaireAnswers = nil },
			rule:   "lst_source_review",
			status: api.MetadataEvidenceStatusPartial,
		},
		{
			name: "wrong rate control",
			change: func(s *api.TrackerValidationSubject) {
				s.QuestionnaireAnswers[sourceQuestionKey(*s, "encode_rate_control")] = "other"
			},
			rule:   "lst_source_review",
			status: api.MetadataEvidenceStatusComplete,
		},
		{
			name: "uncropped",
			change: func(s *api.TrackerValidationSubject) {
				s.QuestionnaireAnswers[sourceQuestionKey(*s, "encode_crop")] = "entirely_uncropped"
			},
			rule:   "lst_source_review",
			status: api.MetadataEvidenceStatusComplete,
		},
		{name: "imperfect crop permitted", change: func(s *api.TrackerValidationSubject) {
			s.QuestionnaireAnswers[sourceQuestionKey(*s, "encode_crop")] = "imperfect_crop"
		}},
		{
			name: "settings missing",
			change: func(s *api.TrackerValidationSubject) {
				s.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusMissing
			},
			rule:   "lst_encode_settings",
			status: api.MetadataEvidenceStatusComplete,
		},
		{
			name: "settings unknown",
			change: func(s *api.TrackerValidationSubject) {
				s.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusUnknown
			},
			rule:   "lst_encode_settings_evidence",
			status: api.MetadataEvidenceStatusPartial,
		},
		{
			name:   "720p x265",
			change: func(s *api.TrackerValidationSubject) { s.Release.Resolution = "720p"; s.VideoEncoder = "x265" },
			rule:   "lst_encode_resolution_codec",
			status: api.MetadataEvidenceStatusComplete,
		},
		{
			name:   "unsupported encoder",
			change: func(s *api.TrackerValidationSubject) { s.VideoEncoder = "NVENC" },
			rule:   "lst_encode_encoder",
			status: api.MetadataEvidenceStatusComplete,
		},
		{
			name:   "codec cannot prove encoder",
			change: func(s *api.TrackerValidationSubject) { s.VideoEncoder = "H.264" },
			rule:   "lst_source_review",
			status: api.MetadataEvidenceStatusPartial,
		},
		{name: "legacy preservation encoder", change: func(s *api.TrackerValidationSubject) { s.VideoEncoder = "XviD"; s.Release.Resolution = "480p" }},
		{name: "remux excludes encode checks", change: func(s *api.TrackerValidationSubject) { s.Type = "REMUX"; s.QuestionnaireAnswers = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := reviewedLSTEncode(t)
			if tc.change != nil {
				tc.change(&subject)
			}
			failures := requiredSourceFailures(t, subject)
			if tc.rule == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %#v", failures)
				}
				return
			}
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == tc.rule && f.EvidenceStatus == tc.status && f.Disposition == api.RuleDispositionStrict
			}) {
				t.Fatalf("failures = %#v", failures)
			}
		})
	}
}

func TestLSTRetailWorkprintAndBelowHDSource(t *testing.T) {
	subject := exceptionTestSubject(t, "mkv", "Workprint", "")
	if failures := requiredSourceFailures(t, subject); len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("unknown workprint = %#v", failures)
	}
	subject.QuestionnaireAnswers[sourceQuestionKey(subject, "workprint_retail")] = "official_retail"
	if failures := requiredSourceFailures(t, subject); len(failures) != 0 {
		t.Fatalf("retail workprint = %#v", failures)
	}
	subject = reviewedLSTEncode(t)
	subject.Release.Resolution = "576p"
	for _, field := range sourceReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = field.Options[1]
	}
	if failures := requiredSourceFailures(t, subject); len(failures) != 0 {
		t.Fatalf("below-HD source = %#v", failures)
	}
	subject.QuestionnaireAnswers[sourceQuestionKey(subject, "encode_source_resolution")] = "source_supports_hd"
	if failures := requiredSourceFailures(t, subject); len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("HD source = %#v", failures)
	}
}

func TestLSTFinishedSeasonEvidenceIsSourceBound(t *testing.T) {
	subject := exceptionTestSubject(t, "mkv", "WEB-DL", "")
	subject.Identity.Category = api.CanonicalCategoryTV
	subject.EpisodeInt = 1
	subject.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: subject.SourcePath,
		Generation: subject.Identity.Generation,
		TVDB:       &api.TVDBMetadata{Status: "Ended"},
	}
	failures := requiredSourceFailures(t, subject)
	if len(failures) != 1 || failures[0].Rule != "lst_finished_season_episode" {
		t.Fatalf("ended = %#v", failures)
	}
	subject.ProviderMetadata.Generation++
	failures = requiredSourceFailures(t, subject)
	if len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("stale metadata = %#v", failures)
	}
	subject.TVPack = true
	if failures := requiredSourceFailures(t, subject); len(failures) != 0 {
		t.Fatalf("season pack = %#v", failures)
	}
}

func TestLSTSourceReviewBindsEveryResource(t *testing.T) {
	subject := reviewedLSTEncode(t)
	subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{{
		Container:           "mkv",
		Source:              "Blu-ray",
		VideoEncoder:        "x264",
		Resolution:          "1080p",
		EncodeSettingsKnown: true,
		HasEncodeSettings:   true,
	}, {
		Container:           "mkv",
		Source:              "CAM",
		VideoEncoder:        "x264",
		Resolution:          "1080p",
		EncodeSettingsKnown: true,
		HasEncodeSettings:   true,
	}}}
	failures := requiredSourceFailures(t, subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "lst_resource_prohibited_source" && f.EvidenceStatus == api.MetadataEvidenceStatusComplete
	}) {
		t.Fatalf("secondary prohibited source = %#v", failures)
	}
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "lst_source_review" && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
	}) {
		t.Fatalf("stale review survived resources changing = %#v", failures)
	}
	subject.MediaFileFacts.Files[1].Source = "Blu-ray"
	for _, field := range sourceReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = field.Options[1]
	}
	if failures := requiredSourceFailures(t, subject); len(failures) != 0 {
		t.Fatalf("reviewed pack = %#v", failures)
	}
	subject.MediaFileFacts.Files[1].Resolution = "720p"
	subject.MediaFileFacts.Files[1].VideoEncoder = "x265"
	if failures := requiredSourceFailures(t, subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "lst_resource_encode_resolution_codec" }) {
		t.Fatalf("secondary encode violation = %#v", failures)
	}
}

func TestLSTMeasuredEncoderAndAV1Settings(t *testing.T) {
	subject := reviewedLSTEncode(t)
	subject.VideoEncoder = ""
	subject.VideoEncode = "x264"
	failures := requiredSourceFailures(t, subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "lst_source_review" && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
	}) {
		t.Fatalf("naming encoder inferred measured identity: %#v", failures)
	}
	subject.VideoEncoder = "AV1"
	subject.VideoCodec = "AV1"
	subject.HasEncodeSettings = true
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusNotApplicable
	for _, field := range sourceReviewQuestions(subject) {
		subject.QuestionnaireAnswers[field.Key] = field.Options[1]
	}
	if failures := requiredSourceFailures(t, subject); len(failures) != 0 {
		t.Fatalf("measured AV1 settings rejected: %#v", failures)
	}
}

func requiredSourceFailures(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	return withoutLSTStreamProcessingGuidance(t, sourceValidationFailures(subject))
}
