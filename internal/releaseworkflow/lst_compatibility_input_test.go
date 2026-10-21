// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTInputReadinessAcceptsResolvedCompatibilityCorrection(t *testing.T) {
	for _, previous := range []string{"unresolved", "not_compatibility"} {
		t.Run(previous, func(t *testing.T) {
			base := api.UploadSubject{
				SourcePath: filepath.Join(t.TempDir(), "Example.mkv"),
				Type:       "REMUX",
				Source:     "BluRay",
				Identity:   api.ExternalIdentity{Generation: 1},
				LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
					OriginalLanguage:      "English",
					TrackCoverageComplete: true,
					PrimaryAudioTrackID:   "main",
					Tracks: []api.MediaTrackFacts{
						{
							ID:         "main",
							ResourceID: "media",
							Kind:       api.MediaTrackAudio,
							Role:       api.AudioRoleProgramme,
							Codec:      "TrueHD",
							Languages:  []string{"English"},
						},
						{
							ID:         "companion",
							ResourceID: "media",
							Kind:       api.MediaTrackAudio,
							Role:       api.AudioRoleProgramme,
							Codec:      "AC-3",
							Languages:  []string{"English"},
						},
					},
				}),
			}
			preparer := testPreparer()
			preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
				subject := base
				subject.SourcePath = input.Release.SourcePath
				subject.Identity.Generation = input.Release.Generation
				subject.TrackerQuestionnaireAnswers = input.QuestionnaireAnswers
				return subject, nil
			}
			registry := trackerimpl.MustNewRegistry()
			module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(registryQuestionnaireEvaluator{registry: registry}))
			current := executeCommand(t, module, CreateWorkflowCommand{})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: base.SourcePath},
			})
			key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(base, "LST"), "compatibility_mix_companion")
			for _, answer := range []string{previous, "main"} {
				current = executeCommand(t, module, EvaluateInputReadinessCommand{
					WorkflowID:          current.Workflow.ID,
					ExpectedRevision:    current.Workflow.Revision,
					TrackerIDs:          []api.TrackerID{"LST"},
					TrackerInputAnswers: map[api.TrackerID]map[string]*string{"LST": {key: new(answer)}},
				})
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := state.TrackerInputAnswers["LST"][key]; got != "main" {
				t.Fatalf("resolved answer not persisted: %q", got)
			}
			base.TrackerQuestionnaireAnswers = cloneTrackerInputAnswers(state.TrackerInputAnswers)
			evaluation, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"LST"}, base)
			if err != nil {
				t.Fatal(err)
			}
			for _, schema := range evaluation.TrackerQuestionnaires {
				for _, field := range schema.Fields {
					if field.Key == key {
						if field.Required || field.NeedsReview || field.Value != "main" {
							t.Fatalf("resolved answer still requires review: %+v", field)
						}
						return
					}
				}
			}
			t.Fatal("resolved answer missing from post-answer schema")
		})
	}
}
