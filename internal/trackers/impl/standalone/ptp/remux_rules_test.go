// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func ptpRemuxSubject(subject api.TrackerValidationSubject) api.TrackerValidationSubject {
	subject.Type, subject.DiscType = "REMUX", "BDMV"
	return subject
}

func TestPTPAnimeRemuxProgrammeBoundary(t *testing.T) {
	for _, test := range []struct {
		name      string
		languages []string
		role      api.AudioTrackRole
		wantIssue bool
	}{
		{name: "Japanese and English", languages: []string{"Japanese", "English"}},
		{name: "Japanese only", languages: []string{"Japanese"}},
		{name: "English only", languages: []string{"English"}},
		{
			name:      "duplicate Japanese",
			languages: []string{"Japanese", "Japanese", "English"},
			wantIssue: true,
		},
		{
			name:      "duplicate English",
			languages: []string{"Japanese", "English", "English"},
			wantIssue: true,
		},
		{
			name:      "extra German",
			languages: []string{"Japanese", "English", "German"},
			wantIssue: true,
		},
		{
			name:      "Japanese and German",
			languages: []string{"Japanese", "German"},
			wantIssue: true,
		},
		{
			name:      "alternate Japanese mix still counts",
			languages: []string{"Japanese", "English", "Japanese"},
			role:      api.AudioRoleAlternateMix,
			wantIssue: true,
		},
		{
			name:      "commentary excluded",
			languages: []string{"Japanese", "English", "Japanese"},
			role:      api.AudioRoleCommentary,
		},
		{
			name:      "compatibility excluded",
			languages: []string{"Japanese", "English", "Japanese"},
			role:      api.AudioRoleCompatibility,
		},
		{
			name:      "isolated score excluded",
			languages: []string{"Japanese", "English", "Japanese"},
			role:      api.AudioRoleIsolatedScore,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ptpLanguageSubject("Japanese", test.languages...)
			subject.Anime = true
			if test.role != "" {
				subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].Role = test.role
			}
			subject = ptpRemuxSubject(subject)
			failures := ptpNonAdvisoryFailures(subject)
			found := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_anime_remux_programme" })
			if found != test.wantIssue {
				t.Fatalf("anime programme issue = %t, want %t: %#v", found, test.wantIssue, failures)
			}
			if !test.wantIssue && len(failures) != 0 {
				t.Fatalf("permitted programme set blocked: %#v", failures)
			}
		})
	}
}

func TestPTPRemuxMissingPrimaryDefaultAndProgrammeEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.TrackerValidationSubject)
	}{
		{name: "unknown default", change: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].Default = false
			s.LanguageFacts.Tracks[0].DefaultKnown = false
		}},
		{name: "missing primary", change: func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "" }},
		{name: "secondary primary", change: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleCommentary }},
		{name: "incomplete manifest", change: func(s *api.TrackerValidationSubject) { s.LanguageFacts.TrackCoverageComplete = false }},
		{name: "unknown role", change: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[1].Role = ""
			s.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ptpLanguageSubject("Japanese", "Japanese", "English")
			subject.Anime = true
			test.change(&subject)
			subject = ptpRemuxSubject(subject)
			failures := ptpNonAdvisoryFailures(subject)
			requirePTPLanguageFailure(t, failures, "language_remux_main_default", api.RuleDispositionStrict)
		})
	}
}

func TestPTPRemuxBoundariesDoNotBroadenEligibility(t *testing.T) {
	for _, anime := range []bool{false, true} {
		for _, releaseType := range []string{"DISC", "ENCODE", "REMUX"} {
			subject := ptpLanguageSubject("Japanese", "Japanese", "English", "German")
			subject.Anime = anime
			subject = ptpRemuxSubject(subject)
			subject.Type = releaseType
			if releaseType == "ENCODE" {
				subject.DiscType = ""
			}
			failures := ptpNonAdvisoryFailures(subject)
			if anime && releaseType == "REMUX" {
				if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_anime_remux_programme" }) {
					t.Fatalf("remux missed anime boundary: %#v", failures)
				}
			} else if len(failures) != 0 {
				t.Fatalf("anime=%t type=%s acquired a new whitelist: %#v", anime, releaseType, failures)
			}
		}
	}
}

func TestPTPAnimeRemuxBoundarySurvivesSourceGuidance(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "English", "German")
	subject.Anime = true
	subject = ptpRemuxSubject(subject)
	ptpLanguageAnswer(&subject, "programme_track_purpose", "redundant")
	failures := ptpNonAdvisoryFailures(subject)
	requirePTPLanguageFailure(t, languageFailures(subject), "language_redundant_audio", api.RuleDispositionAdvisory)
	requirePTPLanguageFailure(t, failures, "language_anime_remux_programme", api.RuleDispositionWaivable)
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("PTP", slices.DeleteFunc(slices.Clone(failures), func(f api.RuleFailure) bool { return f.Rule == "language_anime_remux_programme" }))
	if err != nil {
		t.Fatal(err)
	}
	blocking, err := trackers.FirstBlockingRuleFailure("PTP", failures, api.WorkflowExecutionModeNormal, &api.TrackerReleaseProjection{
		WaivableRuleFingerprint: fingerprint, RuleAuthorizationFingerprint: fingerprint,
	})
	if err != nil || blocking == nil || blocking.Rule != "language_anime_remux_programme" {
		t.Fatalf("source guidance cleared the independent boundary: %#v, %v", blocking, err)
	}
}

func TestPTPAnimeRemuxAmbiguousProgrammeLanguage(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "English")
	subject.Anime = true
	subject.LanguageFacts.Tracks[1].Languages = []string{"English", "Japanese"}
	subject = ptpRemuxSubject(subject)
	requirePTPLanguageFailure(t, languageFailures(subject), "language_anime_remux_programme", api.RuleDispositionStrict)
}

func TestPTPRemuxWaiverBindingAndDebug(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "English", "German")
	subject.Anime = true
	for i := range subject.LanguageFacts.Tracks {
		subject.LanguageFacts.Tracks[i].StreamOrder = i + 1
		subject.LanguageFacts.Tracks[i].StreamOrderKnown = true
	}
	subject = ptpRemuxSubject(subject)
	failures := ptpRemuxLanguageFailures(subject)
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("PTP", failures)
	if err != nil {
		t.Fatal(err)
	}
	projection := &api.TrackerReleaseProjection{WaivableRuleFingerprint: fingerprint, RuleAuthorizationFingerprint: fingerprint}
	if blocking, err := trackers.FirstBlockingRuleFailure("PTP", failures, api.WorkflowExecutionModeNormal, projection); err != nil || blocking != nil {
		t.Fatalf("exact waiver: %+v %v", blocking, err)
	}
	for _, change := range []func(*api.TrackerValidationSubject){
		func(s *api.TrackerValidationSubject) { s.Identity.Generation++ },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[2].Languages = []string{"French"} },
	} {
		changed := subject
		changed.LanguageFacts = subject.LanguageFacts.Clone()
		change(&changed)
		changedFailures := ptpRemuxLanguageFailures(changed)
		if blocking, err := trackers.FirstBlockingRuleFailure("PTP", changedFailures, api.WorkflowExecutionModeNormal, projection); err != nil || blocking == nil {
			t.Fatalf("stale waiver: %+v %v", blocking, err)
		}
	}
	if blocking, err := trackers.FirstBlockingRuleFailure("PTP", failures, api.WorkflowExecutionModeDebug, nil); err != nil || blocking != nil {
		t.Fatalf("debug bypass: %+v %v", blocking, err)
	}
}

func TestPTPAnimeRemuxAmbiguityCannotDependOnTrackOrder(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "English", "German", "English")
	subject.Anime = true
	subject.LanguageFacts.Tracks[3].Languages = []string{"English", "Japanese"}
	for _, reverse := range []bool{false, true} {
		if reverse {
			slices.Reverse(subject.LanguageFacts.Tracks)
		}
		current := ptpRemuxSubject(subject)
		failures := ptpRemuxLanguageFailures(current)
		requirePTPLanguageFailure(t, failures, "language_anime_remux_programme", api.RuleDispositionStrict)
	}
}

func TestPTPNoDialogueRemuxStillAssessesMainDefault(t *testing.T) {
	subject := ptpRemuxSubject(ptpLanguageSubject("ZXX", "ZXX"))
	subject.Anime = true
	subject.LanguageFacts.Tracks[0].StreamOrder = 2
	subject.LanguageFacts.Tracks[0].StreamOrderKnown = true
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:               "subtitles",
		Kind:             api.MediaTrackSubtitle,
		Languages:        []string{"English"},
		StreamOrder:      1,
		StreamOrderKnown: true,
	})
	if failures := ptpNonAdvisoryFailures(subject); len(failures) != 0 {
		t.Fatalf("no-dialogue remux acquired an order requirement: %+v", failures)
	}
	subject.LanguageFacts.Tracks[0].Default = false
	requirePTPLanguageFailure(t, languageFailures(subject), "language_remux_main_default", api.RuleDispositionWaivable)
	subject.LanguageFacts.Tracks[0].DefaultKnown = false
	requirePTPLanguageFailure(t, languageFailures(subject), "language_remux_main_default", api.RuleDispositionStrict)
	subject.LanguageFacts.AudioAbsent = true
	subject.LanguageFacts.Tracks = subject.LanguageFacts.Tracks[1:]
	if failures := ptpNonAdvisoryFailures(subject); len(failures) != 0 {
		t.Fatalf("no audio acquired default-main requirement: %+v", failures)
	}
}

func TestPTPRemuxDefaultFlagEvidenceAndWaiver(t *testing.T) {
	for _, tc := range []struct {
		name         string
		value, known bool
		outcome      api.RuleDisposition
	}{
		{"known yes", true, true, ""},
		{"known no", false, true, api.RuleDispositionWaivable},
		{"missing flag", false, false, api.RuleDispositionStrict},
		{"legacy truthy without recognized flag", true, false, api.RuleDispositionStrict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := ptpLanguageSubject("Japanese", "Japanese", "English")
			subject.LanguageFacts.Tracks[0].Default, subject.LanguageFacts.Tracks[0].DefaultKnown = tc.value, tc.known
			for i := range subject.LanguageFacts.Tracks {
				subject.LanguageFacts.Tracks[i].StreamOrder = i + 1
				subject.LanguageFacts.Tracks[i].StreamOrderKnown = true
			}
			subject = ptpRemuxSubject(subject)
			failures := ptpRemuxLanguageFailures(subject)
			if tc.outcome == "" {
				if len(failures) != 0 {
					t.Fatalf("known default blocked: %+v", failures)
				}
				return
			}
			if len(failures) != 1 || failures[0].Disposition != tc.outcome {
				t.Fatalf("default evidence outcome: %+v", failures)
			}
			if tc.known && (!strings.Contains(failures[0].Reason, "Trumpable release") || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete) {
				t.Fatalf("known No lost: %+v", failures)
			}
			if !tc.known && failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
				t.Fatalf("unknown flag invented: %+v", failures)
			}
			fingerprint, err := trackers.WaivableRuleFailureFingerprint("PTP", failures)
			if err != nil {
				t.Fatal(err)
			}
			projection := &api.TrackerReleaseProjection{WaivableRuleFingerprint: fingerprint, RuleAuthorizationFingerprint: fingerprint}
			blocking, err := trackers.FirstBlockingRuleFailure("PTP", failures, api.WorkflowExecutionModeNormal, projection)
			if err != nil || (blocking == nil) != (tc.outcome == api.RuleDispositionWaivable) {
				t.Fatalf("waiver boundary: %+v %v", blocking, err)
			}
			if tc.known {
				subject.LanguageFacts.Tracks[0].DefaultKnown = false
				failures = ptpRemuxLanguageFailures(subject)
				if blocking, err := trackers.FirstBlockingRuleFailure("PTP", failures, api.WorkflowExecutionModeNormal, projection); err != nil || blocking == nil {
					t.Fatalf("known-flag approval cleared missing evidence: %+v %v", blocking, err)
				}
			}
		})
	}
}

func TestPTPKnownNonDefaultSurvivesOrderRelaxation(t *testing.T) {
	subject := ptpRemuxSubject(ptpLanguageSubject("Japanese", "Japanese", "English"))
	subject.LanguageFacts.Tracks[0].Default = false
	for _, orderKnown := range []bool{false, true} {
		for i := range subject.LanguageFacts.Tracks {
			subject.LanguageFacts.Tracks[i].StreamOrder = 2 - i
			subject.LanguageFacts.Tracks[i].StreamOrderKnown = orderKnown
		}
		ptpLanguageAnswer(&subject, "remux_track_order", "main_first")
		failures := ptpRemuxLanguageFailures(subject)
		requirePTPLanguageFailure(t, failures, "language_remux_main_default", api.RuleDispositionWaivable)
		if len(failures) != 1 {
			t.Fatalf("orderKnown=%t changed default finding: %+v", orderKnown, failures)
		}
	}
	subject.Type = "DISC"
	if got := languageFailures(subject); len(got) != 0 {
		t.Fatalf("full disc acquired flag requirement: %+v", got)
	}
}
