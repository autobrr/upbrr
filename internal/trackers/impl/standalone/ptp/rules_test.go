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
OriginalLanguage: original,
 TrackCoverageComplete: true,
 PrimaryAudioTrackID: "audio-0",
}
	for i, language := range programme {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID: "audio-" + strconv.Itoa(i),
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
			Languages: []string{language},
 Default: i == 0,
		})
	}
	return api.TrackerValidationSubject{
		Tracker: "PTP",
 SourcePath: "ptp-language",
 Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, Generation: 1},
		Type: "ENCODE",
 Source: "BluRay",
 Container: "mkv",
 VideoCodec: "H.264",
 Release: api.ReleaseInfo{Resolution: "1080p"},
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
	for _, rule := range []string{"language_non_english_dub", "language_english_subtitles", "language_forced_english_subtitles"} {
		requirePTPLanguageFailure(t, failures, rule, api.RuleDispositionWaivable)
	}
	for _, failure := range failures {
		if !strings.Contains(failure.Reason, "Trumpable release") || !strings.Contains(failure.Reason, "compliant replacement") {
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
	requirePTPLanguageFailure(t, languageFailures(foreign), "language_english_subtitles", api.RuleDispositionWaivable)
	ptpLanguageAnswer(&foreign, "english_subtitle_manager", "available")
	if failures := languageFailures(foreign); len(failures) != 0 {
		t.Fatalf("manager availability ignored: %#v", failures)
	}
	foreign.LanguageFacts.PrimaryAudioTrackID = "unknown"
	requirePTPLanguageFailure(t, languageFailures(foreign), "language_primary_evidence", api.RuleDispositionStrict)
}

func TestPTPLanguageReviewFactsAndGenerationInvalidateAnswers(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "German")
	ptpLanguageAnswer(&subject, "programme_track_purpose", "distinct_content")
	ptpLanguageAnswer(&subject, "english_subtitle_manager", "available")
	ptpLanguageAnswer(&subject, "forced_english_dialogue", "not_required")
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("reviewed facts blocked: %#v", failures)
	}
	for _, change := range []func(*api.TrackerValidationSubject){
		func(s *api.TrackerValidationSubject) { s.Identity.Generation++ },
		func(s *api.TrackerValidationSubject) {
			s.LanguageFacts = s.LanguageFacts.Clone()
			s.LanguageFacts.PrimaryAudioTrackID = "audio-1"
		},
		func(s *api.TrackerValidationSubject) {
			s.LanguageFacts = s.LanguageFacts.Clone()
			s.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix
		},
	} {
		changed := subject
		change(&changed)
		fields := languageReviewFields(changed, api.WorkflowExecutionModeNormal)
		if len(fields) == 0 || slices.ContainsFunc(fields, func(field api.TrackerQuestionnaireField) bool { return field.Value != "" }) {
			t.Fatalf("changed evidence reused answers: %#v", fields)
		}
		if failures := languageFailures(changed); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Disposition == api.RuleDispositionStrict }) {
			t.Fatalf("changed evidence did not require reassessment: %#v", failures)
		}
	}
	for _, disc := range []string{"", "DVD", "BDMV"} {
		discSubject := subject
		discSubject.Type, discSubject.DiscType = "DISC", disc
		discSubject.QuestionnaireAnswers = nil
		if failures := languageFailures(discSubject); len(failures) != 0 {
			t.Fatalf("full disc assessed: %#v", failures)
		}
		if fields := languageReviewFields(discSubject, api.WorkflowExecutionModeNormal); len(fields) != 0 {
			t.Fatalf("full disc acquired questions: %#v", fields)
		}
	}
	subject.Type, subject.DiscType = "REMUX", "BDMV"
	subject.QuestionnaireAnswers = nil
	requirePTPLanguageFailure(t, languageFailures(subject), "language_redundant_audio", api.RuleDispositionStrict)
}

func TestPTPSubtitleCompletenessAndForcedEvidence(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese")
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
	ptpLanguageAnswer(&subject, "english_subtitle_manager", "missing")
	ptpLanguageAnswer(&subject, "forced_english_dialogue", "missing")
	for _, rule := range []string{"language_english_subtitles", "language_forced_english_subtitles"} {
		requirePTPLanguageFailure(t, languageFailures(subject), rule, api.RuleDispositionStrict)
	}
	ptpLanguageAnswer(&subject, "english_subtitle_manager", "available")
	ptpLanguageAnswer(&subject, "forced_english_dialogue", "available_in_manager")
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("known manager alternatives required irrelevant local completeness: %#v", failures)
	}
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
},
			{Kind: api.MediaTrackSubtitle, Languages: []string{"English (Forced)"}},
		},
	}
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	subject.QuestionnaireAnswers = nil
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("finalized forced-English evidence ignored: %#v", failures)
	}
	media.SubtitleLanguages, media.SubtitleLanguagesProvenance = nil, api.FactProvenanceManualEmpty
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if ptpHasForcedEnglish(subject) {
		t.Fatal("subtitle clear reused retained track English evidence")
	}
	requirePTPLanguageFailure(t, languageFailures(subject), "language_forced_english_subtitles", api.RuleDispositionStrict)
	media.AudioLanguagesProvenance = api.FactProvenanceManualEmpty
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	requirePTPLanguageFailure(t, languageFailures(subject), "language_evidence", api.RuleDispositionStrict)
	media.AudioLanguagesProvenance = api.FactProvenanceAutomatic
	media.Tracks[0].Role = ""
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	requirePTPLanguageFailure(t, languageFailures(subject), "language_evidence", api.RuleDispositionStrict)
}

func TestPTPNoAudioIsNotADub(t *testing.T) {
	subject := ptpLanguageSubject("Japanese")
	for _, facts := range []api.LanguageFacts{
		mediafacts.ResolveLanguages(api.MediaFacts{
OriginalLanguage: "Japanese",
 AudioAbsent: true,
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

func TestPTPRepeatedProgrammeAudioNeedsSourceEvidence(t *testing.T) {
	subject := ptpLanguageSubject("English", "English", "English")
	ptpLanguageAnswer(&subject, "forced_english_dialogue", "not_required")
	requirePTPLanguageFailure(t, languageFailures(subject), "language_redundant_audio", api.RuleDispositionStrict)
	ptpLanguageAnswer(&subject, "programme_track_purpose", "distinct_content")
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("distinct mixes were treated as duplicates: %#v", failures)
	}
	subject.LanguageFacts.Tracks[1].Role = api.AudioRoleAlternateMix
	ptpLanguageAnswer(&subject, "programme_track_purpose", "distinct_content")
	ptpLanguageAnswer(&subject, "forced_english_dialogue", "not_required")
	if failures := languageFailures(subject); len(failures) != 0 {
		t.Fatalf("reviewed distinct alternate mix was treated as redundant: %#v", failures)
	}
	ptpLanguageAnswer(&subject, "programme_track_purpose", "redundant")
	requirePTPLanguageFailure(t, languageFailures(subject), "language_redundant_audio", api.RuleDispositionWaivable)
	for _, role := range []api.AudioTrackRole{api.AudioRoleCommentary, api.AudioRoleCompatibility, api.AudioRoleIsolatedScore} {
		secondary := ptpLanguageSubject("English", "English")
		secondary.LanguageFacts.Tracks = append(secondary.LanguageFacts.Tracks, api.MediaTrackFacts{
Kind: api.MediaTrackAudio,
 Role: role,
 Languages: []string{"English"},
})
		ptpLanguageAnswer(&secondary, "forced_english_dialogue", "not_required")
		if failures := languageFailures(secondary); len(failures) != 0 {
			t.Fatalf("secondary %s treated as duplicate programme: %#v", role, failures)
		}
	}
}
