// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateInputReadinessPassesAndRemovesTrackerAnswers(t *testing.T) {
	t.Parallel()

	preparer := testPreparer()
	var subjects []api.UploadSubjectInput
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		subjects = append(subjects, input)
		return api.UploadSubject{
			SourcePath: input.Release.SourcePath,
			Source:     "bluray",
			Type:       "movie",
		}, nil
	}
	module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(readyInputReadinessEvaluator{}))
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: `C:\releases\Example.Release.2026`},
	})
	answer := "director"
	current = executeCommand(t, module, EvaluateInputReadinessCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{
			"ALPHA": {"edition": &answer},
		},
	})
	if current.InputReadiness == nil || len(subjects) != 1 || subjects[0].QuestionnaireAnswers["ALPHA"]["edition"] != "director" {
		t.Fatalf("input readiness subject = %#v, snapshot = %#v", subjects, current.InputReadiness)
	}

	current = executeCommand(t, module, EvaluateInputReadinessCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{
			"ALPHA": {"edition": nil},
		},
	})
	if len(subjects) != 2 {
		t.Fatalf("input readiness subjects = %#v", subjects)
	}
	if _, present := subjects[1].QuestionnaireAnswers["ALPHA"]["edition"]; present {
		t.Fatalf("removed tracker answer reached subject: %#v", subjects[1].QuestionnaireAnswers)
	}
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load tracker input state: %v", err)
	}
	if _, present := state.TrackerInputAnswers["ALPHA"]["edition"]; present {
		t.Fatalf("removed tracker answer persisted: %#v", state.TrackerInputAnswers)
	}
}

func TestEvaluateInputReadinessRetainsGlobalRequirements(t *testing.T) {
	t.Parallel()

	preparer := testPreparer()
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{SourcePath: input.Release.SourcePath}, nil
	}
	module, _ := newTestModule(t, preparer, WithInputReadinessEvaluator(readyInputReadinessEvaluator{}))
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: `C:\releases\Example.Release.2026`},
	})
	current = executeCommand(t, module, EvaluateInputReadinessCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
	})
	if current.InputReadiness == nil || inputReadinessGoalSatisfied(current.InputReadiness, current, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("tracker evaluator bypassed missing global inputs: %#v", current.InputReadiness)
	}
	for _, key := range []string{"source", "type"} {
		if !inputReadinessFieldMissing(current.InputReadiness.Fields, key) {
			t.Fatalf("missing global %s outcome: %#v", key, current.InputReadiness.Fields)
		}
	}
}

func TestEvaluateInputReadinessRejectsAnswersWithoutEvaluator(t *testing.T) {
	t.Parallel()

	module, repository := newTestModule(t, testPreparer())
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: `C:\releases\Example.Release.2026`},
	})
	answer := "director"
	_, err := module.Execute(t.Context(), testOwnerID, EvaluateInputReadinessCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{
			"ALPHA": {"edition": &answer},
		},
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("tracker answers without evaluator: %v", err)
	}
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load rejected tracker input state: %v", err)
	}
	if len(state.TrackerInputAnswers) != 0 || state.Workflow.InputReadiness != nil || state.Workflow.Revision != current.Workflow.Revision {
		t.Fatalf("rejected tracker answers changed persisted workflow: %#v", state.Workflow)
	}
}

type readyInputReadinessEvaluator struct{}

func (readyInputReadinessEvaluator) Requirements(_ context.Context, _ []api.TrackerID) (api.MetadataRequirementSet, error) {
	return api.MetadataRequirementSet{Version: "ready-input-readiness-v1"}, nil
}

func (readyInputReadinessEvaluator) Evaluate(
	_ context.Context,
	_ api.UploadSubject,
	trackerIDs []api.TrackerID,
) (api.InputReadinessEvaluation, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(trackerIDs)
	if err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("fingerprint ready input requirements: %w", err)
	}
	return api.InputReadinessEvaluation{
		RequirementsFingerprint: fingerprint,
		Schemas: []api.TrackerQuestionnaire{{
			Tracker: "ALPHA",
			Fields:  []api.TrackerQuestionnaireField{{Key: "edition"}},
		}},
		Fields: []api.InputReadinessFieldOutcome{{
			Key:         "source",
			Status:      api.InputReadinessFieldReady,
			Disposition: api.RuleDispositionStrict,
		}},
	}, nil
}

func inputReadinessFieldMissing(fields []api.InputReadinessFieldOutcome, key string) bool {
	for _, field := range fields {
		if field.Key == key && field.Status == api.InputReadinessFieldMissing {
			return true
		}
	}
	return false
}
