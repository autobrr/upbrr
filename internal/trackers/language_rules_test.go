// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageSubject(original string, audio ...string) api.TrackerValidationSubject {
	media := api.MediaFacts{
		OriginalLanguage:      original,
		TrackCoverageComplete: true,
		SubtitleLanguages:     []string{"English"},
	}
	for i, language := range audio {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:               fmt.Sprintf("audio_%d", i),
			Kind:             api.MediaTrackAudio,
			Role:             api.AudioRoleProgramme,
			Title:            language + " main audio",
			StreamOrder:      i + 1,
			StreamOrderKnown: true,
			Languages:        []string{language},
			Default:          i == 0,
			DefaultKnown:     true,
			Codec:            "FLAC",
		})
	}
	media.Tracks = append(media.Tracks, api.MediaTrackFacts{
		Kind:             api.MediaTrackSubtitle,
		Title:            "English subtitles",
		StreamOrder:      len(audio) + 1,
		StreamOrderKnown: true,
		Languages:        []string{"English"},
		Default:          true,
		DefaultKnown:     true,
	})
	if len(audio) > 0 {
		media.PrimaryAudioTrackID = "audio_0"
	}
	return api.TrackerValidationSubject{LanguageFacts: mediafacts.ResolveLanguages(media), Type: "ENCODE"}
}

func languageFailures(t *testing.T, tracker string, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	subject.Tracker = tracker
	failures, err := trackers.EvaluateTrackerValidationWithRegistry(context.Background(), registry, tracker, subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(failures, func(failure api.RuleFailure) bool { return !strings.HasPrefix(failure.Rule, "language_") })
}

func TestTrackerProgrammeDubMatrix(t *testing.T) {
	for _, tt := range []struct {
		tracker, original string
		audio             []string
		want              string
	}{
		{"BHD", "Japanese", []string{"Japanese", "English"}, ""},
		{"BHD", "Japanese", []string{"Japanese", "English", "German"}, "Prohibited"},
		{"BHD", "Japanese", []string{"Japanese", "German"}, "Prohibited"},
		{"BHD", "English", []string{"English", "German"}, "Prohibited"},
		{"BHD", "Japanese", []string{"English"}, "Prohibited"},
		{"AITHER", "Japanese", []string{"Japanese", "English", "German"}, "Staff approval required"},
		{"HHD", "Japanese", []string{"English"}, "Prohibited"},
		{"ULCX", "Japanese", []string{"English"}, ""},
		{"LUME", "Japanese", []string{"Japanese", "English", "German"}, ""},
	} {
		t.Run(tt.tracker+"/"+tt.original+"/"+strings.Join(tt.audio, "+"), func(t *testing.T) {
			subject := languageSubject(tt.original, tt.audio...)
			if tt.tracker == "BHD" {
				subject.Tracker = "BHD"
				subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "existing_release"): "unchanged_or_new"}
			}
			failures := languageFailures(t, tt.tracker, subject)
			if tt.want == "" {
				if len(nonAdvisoryFailures(failures)) != 0 {
					t.Fatalf("unexpected blocking findings: %#v", failures)
				}
				return
			}
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return strings.HasPrefix(f.Reason, tt.want) && api.IsStrictRuleFailure(f)
			}) {
				t.Fatalf("want %s, got %#v", tt.want, failures)
			}
		})
	}
}

func TestLanguagePersonalAnimationAndDiscBoundaries(t *testing.T) {
	ulcx := languageSubject("Japanese", "English")
	ulcx.PersonalRelease = true
	if len(languageFailures(t, "ULCX", ulcx)) == 0 {
		t.Fatal("personal release omitted original audio")
	}

	for _, tracker := range []string{"BHD", "AITHER", "LST", "HHD", "ULCX", "LUME"} {
		for _, disc := range []string{"DVD", "BDMV", "Blu-Ray"} {
			subject := languageSubject("Japanese", "Japanese", "English", "German")
			subject.DiscType, subject.LanguageFacts.SubtitleLanguages = disc, nil
			if got := languageFailures(t, tracker, subject); len(got) != 0 {
				t.Fatalf("%s %s disc affected: %#v", tracker, disc, got)
			}
		}
	}
}

func TestStaffUploadTokenIsScopedSecretAndNotAuthorization(t *testing.T) {
	const secret = "synthetic-staff-credential"
	token := api.NewStaffUploadToken(secret)
	input := trackers.PreparationInput{Tracker: "AITHER", Meta: api.UploadSubject{StaffUploadTokens: map[string]api.StaffUploadToken{"AITHER": token}}}
	if input.StaffUploadToken().Secret() != secret {
		t.Fatal("credential not passed to selected tracker")
	}
	input.Tracker = "BHD"
	if input.StaffUploadToken().Secret() != "" {
		t.Fatal("credential crossed tracker scope")
	}
	encoded, err := json.Marshal(input.Meta)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(encoded), fmt.Sprintf("%v", input), fmt.Sprintf("%+v", input), fmt.Sprintf("%#v", input)} {
		if strings.Contains(output, secret) {
			t.Fatal("credential exposed")
		}
	}
	for _, failure := range languageFailures(t, "AITHER", languageSubject("Japanese", "Japanese", "German")) {
		if !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, true) {
			t.Fatal("staff block became waivable")
		}
		if trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeDebug, false) {
			t.Fatal("existing debug eligibility bypass lost")
		}
	}
}

func TestCanonicalDiscTypeAndRemuxBoundary(t *testing.T) {
	subject := languageSubject("Japanese", "Japanese", "German")
	subject.Type = "DISC"
	if failures := languageFailures(t, "BHD", subject); len(failures) > 0 {
		t.Fatalf("canonical DISC assessed: %#v", failures)
	}
	subject.Type, subject.DiscType = "REMUX", "BDMV"
	if failures := languageFailures(t, "BHD", subject); len(failures) == 0 {
		t.Fatal("disc-sourced remux exempted")
	}
}

func TestLanguageQuestionKeyInvalidatesChangedEvidence(t *testing.T) {
	subject := api.TrackerValidationSubject{
		Tracker:       "ULCX",
		SourcePath:    "synthetic.mkv",
		Identity:      api.ExternalIdentity{Generation: 1},
		LanguageFacts: api.LanguageFacts{ProgrammeLanguages: []string{"Japanese"}},
	}
	original := trackers.LanguageQuestionKey(subject, "retail")
	if trackers.LanguageQuestionKey(subject, "retail") != original {
		t.Fatal("unchanged evidence changed question")
	}
	for _, mutate := range []func(*api.TrackerValidationSubject){
		func(s *api.TrackerValidationSubject) { s.Tracker = "LST" },
		func(s *api.TrackerValidationSubject) { s.SourcePath = "other.mkv" },
		func(s *api.TrackerValidationSubject) { s.Source = "BluRay" },
		func(s *api.TrackerValidationSubject) { s.Type = "REMUX" },
		func(s *api.TrackerValidationSubject) { s.DiscType = "BDMV" },
		func(s *api.TrackerValidationSubject) { s.Identity.Generation++ },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.ProgrammeLanguages = []string{"English"} },
		func(s *api.TrackerValidationSubject) { s.PersonalRelease = true },
		func(s *api.TrackerValidationSubject) { s.Anime = true },
	} {
		changed := subject
		mutate(&changed)
		if trackers.LanguageQuestionKey(changed, "retail") == original {
			t.Fatalf("changed evidence retained question: %+v", changed)
		}
	}
}

func TestLanguageWaiverBindsPreparedEvidence(t *testing.T) {
	subject := languageSubject("Japanese", "Japanese", "English")
	subject.Tracker, subject.SourcePath, subject.Source = "PTP", "synthetic.mkv", "BluRay"
	subject.Identity.Generation = 1
	failure := trackers.LanguageRuleFailure(subject, "example", "same reported defect", trackers.LanguageTrumpable)
	if decoded, err := hex.DecodeString(string(failure.EvidenceFingerprint)); err != nil || len(decoded) != sha256.Size {
		t.Fatalf("invalid workflow fingerprint: %q", failure.EvidenceFingerprint)
	}
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("PTP", []api.RuleFailure{failure})
	if err != nil {
		t.Fatal(err)
	}
	projection := &api.TrackerReleaseProjection{WaivableRuleFingerprint: fingerprint, RuleAuthorizationFingerprint: fingerprint}
	if blocking, err := trackers.FirstBlockingRuleFailure("PTP", []api.RuleFailure{failure}, api.WorkflowExecutionModeNormal, projection); err != nil || blocking != nil {
		t.Fatalf("exact evidence rejected: %+v %v", blocking, err)
	}
	for _, change := range []func(*api.TrackerValidationSubject){
		func(s *api.TrackerValidationSubject) { s.Identity.Generation++ },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].StreamOrder++ },
		func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "audio_1" },
		func(s *api.TrackerValidationSubject) { s.Source = "WEB-DL" },
		func(s *api.TrackerValidationSubject) { s.Tracker = "LST" },
	} {
		changed := subject
		changed.LanguageFacts = subject.LanguageFacts.Clone()
		change(&changed)
		current := trackers.LanguageRuleFailure(changed, "example", "same reported defect", trackers.LanguageTrumpable)
		if blocking, err := trackers.FirstBlockingRuleFailure("PTP", []api.RuleFailure{current}, api.WorkflowExecutionModeNormal, projection); err != nil || blocking == nil {
			t.Fatalf("changed evidence reused acknowledgement: %+v %v", blocking, err)
		}
	}
}
