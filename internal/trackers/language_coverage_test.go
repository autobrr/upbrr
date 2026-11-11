// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProgrammeLanguageStatusSelectedResourceBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*api.TrackerValidationSubject)
		want   api.MetadataEvidenceStatus
	}{
		{"known selected report", nil, api.MetadataEvidenceStatusComplete},
		{"missing programme language", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Languages = nil }, api.MetadataEvidenceStatusPartial},
		{"unknown programme language", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Languages = []string{"und"} }, api.MetadataEvidenceStatusPartial},
		{"missing programme role", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = "" }, api.MetadataEvidenceStatusPartial},
		{"unknown programme role", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].Role = "unknown" }, api.MetadataEvidenceStatusPartial},
		{"cleared programme aggregate", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:         "Japanese",
				Tracks:                   s.LanguageFacts.Tracks,
				PrimaryAudioTrackID:      s.LanguageFacts.PrimaryAudioTrackID,
				AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
			})
		}, api.MetadataEvidenceStatusPartial},
		{"unknown aggregate", func(s *api.TrackerValidationSubject) { s.LanguageFacts.ProgrammeLanguages = []string{"und"} }, api.MetadataEvidenceStatusPartial},
		{"contradictory facts", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
		}, api.MetadataEvidenceStatusContradictory},
		{"aggregate mismatch", func(s *api.TrackerValidationSubject) { s.LanguageFacts.ProgrammeLanguages = []string{"English"} }, api.MetadataEvidenceStatusPartial},
		{"empty primary ID", func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "" }, api.MetadataEvidenceStatusPartial},
		{"missing primary", func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "missing" }, api.MetadataEvidenceStatusPartial},
		{"duplicate primary", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, s.LanguageFacts.Tracks[0])
		}, api.MetadataEvidenceStatusPartial},
		{"secondary primary", func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "commentary" }, api.MetadataEvidenceStatusPartial},
		{"missing primary resource", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].ResourceID = "" }, api.MetadataEvidenceStatusPartial},
		{"missing primary manifest", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].ManifestFingerprint = "" }, api.MetadataEvidenceStatusPartial},
		{"missing other resource", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ResourceID = "" }, api.MetadataEvidenceStatusPartial},
		{"mixed resources", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ResourceID = "other" }, api.MetadataEvidenceStatusPartial},
		{"missing other manifest", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ManifestFingerprint = "" }, api.MetadataEvidenceStatusPartial},
		{"mixed manifests", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[1].ManifestFingerprint = "other" }, api.MetadataEvidenceStatusPartial},
		{"commentary from another resource", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[3].ResourceID = "other" }, api.MetadataEvidenceStatusPartial},
		{"disc track", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].DiscID = "disc" }, api.MetadataEvidenceStatusPartial},
		{"playlist track", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].PlaylistID = "playlist" }, api.MetadataEvidenceStatusPartial},
		{"full disc", func(s *api.TrackerValidationSubject) { s.DiscType = "BDMV" }, api.MetadataEvidenceStatusPartial},
		{"disc type", func(s *api.TrackerValidationSubject) { s.Type = "DISC" }, api.MetadataEvidenceStatusPartial},
		{"single file", func(s *api.TrackerValidationSubject) { s.FileList = s.FileList[:1] }, api.MetadataEvidenceStatusPartial},
		{"no files", func(s *api.TrackerValidationSubject) { s.FileList = nil }, api.MetadataEvidenceStatusPartial},
		{"no selected file", func(s *api.TrackerValidationSubject) { s.VideoPath = "" }, api.MetadataEvidenceStatusPartial},
		{"selected file outside list", func(s *api.TrackerValidationSubject) { s.VideoPath = filepath.Join(s.SourcePath, "unlisted.mkv") }, api.MetadataEvidenceStatusPartial},
		{"complete coverage with partial evidence", func(s *api.TrackerValidationSubject) { s.LanguageFacts.TrackCoverageComplete = true }, api.MetadataEvidenceStatusPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := packProgrammeSubject(t)
			if test.change != nil {
				test.change(&subject)
			}
			before := subject.LanguageFacts.Clone()
			if got := trackers.ProgrammeLanguageStatus(subject); got != test.want {
				t.Fatalf("programme eligibility status = %s, want %s", got, test.want)
			}
			if !reflect.DeepEqual(subject.LanguageFacts, before) {
				t.Fatal("eligibility changed canonical language facts or coverage")
			}
		})
	}
}

func TestProgrammeLanguageStatusCommentaryDoesNotSupplyOriginal(t *testing.T) {
	subject := packProgrammeSubject(t)
	subject.LanguageFacts.OriginalLanguages = []string{"French"}
	for index := range subject.LanguageFacts.Tracks {
		if subject.LanguageFacts.Tracks[index].Role == api.AudioRoleProgramme {
			subject.LanguageFacts.Tracks[index].AudioOrigin = api.TrackAudioDubbed
		}
	}
	before := subject.LanguageFacts.Clone()
	if status := trackers.ProgrammeLanguageStatus(subject); status != api.MetadataEvidenceStatusComplete {
		t.Fatalf("known programme evidence needs review: %s", status)
	}
	failures := trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{OriginalAudio: trackers.LanguageProhibited})
	if len(failures) != 1 || failures[0].Rule != "language_original" || !api.IsStrictRuleFailure(failures[0]) {
		t.Fatalf("French commentary replaced missing original programme audio: %+v", failures)
	}
	if subject.LanguageFacts.HasOriginalAudio() || !slices.Equal(subject.LanguageFacts.ProgrammeLanguages, []string{"English", "Japanese"}) ||
		!reflect.DeepEqual(subject.LanguageFacts, before) {
		t.Fatal("commentary changed canonical programme audio or coverage")
	}
}

func packProgrammeSubject(t *testing.T) api.TrackerValidationSubject {
	t.Helper()
	subject := languageSubject("Japanese", "English", "Japanese")
	subject.SourcePath = t.TempDir()
	subject.VideoPath = filepath.Join(subject.SourcePath, "Example.S01E01.mkv")
	subject.FileList = []string{subject.VideoPath, filepath.Join(subject.SourcePath, "Example.S01E02.mkv")}
	subject.LanguageFacts.TrackCoverageComplete = false
	subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
	subject.LanguageFacts.AudioStatus = api.MetadataEvidenceStatusPartial
	subject.LanguageFacts.SubtitleStatus = api.MetadataEvidenceStatusPartial
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:        "commentary",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleCommentary,
		Languages: []string{"French"},
	})
	for index := range subject.LanguageFacts.Tracks {
		subject.LanguageFacts.Tracks[index].ResourceID = "selected-resource"
		subject.LanguageFacts.Tracks[index].ManifestFingerprint = "selected-report"
	}
	return subject
}

func TestPackProgrammeEligibilityPreservesIndependentRules(t *testing.T) {
	for _, test := range []struct {
		name, tracker, want string
		change              func(*api.TrackerValidationSubject)
	}{
		{"animated English dub", "HDB", "", func(s *api.TrackerValidationSubject) { s.Anime = true }},
		{"live action English dub", "HDB", "language_english_dub", func(s *api.TrackerValidationSubject) { s.EffectiveMetadata.Genres = []string{"Drama"} }},
		{"foreign programme dub", "HDB", "language_extra_dub", func(s *api.TrackerValidationSubject) {
			s.Anime = true
			s.LanguageFacts.Tracks[0].Languages = []string{"German"}
			s.LanguageFacts.ProgrammeLanguages = []string{"German", "Japanese"}
		}},
		{"original default", "CZ", "language_original_default", nil},
		{"remux manifest", "PTP", "language_remux_main_default", func(s *api.TrackerValidationSubject) { s.Type = "REMUX" }},
		{"subtitle coverage", "LUME", "language_subtitle_coverage", func(s *api.TrackerValidationSubject) { s.LanguageFacts.FullSubtitleLanguages = nil }},
		{"foreign flag evidence", "BTN", "language_primary_evidence", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := packProgrammeSubject(t)
			if test.change != nil {
				test.change(&subject)
			}
			before := subject.LanguageFacts.Clone()
			failures := languageFailures(t, test.tracker, subject)
			if slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_evidence" }) {
				t.Fatalf("collection coverage blocked inspected programme evidence: %+v", failures)
			}
			if test.want == "" {
				if len(nonAdvisoryFailures(failures)) != 0 {
					t.Fatalf("permitted programme audio blocked: %+v", failures)
				}
			} else if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == test.want && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
			}) {
				t.Fatalf("independent %s requirement was waived: %+v", test.want, failures)
			}
			if !reflect.DeepEqual(subject.LanguageFacts, before) {
				t.Fatal("tracker assessment changed canonical facts")
			}
		})
	}
}

func originalMultilingualSubject() api.TrackerValidationSubject {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "original",
		SubtitleLanguages:     []string{"English"},
		Tracks: []api.MediaTrackFacts{
			{
				ID:                  "original",
				ResourceID:          "resource",
				ManifestFingerprint: "manifest",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           []string{"mul"},
				AudioOrigin:         api.TrackAudioOriginal,
				Default:             true,
				DefaultKnown:        true,
				StreamOrderKnown:    true,
				Codec:               "AAC",
			},
			{Kind: api.MediaTrackSubtitle, Languages: []string{"English"}},
		},
	}
	return api.TrackerValidationSubject{Type: "ENCODE", LanguageFacts: mediafacts.ResolveLanguages(media)}
}

func TestOriginalMultilingualProgrammeAudioEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.TrackerValidationSubject)
		want   api.MetadataEvidenceStatus
	}{
		{"confirmed original", nil, api.MetadataEvidenceStatusComplete},
		{"unclassified soundtrack", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].AudioOrigin = "" }, api.MetadataEvidenceStatusPartial},
		{"confirmed dub", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].AudioOrigin = api.TrackAudioDubbed }, api.MetadataEvidenceStatusPartial},
		{"unknown language", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"und"}
			s.LanguageFacts.ProgrammeLanguages = []string{"und"}
		}, api.MetadataEvidenceStatusPartial},
		{"mixed unresolved languages", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Languages = []string{"mul", "und"}
			s.LanguageFacts.ProgrammeLanguages = []string{"mul", "und"}
		}, api.MetadataEvidenceStatusPartial},
		{"cleared aggregate", func(s *api.TrackerValidationSubject) { s.LanguageFacts.ProgrammeLanguages = nil }, api.MetadataEvidenceStatusPartial},
		{"contradictory aggregate", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
		}, api.MetadataEvidenceStatusContradictory},
		{"aggregate mismatch", func(s *api.TrackerValidationSubject) { s.LanguageFacts.ProgrammeLanguages = []string{"English"} }, api.MetadataEvidenceStatusPartial},
		{"missing role", func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = "" }, api.MetadataEvidenceStatusPartial},
		{"incomplete inspection", func(s *api.TrackerValidationSubject) { s.LanguageFacts.TrackCoverageComplete = false }, api.MetadataEvidenceStatusPartial},
		{"selected pack report", func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.TrackCoverageComplete = false
			s.SourcePath = t.TempDir()
			s.VideoPath = filepath.Join(s.SourcePath, "Example.S01E01.mkv")
			s.FileList = []string{s.VideoPath, filepath.Join(s.SourcePath, "Example.S01E02.mkv")}
		}, api.MetadataEvidenceStatusComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := originalMultilingualSubject()
			if test.change != nil {
				test.change(&subject)
			}
			before := subject.LanguageFacts.Clone()
			if got := trackers.ProgrammeAudioStatus(subject); got != test.want {
				t.Fatalf("soundtrack evidence = %s, want %s", got, test.want)
			}
			if trackers.ProgrammeLanguageStatus(subject) == api.MetadataEvidenceStatusComplete || !reflect.DeepEqual(subject.LanguageFacts, before) {
				t.Fatal("soundtrack eligibility established unknown language identities or changed canonical facts")
			}
		})
	}
}

func TestOriginalMultilingualTrackerRequirements(t *testing.T) {
	for _, test := range []struct{ tracker, want string }{
		{"HHD", ""}, {"ULCX", ""}, {"AZ", ""}, {"PTP", ""},
		{"AITHER", "language_multilingual_balance"},
		{"LUME", "language_track_language"},
		{"DP", "language_multilingual_evidence"},
		{"BTN", "language_evidence"},
	} {
		t.Run(test.tracker, func(t *testing.T) {
			failures := languageFailures(t, test.tracker, originalMultilingualSubject())
			if test.want == "" {
				if blocking := nonAdvisoryFailures(failures); len(blocking) != 0 {
					t.Fatalf("confirmed original multilingual audio blocked: %+v", blocking)
				}
			} else if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == test.want && api.IsStrictRuleFailure(f) }) {
				t.Fatalf("independent %s requirement was waived: %+v", test.want, failures)
			}
			if test.tracker == "LUME" {
				registry, err := impl.NewRegistry()
				if err != nil {
					t.Fatal(err)
				}
				readiness, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"LUME"}, api.UploadSubject{LanguageFacts: originalMultilingualSubject().LanguageFacts})
				if err != nil || !slices.ContainsFunc(readiness.Fields, func(field api.InputReadinessFieldOutcome) bool {
					return field.TrackID == "original" && field.CorrectionField != nil && *field.CorrectionField == api.CorrectionFieldMetadataTrackLanguages
				}) {
					t.Fatalf("LUME disclosure lost its correction prompt: %+v, %v", readiness, err)
				}
			}
		})
	}

	subject := originalMultilingualSubject()
	subject.LanguageFacts.SubtitleLanguages = nil
	subject.LanguageFacts.FullSubtitleLanguages = nil
	failures := trackers.EvaluateLanguagePolicy(subject, trackers.EnglishLanguagePolicy())
	if len(failures) != 1 || failures[0].Rule != "language_subtitles" || !api.IsStrictRuleFailure(failures[0]) {
		t.Fatalf("original multilingual audio waived required subtitles: %+v", failures)
	}
	failures = trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{EnglishSubtitles: "without_english", MissingSubtitles: trackers.LanguageProhibited})
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_evidence" && slices.Contains(f.LanguageCorrectionTrackIDs, "original")
	}) {
		t.Fatalf("unknown English coverage no longer requests evidence: %+v", failures)
	}
}
