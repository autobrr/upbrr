// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func retainTestUploadQuestionnaire(state *State, now time.Time) {
	w := &state.Workflow
	release := state.Releases[w.Release.ID]
	source := api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	w.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: "late-projection", Revision: w.Revision}
	w.Dupes = &api.DupeAssessmentRef{ID: "late-dupes", Revision: w.Revision}
	w.Media = &api.MediaArtifactSetRef{ID: "late-media", Revision: w.Revision}
	w.Descriptions = &api.DescriptionSetRef{ID: "late-description", Revision: w.Revision}
	w.DryRun = &api.UploadDryRunResultRef{ID: "late-dry-run", Revision: w.Revision}
	state.Projections[w.TrackerProjections.ID] = api.TrackerReleaseProjectionSet{
		ID:         w.TrackerProjections.ID,
		Revision:   w.Revision,
		Release:    *w.Release,
		ReleaseRef: source,
	}
	state.Media[w.Media.ID] = api.MediaArtifactSet{
		ID:            w.Media.ID,
		Revision:      w.Revision,
		Release:       *w.Release,
		ReleaseRef:    source,
		ProjectionSet: *w.TrackerProjections,
	}
	state.Descriptions[w.Descriptions.ID] = api.DescriptionSet{
		ID:            w.Descriptions.ID,
		Revision:      w.Revision,
		Release:       *w.Release,
		ReleaseRef:    source,
		ProjectionSet: *w.TrackerProjections,
		Media:         w.Media,
	}
	state.DryRuns[w.DryRun.ID] = api.UploadDryRunResult{
		ID:            w.DryRun.ID,
		WorkflowID:    w.ID,
		Revision:      w.Revision,
		ProjectionSet: *w.TrackerProjections,
		Dupes:         *w.Dupes,
		Media:         *w.Media,
		Descriptions:  *w.Descriptions,
		TrackerIDs:    []api.TrackerID{"ALPHA"},
		Reports: []api.TrackerDryRunReport{{TrackerID: "ALPHA", Questionnaire: []api.TrackerQuestionnaireRequirement{{
			Key:      "current_hosted_screenshots",
			Label:    "Inspect screenshots",
			Kind:     "select",
			Required: true,
			Options:  []string{"lossless_preserved", "lossy_or_changed"},
		}}}},
	}
	expires := now.Add(time.Minute)
	w.RequiredActions = []api.RequiredAction{{
		ID:               "late-action",
		Kind:             api.RequiredActionAnswerQuestionnaire,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: w.Revision,
		TrackerID:        "ALPHA",
		Prompt:           "Inspect hosted screenshots",
		CreatedAt:        now,
		ExpiresAt:        &expires,
	}}
}

func TestEvaluateInputReadinessAcceptsOnlyCurrentLateUploadQuestions(t *testing.T) {
	for _, test := range []struct {
		name     string
		change   func(*State)
		accepted bool
	}{
		{"current", func(*State) {}, true},
		{"changed media", func(s *State) { s.Workflow.Media.Revision++ }, false},
		{"changed source", func(s *State) { r := s.Releases[s.Workflow.Release.ID]; r.Release.Generation++; s.Releases[r.ID] = r }, false},
		{"changed description", func(s *State) { s.Workflow.Descriptions.Revision++ }, false},
		{"expired action", func(s *State) { expired := time.Time{}; s.Workflow.RequiredActions[0].ExpiresAt = &expired }, false},
		{"answered action", func(s *State) { s.Workflow.RequiredActions[0].Status = api.RequiredActionStatusResolved }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			preparer := testPreparer()
			var resolved api.UploadSubjectInput
			preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
				resolved = input
				return api.UploadSubject{
					SourcePath: input.Release.SourcePath,
					Source:     "WEB-DL",
					Type:       "WEBDL",
				}, nil
			}
			module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(readyInputReadinessEvaluator{}))
			current := executeCommand(t, module, CreateWorkflowCommand{})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "synthetic.mkv"},
			})
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			previous := state.Workflow.Revision
			state.Workflow.Revision++
			retainTestUploadQuestionnaire(&state, module.clock.Now())
			test.change(&state)
			if err := repository.Save(t.Context(), testOwnerID, previous, state); err != nil {
				t.Fatal(err)
			}
			answer := "lossless_preserved"
			result, err := module.Execute(t.Context(), testOwnerID, EvaluateInputReadinessCommand{
				WorkflowID:          current.Workflow.ID,
				ExpectedRevision:    state.Workflow.Revision,
				TrackerIDs:          []api.TrackerID{"ALPHA"},
				TrackerInputAnswers: map[api.TrackerID]map[string]*string{"ALPHA": {"current_hosted_screenshots": &answer}},
			})
			if !test.accepted {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("stale question accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.InputReadiness == nil || result.Workflow.DryRun != nil || result.Workflow.Media != nil || result.Workflow.TrackerProjections != nil {
				t.Fatalf("late answer did not invalidate downstream: %+v", result.Workflow)
			}
			if resolved.QuestionnaireAnswers["ALPHA"]["current_hosted_screenshots"] != answer {
				t.Fatal("accepted answer did not reach source-only resolver")
			}
			saved, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.TrackerInputAnswers["ALPHA"]["current_hosted_screenshots"] != answer {
				t.Fatal("late answer was not persisted for re-projection")
			}
		})
	}
}

func TestCurrentQuestionnaireWinsRetainedFieldConflict(t *testing.T) {
	answer := "old-option"
	patch := map[api.TrackerID]map[string]*string{"LST": {"same-key": &answer}}
	schemas := []api.TrackerQuestionnaire{
		{Tracker: "LST", Fields: []api.TrackerQuestionnaireField{{Key: "same-key", Options: []string{"current-option"}}}},
		{Tracker: "LST", Fields: []api.TrackerQuestionnaireField{{Key: "same-key", Options: []string{"old-option"}}}},
	}
	if err := validateTrackerInputPatch(patch, []api.TrackerID{"LST"}, schemas, nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("retained field overrode current semantics: %v", err)
	}
}
