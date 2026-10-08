// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func ptpLanguageSubject(original string, programme ...string) api.TrackerValidationSubject {
	media := api.MediaFacts{
		OriginalLanguage:      original,
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "audio-0",
	}
	for i, language := range programme {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:           "audio-" + strconv.Itoa(i),
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Languages:    []string{language},
			Default:      i == 0,
			DefaultKnown: true,
		})
	}
	return api.TrackerValidationSubject{
		Tracker:       "PTP",
		SourcePath:    "ptp-language",
		Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie, Generation: 1},
		Type:          "ENCODE",
		Source:        "BluRay",
		Container:     "mkv",
		VideoCodec:    "H.264",
		Release:       api.ReleaseInfo{Resolution: "1080p"},
		LanguageFacts: mediafacts.ResolveLanguages(media),
	}
}

func ptpLanguageAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = make(map[string]string)
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func requirePTPLanguageFailure(t *testing.T, failures []api.RuleFailure, rule string, disposition api.RuleDisposition) {
	t.Helper()
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule && f.Disposition == disposition }) {
		t.Fatalf("missing %s/%s in %#v", rule, disposition, failures)
	}
}

func TestPTPLanguageFindingsAccumulateWithoutLegacyWaivers(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "German")
	ptpLanguageAnswer(&subject, "english_subtitle_manager", "missing")
	ptpLanguageAnswer(&subject, "forced_english_dialogue", "missing")
	subject.QuestionnaireAnswers["no_english_subtitles"] = "no"
	subject.QuestionnaireAnswers["trumpable_review"] = "no"
	failures := languageFailures(subject)
	requirePTPLanguageFailure(t, failures, "language_non_english_dub", api.RuleDispositionWaivable)
	requirePTPLanguageFailure(t, failures, "language_english_subtitles", api.RuleDispositionAdvisory)
	for _, failure := range failures {
		if failure.Disposition == api.RuleDispositionWaivable && (!strings.Contains(failure.Reason, "Trumpable release") || !strings.Contains(failure.Reason, "compliant replacement")) {
			t.Fatalf("unlabelled defect: %#v", failure)
		}
	}
	subject.Container = "mp4"
	all, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("PTP", all)
	if err != nil {
		t.Fatal(err)
	}
	blocking, err := trackers.FirstBlockingRuleFailure("PTP", all, api.WorkflowExecutionModeNormal, &api.TrackerReleaseProjection{WaivableRuleFingerprint: fingerprint, RuleAuthorizationFingerprint: fingerprint})
	if err != nil || blocking == nil || blocking.Rule != "unsupported_container" {
		t.Fatalf("waiver removed independent strict finding: %#v, %v", blocking, err)
	}
}

func TestPTPPrimaryProgrammeSubtitleApplicability(t *testing.T) {
	english := ptpLanguageSubject("Japanese", "English", "Japanese")
	ptpLanguageAnswer(&english, "forced_english_dialogue", "not_required")
	if failures := languageFailures(english); len(failures) != 0 {
		t.Fatalf("English-primary input acquired a universal caption requirement: %#v", failures)
	}
	unknownOriginal := ptpLanguageSubject("", "English")
	ptpLanguageAnswer(&unknownOriginal, "forced_english_dialogue", "not_required")
	if failures := languageFailures(unknownOriginal); len(failures) != 0 {
		t.Fatalf("English-only programme required irrelevant original evidence: %#v", failures)
	}
	foreign := ptpLanguageSubject("Japanese", "Japanese", "English")
	ptpLanguageAnswer(&foreign, "english_subtitle_manager", "missing")
	ptpLanguageAnswer(&foreign, "forced_english_dialogue", "not_required")
	requirePTPLanguageFailure(t, languageFailures(foreign), "language_english_subtitles", api.RuleDispositionAdvisory)
	ptpLanguageAnswer(&foreign, "english_subtitle_manager", "available")
	requirePTPLanguageFailure(t, languageFailures(foreign), "language_english_subtitles", api.RuleDispositionAdvisory)
	foreign.LanguageFacts.PrimaryAudioTrackID = "unknown"
	requirePTPLanguageFailure(t, languageFailures(foreign), "language_primary_evidence", api.RuleDispositionAdvisory)
}

func TestPTPSourceAnswersDoNotChangeWarningsOrCreateQuestions(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "German")
	for _, answer := range []string{"", "distinct_content", "redundant", "unresolved"} {
		ptpLanguageAnswer(&subject, "programme_track_purpose", answer)
		ptpLanguageAnswer(&subject, "english_subtitle_manager", "available")
		ptpLanguageAnswer(&subject, "forced_english_dialogue", "not_required")
		for _, change := range []func(*api.TrackerValidationSubject){
			func(*api.TrackerValidationSubject) {},
			func(s *api.TrackerValidationSubject) { s.Identity.Generation++ },
			func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix },
		} {
			changed := subject
			changed.LanguageFacts = subject.LanguageFacts.Clone()
			change(&changed)
			if fields := languageReviewFields(changed, api.WorkflowExecutionModeNormal); len(fields) != 0 {
				t.Fatalf("source review became a questionnaire: %+v", fields)
			}
			failures := languageFailures(changed)
			requirePTPLanguageFailure(t, failures, "language_redundant_audio", api.RuleDispositionAdvisory)
			requirePTPLanguageFailure(t, failures, "language_english_subtitles", api.RuleDispositionAdvisory)
			if len(ptpNonAdvisoryFailures(changed)) != 0 {
				t.Fatalf("source answer gated upload: %+v", failures)
			}
		}
	}
	for _, disc := range []string{"", "DVD", "BDMV"} {
		subject.Type, subject.DiscType = "DISC", disc
		if failures := languageFailures(subject); len(failures) != 0 {
			t.Fatalf("full disc assessed: %+v", failures)
		}
		if fields := languageReviewFields(subject, api.WorkflowExecutionModeNormal); len(fields) != 0 {
			t.Fatalf("full disc questioned: %+v", fields)
		}
	}
}

func TestPTPSubtitleWarningsPreserveMeasuredEvidenceBoundaries(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese")
	for _, status := range []api.MetadataEvidenceStatus{api.MetadataEvidenceStatusPartial, api.MetadataEvidenceStatusComplete} {
		subject.LanguageFacts.SubtitleStatus = status
		for _, answer := range []string{"missing", "available", "unresolved"} {
			ptpLanguageAnswer(&subject, "english_subtitle_manager", answer)
			ptpLanguageAnswer(&subject, "forced_english_dialogue", "missing")
			failures := languageFailures(subject)
			requirePTPLanguageFailure(t, failures, "language_english_subtitles", api.RuleDispositionAdvisory)
			if len(ptpNonAdvisoryFailures(subject)) != 0 {
				t.Fatalf("hypothetical coverage gated upload: %+v", failures)
			}
		}
	}
	subject.LanguageFacts.SubtitleLanguages = []string{"English"}
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("current English subtitle evidence ignored: %+v", failures)
	}
	subject.LanguageFacts.SubtitleLanguages = nil
	requirePTPLanguageFailure(t, languageFailures(subject), "language_english_subtitles", api.RuleDispositionAdvisory)
	subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
	requirePTPLanguageFailure(t, languageFailures(subject), "language_evidence", api.RuleDispositionStrict)
}

func TestPTPNoAudioIsNotADub(t *testing.T) {
	subject := ptpLanguageSubject("Japanese")
	for _, facts := range []api.LanguageFacts{
		mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			AudioAbsent:           true,
			TrackCoverageComplete: true,
		}),
		ptpLanguageSubject("ZXX", "ZXX").LanguageFacts,
	} {
		subject.LanguageFacts = facts
		if failures := languageFailures(subject); len(failures) != 0 {
			t.Fatalf("no-dialogue release classified as dubbed: %#v", failures)
		}
	}
}

func TestPTPRepeatedProgrammeAudioProducesSourceGuidance(t *testing.T) {
	subject := ptpLanguageSubject("English", "English", "English")
	for _, role := range []api.AudioTrackRole{api.AudioRoleProgramme, api.AudioRoleAlternateMix} {
		subject.LanguageFacts.Tracks[1].Role = role
		for _, answer := range []string{"", "distinct_content", "redundant"} {
			ptpLanguageAnswer(&subject, "programme_track_purpose", answer)
			requirePTPLanguageFailure(t, languageFailures(subject), "language_redundant_audio", api.RuleDispositionAdvisory)
		}
	}
	for _, role := range []api.AudioTrackRole{api.AudioRoleCommentary, api.AudioRoleCompatibility, api.AudioRoleIsolatedScore} {
		subject.LanguageFacts.Tracks[1].Role = role
		if failures := languageFailures(subject); len(failures) != 0 {
			t.Fatalf("secondary %s treated as duplicate programme: %+v", role, failures)
		}
	}
}

func ptpNonAdvisoryFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	return slices.DeleteFunc(languageFailures(subject), func(failure api.RuleFailure) bool {
		return failure.Disposition == api.RuleDispositionAdvisory
	})
}

func TestPTPMultilingualPrimaryDoesNotGateSubtitleGuidance(t *testing.T) {
	for _, subtitles := range [][]string{{"English"}, nil} {
		subject := ptpLanguageSubject("Japanese", "English")
		subject.LanguageFacts.Tracks[0].Languages = []string{"English", "Japanese"}
		subject.LanguageFacts.ProgrammeLanguages = []string{"English", "Japanese"}
		subject.LanguageFacts.SubtitleLanguages = subtitles
		failures := languageFailures(subject)
		if len(subtitles) > 0 {
			if len(failures) != 0 {
				t.Fatalf("known English subtitles acquired applicability findings: %+v", failures)
			}
		} else {
			requirePTPLanguageFailure(t, failures, "language_primary_evidence", api.RuleDispositionAdvisory)
			for _, failure := range failures {
				if trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false) {
					t.Fatalf("subtitle guidance blocked: %+v", failure)
				}
			}
		}
	}
}
