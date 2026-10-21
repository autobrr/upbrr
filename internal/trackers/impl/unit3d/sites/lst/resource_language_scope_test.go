// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resourceScopeSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker: "LST",
		Type:    "WEBDL",
		LanguageFacts: api.LanguageFacts{
			OriginalLanguages:      []string{"Japanese"},
			OriginalLanguagesKnown: true,
			ProgrammeLanguages:     []string{"English", "Japanese"},
			SubtitleLanguages:      []string{"English"},
			ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
			SubtitleStatus:         api.MetadataEvidenceStatusComplete,
			TrackCoverageComplete:  true,
			Tracks: []api.MediaTrackFacts{
				{
					ID:           "a",
					ResourceID:   "A",
					Kind:         api.MediaTrackAudio,
					Role:         api.AudioRoleProgramme,
					Languages:    []string{"English"},
					Default:      true,
					DefaultKnown: true,
				},
				{
					ID:         "as",
					ResourceID: "A",
					Kind:       api.MediaTrackSubtitle,
					Languages:  []string{"English"},
				},
				{
					ID:           "b",
					ResourceID:   "B",
					Kind:         api.MediaTrackAudio,
					Role:         api.AudioRoleProgramme,
					Languages:    []string{"Japanese"},
					Default:      true,
					DefaultKnown: true,
				},
			},
		},
	}
}

func TestLSTResourceLanguageScope(t *testing.T) {
	subject := resourceScopeSubject()
	before := subject.LanguageFacts.Clone()
	key := trackers.LanguageQuestionKey(subject, "test")
	failures := languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_subtitles_search" && strings.Contains(f.Reason, "resource B:")
	}) {
		t.Fatalf("foreign resource bypassed: %#v", failures)
	}
	if !titleSearchPolicy().Required(subject) {
		t.Fatal("English resource suppressed foreign-resource search")
	}
	if !reflect.DeepEqual(before, subject.LanguageFacts) || key != trackers.LanguageQuestionKey(subject, "test") {
		t.Fatal("canonical evidence mutated")
	}
	subject.LanguageFacts.Tracks[2].Languages = nil
	failures = languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_evidence" && strings.Contains(f.Reason, "resource B:") && f.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("unknown resource downgraded: %#v", failures)
	}
}

func TestLSTResourceLanguageAbsenceAndUnavailable(t *testing.T) {
	subject := resourceScopeSubject()
	subject.MediaFileFacts.Files = []api.MediaFileFact{{
		ResourceID:     "C",
		AudioStatus:    api.MetadataEvidenceStatusComplete,
		SubtitleStatus: api.MetadataEvidenceStatusComplete,
	}}
	facts := resourceLanguageFacts(subject, "C", nil)
	if !facts.AudioAbsent || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || facts.SubtitleStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("positive absence lost: %#v", facts)
	}
	subject.MediaFileFacts.Files[0].AudioStatus = api.MetadataEvidenceStatusUnavailable
	subject.MediaFileFacts.Files[0].SubtitleStatus = api.MetadataEvidenceStatusUnavailable
	facts = resourceLanguageFacts(subject, "C", nil)
	if facts.AudioAbsent || facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete || facts.SubtitleStatus == api.MetadataEvidenceStatusComplete {
		t.Fatalf("unavailable became absence: %#v", facts)
	}
	facts = resourceLanguageFacts(subject, "missing", nil)
	if facts.AudioAbsent || facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete || facts.SubtitleStatus == api.MetadataEvidenceStatusComplete {
		t.Fatalf("missing file became absence: %#v", facts)
	}
}

func TestLSTResourceLanguageKeepsCorrections(t *testing.T) {
	subject := resourceScopeSubject()
	subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
	facts := resourceLanguageFacts(subject, "A", subject.LanguageFacts.Tracks[:2])
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
		t.Fatal("contradiction lost")
	}
	subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
	subject.LanguageFacts.ProgrammeLanguages = nil
	facts = resourceLanguageFacts(subject, "A", subject.LanguageFacts.Tracks[:2])
	if len(facts.ProgrammeLanguages) != 0 || facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
		t.Fatal("aggregate clear lost")
	}
	subject = resourceScopeSubject()
	subject.LanguageFacts.Tracks[0].Languages = nil
	subject.LanguageFacts.Tracks[0].LanguageProvenance = api.FactProvenanceManual
	facts = resourceLanguageFacts(subject, "A", subject.LanguageFacts.Tracks[:2])
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
		t.Fatal("track clear lost")
	}
	facts.Tracks[1].Languages[0] = "French"
	if subject.LanguageFacts.Tracks[1].Languages[0] != "English" {
		t.Fatal("resource slices alias canonical facts")
	}
}

func TestLSTResourceDefaultKnownStates(t *testing.T) {
	subject := resourceScopeSubject()
	subject.PersonalRelease = true
	subject.MediaFileFacts.Files = []api.MediaFileFact{{
		ResourceID:     "C",
		AudioStatus:    api.MetadataEvidenceStatusComplete,
		SubtitleStatus: api.MetadataEvidenceStatusComplete,
	}}
	for _, failure := range languageAssessment(subject) {
		if failure.Rule == "language_default_audio" && strings.Contains(failure.Reason, "resource C:") {
			t.Fatalf("absent audio required default: %#v", failure)
		}
	}
	subject.MediaFileFacts.Files[0].AudioStatus = api.MetadataEvidenceStatusUnavailable
	failures := languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_default_audio" && strings.Contains(f.Reason, "resource C:") && f.EvidenceStatus == api.MetadataEvidenceStatusPartial
	}) {
		t.Fatal("unavailable resource not unresolved")
	}
	subject.MediaFileFacts.Files[0].AudioStatus = api.MetadataEvidenceStatusContradictory
	subject.MediaFileFacts.Files[0].SubtitleStatus = api.MetadataEvidenceStatusContradictory
	facts := resourceLanguageFacts(subject, "C", nil)
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory || facts.SubtitleStatus != api.MetadataEvidenceStatusContradictory {
		t.Fatal("contradictory file states downgraded")
	}
}

func TestLSTResourceContradictoryEnglishCannotAuthorize(t *testing.T) {
	subject := resourceScopeSubject()
	subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusContradictory
	facts := resourceLanguageFacts(subject, "A", subject.LanguageFacts.Tracks[:2])
	if !resourceNeedsEnglishSubtitles(facts) {
		t.Fatal("contradictory English supplied positive coverage")
	}
	failures := languageAssessment(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_evidence" && strings.Contains(f.Reason, "resource A:") && f.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatal("contradictory coverage downgraded evidence")
	}
}

func TestLSTResourceSubtitleCorrectionSurvivesCanonicalProjection(t *testing.T) {
	for _, correction := range []struct {
		name       string
		languages  []string
		provenance api.FactProvenance
	}{
		{"replacement", []string{"French"}, api.FactProvenanceManual},
		{"clear", nil, api.FactProvenanceManualEmpty},
	} {
		t.Run(correction.name, func(t *testing.T) {
			media := api.MediaFacts{
				OriginalLanguage:            "Japanese",
				TrackCoverageComplete:       true,
				SubtitleLanguages:           correction.languages,
				SubtitleLanguagesProvenance: correction.provenance,
				Tracks: []api.MediaTrackFacts{
					{
						ID:         "a",
						ResourceID: "A",
						Kind:       api.MediaTrackAudio,
						Role:       api.AudioRoleProgramme,
						Languages:  []string{"Japanese"},
					},
					{
						ID:         "as",
						ResourceID: "A",
						Kind:       api.MediaTrackSubtitle,
						Languages:  []string{"English"},
					},
					{
						ID:         "b",
						ResourceID: "B",
						Kind:       api.MediaTrackAudio,
						Role:       api.AudioRoleProgramme,
						Languages:  []string{"Japanese"},
					},
					{
						ID:         "bs",
						ResourceID: "B",
						Kind:       api.MediaTrackSubtitle,
						Languages:  []string{"English"},
					},
				},
				MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{
					{
						ResourceID:      "A",
						Primary:         true,
						VideoTrackCount: 1,
						AudioStatus:     api.MetadataEvidenceStatusComplete,
						SubtitleStatus:  api.MetadataEvidenceStatusComplete,
					},
					{
						ResourceID:      "B",
						VideoTrackCount: 1,
						AudioStatus:     api.MetadataEvidenceStatusComplete,
						SubtitleStatus:  api.MetadataEvidenceStatusComplete,
					},
				}},
			}
			media.LanguageFacts = mediafacts.ResolveLanguages(media)
			files := mediafacts.ResolveFiles(media, "1080p")
			subject := api.NewTrackerValidationSubject(api.UploadSubject{
				Type:           "WEBDL",
				LanguageFacts:  media.LanguageFacts,
				MediaFileFacts: files,
			}, "LST")
			before := subject.LanguageFacts.Clone()
			key := trackers.LanguageQuestionKey(subject, "review")
			primary := resourceLanguageFacts(subject, "A", media.Tracks[:2])
			if !slices.Equal(primary.SubtitleLanguages, correction.languages) {
				t.Fatalf("corrected subtitles resurrected: %#v", primary)
			}
			sibling := resourceLanguageFacts(subject, "B", media.Tracks[2:])
			if !slices.Contains(sibling.SubtitleLanguages, "English") {
				t.Fatalf("primary correction leaked to sibling: %#v", sibling)
			}
			if correction.provenance == api.FactProvenanceManual && !titleSearchPolicy().Required(subject) {
				t.Fatal("corrected foreign primary did not request search")
			}
			if correction.provenance == api.FactProvenanceManualEmpty && primary.SubtitleStatus != api.MetadataEvidenceStatusPartial {
				t.Fatal("clear became inspected absence")
			}
			failures := languageAssessment(subject)
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_subtitles_search" && strings.Contains(f.Reason, "resource A:")
			}) {
				t.Fatalf("primary omission bypassed: %#v", failures)
			}
			if !reflect.DeepEqual(before, subject.LanguageFacts) || key != trackers.LanguageQuestionKey(subject, "review") {
				t.Fatal("canonical input mutated")
			}
		})
	}
}
