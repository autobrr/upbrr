// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func azIncompletePackSubject(t *testing.T, original string, audio ...string) api.UploadSubject {
	t.Helper()
	source := filepath.Join(t.TempDir(), "Example.Show.S01")
	selected := filepath.Join(source, "Example.Show.S01E01.mkv")
	facts := azTestLanguageFacts(original, audio...)
	for i := range facts.Tracks {
		facts.Tracks[i].ResourceID = "media_selected_episode"
		facts.Tracks[i].ManifestFingerprint = "selected_episode_manifest"
		facts.Tracks[i].StreamOrder = i + 1
		facts.Tracks[i].StreamOrderKnown = true
	}
	return api.UploadSubject{
		SourcePath: source,
		VideoPath:  selected,
		FileList:   []string{selected, filepath.Join(source, "Example.Show.S01E02.mkv")},
		Type:       "WEBDL",
		TVPack:     true,
		Identity:   api.ExternalIdentity{SourcePath: source, Generation: 1},
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      original,
			Tracks:                facts.Tracks,
			PrimaryAudioTrackID:   facts.PrimaryAudioTrackID,
			TrackCoverageComplete: false,
		}),
	}
}

func TestAZIncompletePackRegionalDialectUsesCanonicalEvidence(t *testing.T) {
	t.Parallel()
	meta := azIncompletePackSubject(t, "Chinese", "Cantonese")
	before := meta.LanguageFacts.Clone()
	if before.TrackCoverageComplete || before.ProgrammeStatus != api.MetadataEvidenceStatusPartial ||
		before.AudioStatus != api.MetadataEvidenceStatusPartial || before.SubtitleStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("fixture must retain incomplete canonical coverage: %+v", before)
	}
	subject := api.NewTrackerValidationSubject(meta, "AZ")
	key := trackers.LanguageQuestionKey(subject, "regional_dialect")
	if status := trackers.ProgrammeLanguageStatus(subject); status != api.MetadataEvidenceStatusComplete {
		t.Fatalf("inspected programme status = %q", status)
	}
	for _, answer := range []string{"", "no", "yes"} {
		meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"AZ": {key: answer}}
		question := New("AZ").ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
		if question == nil || len(question.Fields) != 1 {
			t.Fatalf("incomplete pack did not offer the regional-dialect question: %+v", question)
		}
		field := question.Fields[0]
		if field.Key != key || field.Value != answer || !field.Required {
			t.Fatalf("dialect question lost its canonical answer binding: %+v", field)
		}
		subject = api.NewTrackerValidationSubject(meta, "AZ")
		failures := evaluateAZLanguageRules(siteFor("AZ"), subject)
		if answer == "yes" {
			if len(failures) != 0 {
				t.Fatalf("canonical dialect answer was not consumed: %+v", failures)
			}
		} else if len(failures) != 1 || failures[0].Rule != "language_regional_dialect" ||
			!trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeNormal, true) {
			t.Fatalf("answer %q did not preserve the dialect gate: %+v", answer, failures)
		}
		if trackers.LanguageQuestionKey(subject, "regional_dialect") != key ||
			!reflect.DeepEqual(subject.LanguageFacts, before) || !reflect.DeepEqual(meta.LanguageFacts, before) {
			t.Fatal("eligibility or questionnaire assessment changed canonical language evidence")
		}
	}
}

func TestCinemaZIncompletePackOriginalDefaultRemainsStrict(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		original     bool
		defaultKnown bool
		isDefault    bool
		wantRule     string
		wantStatus   api.MetadataEvidenceStatus
	}{
		{
			name:         "original second and default",
			original:     true,
			defaultKnown: true,
			isDefault:    true,
		},
		{
			name:         "English default",
			original:     true,
			defaultKnown: true,
			wantRule:     "language_original_default",
			wantStatus:   api.MetadataEvidenceStatusComplete,
		},
		{
			name:       "unknown original default",
			original:   true,
			wantRule:   "language_original_default",
			wantStatus: api.MetadataEvidenceStatusPartial,
		},
		{
			name:       "dubbed only",
			wantRule:   "language_original",
			wantStatus: api.MetadataEvidenceStatusComplete,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			audio := []string{"English"}
			if test.original {
				audio = append(audio, "Japanese")
			}
			meta := azIncompletePackSubject(t, "Japanese", audio...)
			if test.original {
				meta.LanguageFacts.Tracks[0].Default = !test.isDefault
				meta.LanguageFacts.Tracks[1].Default = test.isDefault
				meta.LanguageFacts.Tracks[1].DefaultKnown = test.defaultKnown
			}
			subject := api.NewTrackerValidationSubject(meta, "CZ")
			before := subject.LanguageFacts.Clone()
			if trackers.ProgrammeLanguageStatus(subject) != api.MetadataEvidenceStatusComplete {
				t.Fatal("fixture does not establish inspected programme evidence")
			}
			failures := evaluateAZLanguageRules(siteFor("CZ"), subject)
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("original second and default was blocked: %+v", failures)
				}
			} else if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == test.wantRule && f.EvidenceStatus == test.wantStatus &&
					f.Disposition == api.RuleDispositionStrict &&
					trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
			}) {
				t.Fatalf("CinemaZ original/default restriction changed: %+v", failures)
			}
			if slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_evidence" }) {
				t.Fatalf("pack coverage still blocks inspected programme evidence: %+v", failures)
			}
			if before.TrackCoverageComplete || before.ProgrammeStatus != api.MetadataEvidenceStatusPartial ||
				before.AudioStatus != api.MetadataEvidenceStatusPartial || before.SubtitleStatus != api.MetadataEvidenceStatusPartial ||
				!reflect.DeepEqual(subject.LanguageFacts, before) || !reflect.DeepEqual(meta.LanguageFacts, before) {
				t.Fatal("CinemaZ eligibility assessment changed canonical language evidence")
			}
		})
	}
}
