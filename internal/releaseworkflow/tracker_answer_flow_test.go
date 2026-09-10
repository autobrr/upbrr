// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerInputAnswersFlowIntoProjectionAndPreflight(t *testing.T) {
	t.Parallel()

	preparer := testPreparer()
	var subjectInputs []api.UploadSubjectInput
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		subjectInputs = append(subjectInputs, input)
		return api.UploadSubject{
			SourcePath:                  input.Release.SourcePath,
			Trackers:                    append([]string(nil), input.Trackers...),
			TrackerQuestionnaireAnswers: input.QuestionnaireAnswers,
			Source:                      "bluray",
			Type:                        "movie",
		}, nil
	}

	var projectionSubjects, preflightSubjects []api.UploadSubject
	projector := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		subject api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		_ map[api.TrackerID]api.WorkflowFingerprint,
		executionMode api.WorkflowExecutionMode,
	) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		projectionSubjects = append(projectionSubjects, subject)
		return api.TrackerCatalogSnapshot{
				CatalogVersion: "tracker-answer-flow-v1",
				Trackers: []api.TrackerCatalogDescriptor{{
					TrackerID:         "PTP",
					DisplayName:       "PTP",
					ProjectorVersion:  "v1",
					PolicyFingerprint: testFingerprint(t, "ptp-policy"),
				}},
			}, api.TrackerRuntimeSnapshot{
				RuntimeGeneration: "tracker-answer-flow-v1",
				Trackers: []api.TrackerRuntimeEntry{{
					TrackerID:         "PTP",
					Configured:        true,
					ConfigFingerprint: testFingerprint(t, "ptp-config"),
				}},
			}, api.TrackerSelection{TrackerIDs: trackerIDs}, api.TrackerReleaseProjectionSet{
				InputFingerprint:  testFingerprint(t, "ptp-projection-input"),
				PolicyFingerprint: testFingerprint(t, "ptp-projection-policy"),
				ExecutionMode:     executionMode,
				Projections:       []api.TrackerReleaseProjection{testProjection(t, "PTP", "Example.Release.2026.PTP-GRP")},
				Status:            api.StageStatusReady,
			}, nil
	})
	readyPreflight := readyPreflightBuilder(t)
	preflight := trackerPreflightBuilderFunc(func(
		ctx context.Context,
		subject api.UploadSubject,
		catalog api.TrackerCatalogSnapshot,
		runtime api.TrackerRuntimeSnapshot,
		projections api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		preflightSubjects = append(preflightSubjects, subject)
		return readyPreflight.Build(ctx, subject, catalog, runtime, projections, now)
	})
	module, repository := newTestModule(
		t,
		preparer,
		WithClock(&selectionEnrichmentClock{}),
		WithInputReadinessEvaluator(trackerAnswerFlowEvaluator{}),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(preflight),
	)

	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: `C:\releases\Example.Release.2026`},
		TrackerIDs:       []api.TrackerID{"PTP"},
	})
	yes := "yes"
	current = executeCommand(t, module, EvaluateInputReadinessCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"PTP"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{
			"ptp": {"no_english_subtitles": &yes},
		},
	})
	current = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"PTP"},
	})
	current = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
	})

	assertTrackerAnswer(t, subjectInputs[1].QuestionnaireAnswers)
	assertTrackerAnswer(t, subjectInputs[2].QuestionnaireAnswers)
	assertTrackerAnswer(t, projectionSubjects[0].TrackerQuestionnaireAnswers)
	assertTrackerAnswer(t, preflightSubjects[0].TrackerQuestionnaireAnswers)

	current = executeCommand(t, module, EvaluateInputReadinessCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"PTP"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{
			"ptp": {"no_english_subtitles": nil},
		},
	})
	if current.Workflow.TrackerProjections != nil || current.Workflow.TrackerPreflight != nil {
		t.Fatalf("automatic tracker answer reset did not invalidate downstream stages: %#v", current.Workflow)
	}
	current = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"PTP"},
	})
	current = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
	})

	assertTrackerAnswerAbsent(t, subjectInputs[4].QuestionnaireAnswers)
	assertTrackerAnswerAbsent(t, subjectInputs[5].QuestionnaireAnswers)
	assertTrackerAnswerAbsent(t, projectionSubjects[1].TrackerQuestionnaireAnswers)
	assertTrackerAnswerAbsent(t, preflightSubjects[1].TrackerQuestionnaireAnswers)

	current = continueTrackerInputPatch(t, module, current, "yes")
	if current.Workflow.TrackerProjections != nil || current.Workflow.TrackerPreflight != nil {
		t.Fatalf("Continue tracker answer did not invalidate downstream stages: %#v", current.Workflow)
	}
	assertPersistedTrackerAnswer(t, repository, current.Workflow.ID)

	revision := current.Workflow.Revision
	current = continueTrackerInputPatch(t, module, current, "yes")
	if current.Workflow.Revision != revision {
		t.Fatalf("repeated tracker answer changed workflow revision from %d to %d", revision, current.Workflow.Revision)
	}

	current = continueTrackerInputPatch(t, module, current, "")
	assertPersistedTrackerAnswerAbsent(t, repository, current.Workflow.ID)

	revision = current.Workflow.Revision
	current = continueTrackerInputPatch(t, module, current, "")
	if current.Workflow.Revision != revision {
		t.Fatalf("repeated Auto tracker answer changed workflow revision from %d to %d", revision, current.Workflow.Revision)
	}
}

func continueTrackerInputPatch(
	t *testing.T,
	module *Module,
	current CommandResult,
	answer string,
) CommandResult {
	t.Helper()
	var value *string
	idempotencyKey := "continue-tracker-answer-auto"
	if answer != "" {
		value = &answer
		idempotencyKey = "continue-tracker-answer-" + answer
	}
	updated, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
		},
		IdempotencyKey: idempotencyKey,
		Goal:           api.WorkflowGoalInputReady,
		Intent: api.WorkflowIntent{
			TrackerIDs: []api.TrackerID{"PTP"},
			TrackerInputAnswers: map[api.TrackerID]map[string]*string{
				"ptp": {"no_english_subtitles": value},
			},
		},
	})
	if err != nil {
		t.Fatalf("continue tracker input answer at revision %d: %v", current.Workflow.Revision, err)
	}
	started := updated.Operation != nil && updated.Operation.Revision == current.Workflow.Revision
	if started {
		status := *updated.Operation
		if !isTerminalProgressStatus(status.Status) {
			status = waitForWorkflowOperation(t, module, updated.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool {
				return isTerminalProgressStatus(status.Status)
			})
		}
		if status.Status != api.StageStatusCompleted {
			t.Fatalf("continue tracker input operation = %#v", status)
		}
	}
	if started {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			updated, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatalf("load completed tracker input answer: %v", err)
			}
			if updated.Workflow.Revision > current.Workflow.Revision {
				return updated
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("completed tracker input answer did not advance workflow revision")
	}
	updated, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load continued tracker input answer: %v", err)
	}
	return updated
}

func assertPersistedTrackerAnswer(t *testing.T, repository *MemoryRepository, workflowID api.WorkflowID) {
	t.Helper()
	state, err := repository.Load(t.Context(), testOwnerID, workflowID)
	if err != nil {
		t.Fatalf("load tracker input state: %v", err)
	}
	if got := state.TrackerInputAnswers["PTP"]["no_english_subtitles"]; got != "yes" {
		t.Fatalf("persisted PTP no_english_subtitles = %q, want yes in %#v", got, state.TrackerInputAnswers)
	}
}

func assertPersistedTrackerAnswerAbsent(t *testing.T, repository *MemoryRepository, workflowID api.WorkflowID) {
	t.Helper()
	state, err := repository.Load(t.Context(), testOwnerID, workflowID)
	if err != nil {
		t.Fatalf("load tracker input state: %v", err)
	}
	if _, found := state.TrackerInputAnswers["PTP"]["no_english_subtitles"]; found {
		t.Fatalf("stale persisted PTP no_english_subtitles = %#v", state.TrackerInputAnswers)
	}
}

type trackerAnswerFlowEvaluator struct{}

func TestNormalizeTrackerInputAnswers(t *testing.T) {
	t.Parallel()

	yes := "yes"
	patch := map[api.TrackerID]map[string]*string{
		" ptp ": {"no_english_subtitles": &yes},
	}
	normalized, err := normalizeTrackerInputAnswers(patch)
	if err != nil {
		t.Fatalf("normalize tracker input answers: %v", err)
	}
	delete(patch[" ptp "], "no_english_subtitles")
	if got := normalized["PTP"]["no_english_subtitles"]; got == nil || *got != yes {
		t.Fatalf("normalized tracker input answers = %#v", normalized)
	}

	_, err = normalizeTrackerInputAnswers(map[api.TrackerID]map[string]*string{
		"ptp": {"no_english_subtitles": &yes},
		"PTP": {"no_english_subtitles": &yes},
	})
	if err == nil {
		t.Fatal("case-folding tracker answer collision was accepted")
	}
}

func (trackerAnswerFlowEvaluator) Requirements(
	_ context.Context,
	_ []api.TrackerID,
) (api.MetadataRequirementSet, error) {
	return api.MetadataRequirementSet{Version: "tracker-answer-flow-v1"}, nil
}

func (trackerAnswerFlowEvaluator) Evaluate(
	_ context.Context,
	_ api.UploadSubject,
	trackerIDs []api.TrackerID,
) (api.InputReadinessEvaluation, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(trackerIDs)
	if err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("fingerprint tracker input answers: %w", err)
	}
	return api.InputReadinessEvaluation{
		Schemas: []api.TrackerQuestionnaire{{
			Tracker: "PTP",
			Fields: []api.TrackerQuestionnaireField{{
				Key:     "no_english_subtitles",
				Label:   "English subtitles",
				Kind:    "select",
				Options: []string{"yes", "no"},
			}},
		}},
		RequirementsFingerprint: fingerprint,
		Fields: []api.InputReadinessFieldOutcome{{
			Key:         "source",
			Status:      api.InputReadinessFieldReady,
			Disposition: api.RuleDispositionStrict,
		}},
	}, nil
}

func assertTrackerAnswer(t *testing.T, answers map[string]map[string]string) {
	t.Helper()
	if got := answers["PTP"]["no_english_subtitles"]; got != "yes" {
		t.Fatalf("PTP no_english_subtitles = %q, want yes in %#v", got, answers)
	}
}

func assertTrackerAnswerAbsent(t *testing.T, answers map[string]map[string]string) {
	t.Helper()
	if _, found := answers["PTP"]["no_english_subtitles"]; found {
		t.Fatalf("stale PTP no_english_subtitles = %#v", answers)
	}
}

func TestDescriptionInputsCarryCanonicalTrackerAnswersAndClearStaleCopies(t *testing.T) {
	input := api.DescriptionInstructions{QuestionnaireAnswers: map[api.TrackerID]map[string]string{
		"PTP": {"no_english_subtitles": "no", "poster": "retained-poster"},
	}}
	state := State{
		Workflow:            api.ReleaseWorkflow{InputReadiness: &api.InputReadinessSnapshotRef{ID: "ready"}},
		InputReadiness:      map[api.InputReadinessSnapshotID]api.InputReadinessSnapshot{"ready": {Schemas: []api.TrackerQuestionnaire{{Tracker: " ptp ", Fields: []api.TrackerQuestionnaireField{{Key: "no_english_subtitles"}}}}}},
		TrackerInputAnswers: map[api.TrackerID]map[string]string{"PTP": {"no_english_subtitles": "yes"}},
	}
	resolved := descriptionInstructionsWithTrackerInputs(input, &state)
	if resolved.QuestionnaireAnswers["PTP"]["no_english_subtitles"] != "yes" || resolved.QuestionnaireAnswers["PTP"]["poster"] != "retained-poster" {
		t.Fatalf("description/upload questionnaire lost canonical input: %#v", resolved.QuestionnaireAnswers)
	}
	delete(state.TrackerInputAnswers["PTP"], "no_english_subtitles")
	resolved = descriptionInstructionsWithTrackerInputs(input, &state)
	if _, exists := resolved.QuestionnaireAnswers["PTP"]["no_english_subtitles"]; exists {
		t.Fatal("Auto restored an obsolete description copy")
	}
	if input.QuestionnaireAnswers["PTP"]["no_english_subtitles"] != "no" {
		t.Fatal("canonical overlay mutated caller instructions")
	}
}
