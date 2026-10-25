// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEnglishLanguageFallbackEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, original string
		audio          []string
		subtitles      []string
		wantRule       string
	}{
		{"foreign without English", "Japanese", []string{"Japanese"}, nil, "language_subtitles"},
		{"English subtitles", "Japanese", []string{"Japanese"}, []string{"English"}, ""},
		{"English dub", "Japanese", []string{"Japanese", "English"}, nil, ""},
		{"English original", "English", []string{"English"}, nil, ""},
		{"missing original", "Japanese", []string{"English"}, nil, "language_original"},
		{"confirmed extra dub", "Japanese", []string{"Japanese", "German"}, []string{"English"}, "language_extra_dub"},
		{"non-linguistic", "ZXX", []string{"ZXX"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := languageSubject(tc.original, tc.audio...)
			subject.LanguageFacts.SubtitleLanguages = tc.subtitles
			for _, tracker := range []string{"AITHER", "BLU", "PHD"} {
				t.Run(tracker, func(t *testing.T) {
					failures := languageFailures(t, tracker, subject)
					if tc.wantRule == "" {
						if len(failures) != 0 {
							t.Fatalf("%s unexpectedly blocked: %#v", tracker, failures)
						}
						return
					}
					index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == tc.wantRule })
					if index < 0 {
						t.Fatalf("%s missing %s: %#v", tracker, tc.wantRule, failures)
					}
					failure := failures[index]
					if !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, true) ||
						trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeDebug, false) {
						t.Fatalf("%s lost strict normal/debug behavior: %#v", tracker, failure)
					}
					if tracker != "AITHER" && (!strings.Contains(failure.Reason, "upbrr default") || !strings.Contains(failure.Reason, "prepare")) {
						t.Fatalf("%s fallback provenance/remediation missing: %s", tracker, failure.Reason)
					}
				})
			}
		})
	}
}

func TestEnglishLanguageFallbackDiscAndSubtitleBoundaries(t *testing.T) {
	for _, tracker := range []string{"BLU", "PHD"} {
		for _, tc := range []struct {
			releaseType, discType, source string
			blocked                       bool
		}{
			{"DISC", "", "BluRay", false},
			{"DISC", "BDMV", "BluRay", false},
			{"DISC", "DVD", "DVD", false},
			{"REMUX", "BDMV", "BluRay", true},
			{"ENCODE", "", "DVD", true},
		} {
			subject := languageSubject("Japanese", "Japanese")
			subject.Type, subject.DiscType, subject.Source = tc.releaseType, tc.discType, tc.source
			subject.LanguageFacts.SubtitleLanguages = nil
			if got := len(languageFailures(t, tracker, subject)) > 0; got != tc.blocked {
				t.Fatalf("%s %s/%s blocked=%t", tracker, tc.releaseType, tc.discType, got)
			}
		}
		for _, label := range []string{"English", "English (Forced)", "hardcoded English"} {
			subject := languageSubject("Japanese", "Japanese")
			media := api.MediaFacts{
				OriginalLanguage:      "Japanese",
				TrackCoverageComplete: true,
				Tracks:                subject.LanguageFacts.Tracks[:1],
				SubtitleLanguages:     []string{label},
			}
			if label == "hardcoded English" {
				media.SubtitleLanguages = nil
				media.HardcodedSubtitleLanguages = []string{"English"}
			}
			subject.LanguageFacts = mediafacts.ResolveLanguages(media)
			if failures := languageFailures(t, tracker, subject); len(failures) != 0 {
				t.Fatalf("%s changed existing %s evidence semantics: %#v", tracker, label, failures)
			}
		}
	}
}

func TestEnglishLanguageFallbackUsesSharedInputReview(t *testing.T) {
	registry := impl.MustNewRegistry()
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		SubtitleLanguages:     []string{"English"},
		Tracks: []api.MediaTrackFacts{{
			ID:                  "programme",
			ManifestFingerprint: "scan",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			Languages:           []string{"und"},
		}},
	}
	evaluate := func() api.InputReadinessEvaluation {
		t.Helper()
		result, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"AITHER", "BLU", "PHD", "BLU"}, api.UploadSubject{
			Type: "ENCODE", LanguageFacts: mediafacts.ResolveLanguages(media),
		})
		if err != nil {
			t.Fatal(err)
		}
		result.Fields = slices.DeleteFunc(result.Fields, func(field api.InputReadinessFieldOutcome) bool { return field.TrackID == "" })
		return result
	}
	result := evaluate()
	if len(result.Fields) != 2 {
		t.Fatalf("want one language and one origin correction: %#v", result.Fields)
	}
	for _, field := range result.Fields {
		if field.TrackID != "programme" || !slices.Equal(field.TrackerIDs, []api.TrackerID{"AITHER", "BLU", "PHD"}) {
			t.Fatalf("correction not shared across selected trackers: %#v", field)
		}
	}
	media.Tracks[0].Languages = []string{"Japanese", "German"}
	media.Tracks[0].AudioOrigin = api.TrackAudioOriginal
	for range 2 {
		if got := evaluate(); len(got.Fields) != 0 {
			t.Fatalf("corrected multilingual original requested again: %#v", got.Fields)
		}
	}
	media.Tracks[0].ManifestFingerprint = "changed-scan"
	media.Tracks[0].Languages, media.Tracks[0].AudioOrigin = []string{"mul"}, ""
	if got := evaluate(); len(got.Fields) != 2 {
		t.Fatalf("changed unresolved source not reviewed: %#v", got.Fields)
	}
}

func TestEnglishLanguageFallbackPreservesContainerAndSpecificPolicies(t *testing.T) {
	registry := impl.MustNewRegistry()
	subject := languageSubject("Japanese", "Japanese")
	subject.Tracker, subject.Container = "BLU", "avi"
	subject.LanguageFacts.SubtitleLanguages = nil
	failures, err := trackers.EvaluateTrackerValidationWithRegistry(context.Background(), registry, "BLU", subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"container", "language_subtitles"} {
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule && api.IsStrictRuleFailure(f) }) {
			t.Fatalf("BLU lost independent %s: %#v", rule, failures)
		}
	}
	for _, tracker := range []string{"AITHER", "BHD", "LST", "HHD", "ULCX", "LUME", "HDB", "BTN", "PTP", "NBL", "AZ", "CZ", "OE", "DP", "RAS", "RHD", "TOS", "TTR", "ANT"} {
		descriptor, ok := registry.LookupDescriptor(tracker)
		if !ok || strings.Contains(descriptor.Validation.ID, "english-fallback") {
			t.Fatalf("%s specific policy replaced: %#v", tracker, descriptor.Validation)
		}
	}
}
