// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProgrammeDubRemediationPreservesDecisions(t *testing.T) {
	for _, test := range []struct {
		name        string
		subject     api.TrackerValidationSubject
		mutate      func(*api.TrackerValidationSubject)
		outcome     trackers.LanguageOutcome
		want        string
		nonPersonal bool
	}{
		{
			name:        "other group retains original audio",
			subject:     languageSubject("Japanese", "Japanese", "English", "German"),
			want:        "Do not modify another group's release",
			nonPersonal: true,
		},
		{
			name:        "other group sole dub",
			subject:     languageSubject("Japanese", "German"),
			want:        "Do not modify another group's release",
			nonPersonal: true,
		},
		{
			name:    "original and English remain",
			subject: languageSubject("Japanese", "Japanese", "English", "German"),
			want:    "separate remux",
		},
		{
			name:    "English original remains",
			subject: languageSubject("English", "English", "German"),
			want:    "separate remux",
		},
		{
			name:    "sole programme audio",
			subject: languageSubject("Japanese", "German"),
			want:    "compliant source",
		},
		{
			name:    "original is missing",
			subject: languageSubject("Japanese", "English", "German"),
			want:    "compliant source",
		},
		{
			name:    "original shares the offending track",
			subject: languageSubject("Japanese", "Japanese", "German"),
			want:    "compliant source",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.Tracks[1].Languages = []string{"Japanese", "German"}
			},
		},
		{
			name:    "English shares the offending track",
			subject: languageSubject("Japanese", "Japanese", "English", "German"),
			want:    "compliant source",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.Tracks[2].Languages = []string{"English", "German"}
			},
		},
		{
			name:    "another file's original is insufficient",
			subject: languageSubject("Japanese", "Japanese", "German"),
			want:    "compliant source",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.Tracks[1].ResourceID = "other-episode"
			},
		},
		{
			name:    "later offending track lacks original audio",
			subject: languageSubject("Japanese", "Japanese", "German", "German"),
			want:    "compliant source",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.Tracks[2].ResourceID = "other-episode"
			},
		},
		{
			name:    "secondary audio cannot replace programme",
			subject: languageSubject("Japanese", "Japanese", "German"),
			want:    "compliant source",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.Tracks[0].Role = api.AudioRoleCommentary
			},
		},
		{
			name:    "partial evidence",
			subject: languageSubject("Japanese", "Japanese", "German"),
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
			},
		},
		{
			name:    "contradictory evidence",
			subject: languageSubject("Japanese", "Japanese", "German"),
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
			},
		},
		{
			name:    "staff permission remains strict",
			subject: languageSubject("Japanese", "Japanese", "German"),
			outcome: trackers.LanguageStaffException,
			want:    "separate remux",
		},
		{
			name:    "advisory unchanged",
			subject: languageSubject("Japanese", "Japanese", "German"),
			outcome: trackers.LanguageAdvisory,
		},
		{
			name:    "trumpable unchanged",
			subject: languageSubject("Japanese", "Japanese", "German"),
			outcome: trackers.LanguageTrumpable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := test.subject
			subject.Tracker = "EXAMPLE"
			subject.PersonalRelease = !test.nonPersonal
			if test.mutate != nil {
				test.mutate(&subject)
			}
			before := subject.LanguageFacts.Clone()
			outcome := test.outcome
			if outcome == "" {
				outcome = trackers.LanguageProhibited
			}
			failures := trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{ExtraDubs: outcome})
			found := false
			for _, failure := range failures {
				if failure.Rule != "language_extra_dub" {
					continue
				}
				found = true
				original := trackers.LanguageRuleFailure(subject, "extra_dub", "non-original, non-English programme dub: German", outcome)
				if test.want == "" {
					if failure != original {
						t.Fatalf("unrelated finding changed: %+v", failure)
					}
				} else {
					if !strings.Contains(failure.Reason, test.want) || !strings.Contains(failure.Reason, "prepare") || !strings.Contains(failure.Reason, "fresh") {
						t.Fatalf("missing safe remediation %q: %s", test.want, failure.Reason)
					}
					if test.want != "separate remux" && strings.Contains(failure.Reason, "separate remux") {
						t.Fatalf("unsafe track removal advice: %s", failure.Reason)
					}
					failure.Reason = original.Reason
					if failure != original {
						t.Fatalf("remediation changed decision: %+v", failure)
					}
				}
			}
			if !found || !reflect.DeepEqual(before, subject.LanguageFacts) {
				t.Fatal("extra-dub finding missing or prepared evidence mutated")
			}
		})
	}
}
