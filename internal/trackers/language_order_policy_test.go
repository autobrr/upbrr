// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerAudioOrderDoesNotReplaceOriginalPresence(t *testing.T) {
	for _, tracker := range []string{"AITHER", "BHD", "HHD", "BTN", "HDB"} {
		for _, releaseType := range []string{"ENCODE", "REMUX", "WEBDL"} {
			t.Run(tracker+"/"+releaseType, func(t *testing.T) {
				subject := languageSubject("Japanese", "English", "Japanese")
				subject.Type, subject.Anime = releaseType, true
				for _, originalDefault := range []bool{false, true} {
					subject.LanguageFacts.Tracks[0].Default = !originalDefault
					subject.LanguageFacts.Tracks[1].Default = originalDefault
					for _, failure := range languageFailures(t, tracker, subject) {
						if failure.Rule == "language_original_primary" || failure.Rule == "language_primary_evidence" {
							t.Fatalf("track order/default flags created original-primary gate: %+v", failure)
						}
					}
				}
				subject = languageSubject("Japanese", "English")
				subject.Type, subject.Anime = releaseType, true
				if (tracker == "BTN" || tracker == "HDB") && releaseType != "REMUX" {
					return
				}
				failures := languageFailures(t, tracker, subject)
				if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
					return f.Rule == "language_original" && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
				}) {
					t.Fatalf("missing original audio no longer blocks: %+v", failures)
				}
			})
		}
	}
}

func TestTrackerAudioOrderRecommendationsRemainNonBlocking(t *testing.T) {
	for _, tracker := range []string{"LST", "LUME"} {
		for _, personal := range []bool{false, true} {
			subject := languageSubject("Japanese", "English", "Japanese")
			subject.PersonalRelease = personal
			subject.LanguageFacts.Tracks[0].Default = false
			subject.LanguageFacts.Tracks[1].Default = true
			failures := languageFailures(t, tracker, subject)
			found := false
			for _, failure := range failures {
				if failure.Rule != "language_original_order" {
					continue
				}
				found = true
				for _, acknowledged := range []bool{false, true} {
					if failure.Disposition != api.RuleDispositionAdvisory || trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, acknowledged) {
						t.Fatalf("%s personal=%t ordering requires approval: %+v", tracker, personal, failure)
					}
				}
			}
			if !found {
				t.Fatalf("%s personal=%t omitted ordering warning", tracker, personal)
			}
		}
	}
}

func TestCinemaZDefaultAudioIsIndependentOfOrder(t *testing.T) {
	for _, originalFirst := range []bool{false, true} {
		audio := []string{"English", "Japanese"}
		originalIndex := 1
		if originalFirst {
			audio, originalIndex = []string{"Japanese", "English"}, 0
		}
		for _, originalDefault := range []bool{false, true} {
			subject := languageSubject("Japanese", audio...)
			subject.LanguageFacts.Tracks[originalIndex].Default = originalDefault
			subject.LanguageFacts.Tracks[1-originalIndex].Default = !originalDefault
			failures := languageFailures(t, "CZ", subject)
			blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_original_default" && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
			})
			if blocked == originalDefault {
				t.Fatalf("first=%t default=%t: %+v", originalFirst, originalDefault, failures)
			}
		}
	}
	failures := languageFailures(t, "CZ", languageSubject("Japanese", "English"))
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_original" && api.IsStrictRuleFailure(f) }) {
		t.Fatalf("dub-only upload was allowed: %+v", failures)
	}
}

func TestHDBDubRestrictionsRemainStrictRegardlessOfOrder(t *testing.T) {
	for _, releaseType := range []string{"ENCODE", "REMUX", "WEBDL"} {
		for _, test := range []struct {
			name   string
			anime  bool
			genres []string
			dub    string
			want   string
		}{
			{
				name:  "anime English",
				anime: true,
				dub:   "English",
			},
			{
				name:   "animation English",
				genres: []string{"Animation"},
				dub:    "English",
			},
			{
				name:   "live action English",
				genres: []string{"Drama"},
				dub:    "English",
				want:   "language_english_dub",
			},
			{
				name: "unknown animation",
				dub:  "English",
				want: "language_english_dub",
			},
			{
				name:  "anime foreign dub",
				anime: true,
				dub:   "German",
				want:  "language_extra_dub",
			},
		} {
			t.Run(releaseType+"/"+test.name, func(t *testing.T) {
				subject := languageSubject("Japanese", test.dub, "Japanese")
				subject.Type, subject.Anime = releaseType, test.anime
				subject.EffectiveMetadata.Genres = test.genres
				failures := languageFailures(t, "HDB", subject)
				if test.want == "" {
					if len(nonAdvisoryFailures(failures)) != 0 {
						t.Fatalf("permitted animated dub blocked: %+v", failures)
					}
					return
				}
				if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
					return f.Rule == test.want && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
				}) {
					t.Fatalf("dub restriction was waived: %+v", failures)
				}
			})
		}
	}
}
