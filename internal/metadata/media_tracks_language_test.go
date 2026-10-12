// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInspectedAudioLanguagesDriveTrackerEligibility(t *testing.T) {
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, title, language, originalFlag, commentaryLanguage string
		wantRole                                                api.AudioTrackRole
		wantStatus                                              api.MetadataEvidenceStatus
		wantOriginal                                            bool
	}{
		{"original title", "Original", "jpn", "", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusComplete, true},
		{"original flag", "Original", "jpn", "Yes", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusComplete, true},
		{"empty title", "", "jpn", "", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusComplete, true},
		{"codec title", "FLAC 2.0 [GRP]", "jpn", "No", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusComplete, true},
		{"unrelated commentary", "", "jpn", "", "fra", api.AudioRoleProgramme, api.MetadataEvidenceStatusComplete, true},
		{"different language", "Original", "deu", "Yes", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusComplete, false},
		{"missing language", "Original", "", "Yes", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusPartial, false},
		{"unknown language", "Original", "und", "Yes", "eng", api.AudioRoleProgramme, api.MetadataEvidenceStatusPartial, false},
		{"commentary", "Original commentary", "jpn", "Yes", "eng", api.AudioRoleCommentary, api.MetadataEvidenceStatusComplete, false},
		{"description", "Original audio description", "jpn", "Yes", "eng", api.AudioRoleDescription, api.MetadataEvidenceStatusComplete, false},
		{"compatibility", "Original compatibility", "jpn", "Yes", "eng", api.AudioRoleCompatibility, api.MetadataEvidenceStatusComplete, false},
		{"score", "Original isolated score", "jpn", "Yes", "eng", api.AudioRoleIsolatedScore, api.MetadataEvidenceStatusComplete, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var doc mediaInfoDoc
			doc.Media.Track = []map[string]any{
				{
					"@type":       "Audio",
					"ID":          "1",
					"StreamOrder": "1",
					"Language":    "eng",
					"Format":      "FLAC",
					"Channels":    "6",
					"Title":       "FLAC 5.1 [GRP]",
					"Default":     "Yes",
				},
				{
					"@type":       "Audio",
					"ID":          "2",
					"StreamOrder": "2",
					"Language":    test.language,
					"Format":      "FLAC",
					"Channels":    "2",
					"Title":       test.title,
					"Default":     "No",
					"Original":    test.originalFlag,
				},
				{
					"@type":       "Audio",
					"ID":          "3",
					"StreamOrder": "3",
					"Language":    test.commentaryLanguage,
					"Format":      "FLAC",
					"Channels":    "2",
					"Title":       "Commentary - FLAC 2.0 [GRP]",
					"Default":     "No",
				},
			}
			tracks, primary, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			if tracks[1].Role != test.wantRole || tracks[2].Role != api.AudioRoleCommentary || primary != tracks[0].ID {
				t.Fatalf("roles or primary changed: second=%q third=%q primary=%q", tracks[1].Role, tracks[2].Role, primary)
			}
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "Japanese",
				TrackCoverageComplete: true,
				Tracks:                tracks,
				PrimaryAudioTrackID:   primary,
			})
			if facts.ProgrammeStatus != test.wantStatus || facts.HasOriginalAudio() != test.wantOriginal {
				t.Fatalf("programme=%v status=%s original=%v", facts.ProgrammeLanguages, facts.ProgrammeStatus, facts.HasOriginalAudio())
			}
			for _, tracker := range []string{"AITHER", "BHD", "LST"} {
				failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, tracker, api.TrackerValidationSubject{
					Tracker:       tracker,
					Type:          "ENCODE",
					Anime:         true,
					LanguageFacts: facts,
				}, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				if got := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_evidence" }); got != (test.wantStatus != api.MetadataEvidenceStatusComplete) {
					t.Fatalf("%s evidence gate=%v, status=%s", tracker, got, facts.ProgrammeStatus)
				}
				if tracker != "LST" && test.wantStatus == api.MetadataEvidenceStatusComplete && !test.wantOriginal &&
					!slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_original" }) {
					t.Fatalf("%s accepted secondary or mismatched language as original", tracker)
				}
				if test.wantOriginal {
					for _, failure := range failures {
						if strings.HasPrefix(failure.Rule, "language_") && trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false) {
							t.Fatalf("%s blocked ordinary Japanese track: %+v", tracker, failure)
						}
					}
				}
			}
		})
	}
}
