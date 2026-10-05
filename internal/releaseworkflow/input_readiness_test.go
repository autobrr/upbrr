// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
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

type stagedQuestionnaireEvaluator struct {
	readyInputReadinessEvaluator
	schema *api.TrackerQuestionnaire
}

func (e stagedQuestionnaireEvaluator) Evaluate(ctx context.Context, subject api.UploadSubject, trackerIDs []api.TrackerID) (api.InputReadinessEvaluation, error) {
	result, err := (readyInputReadinessEvaluator{}).Evaluate(ctx, subject, trackerIDs)
	result.Schemas = nil
	if e.schema != nil {
		result.TrackerQuestionnaires = []api.TrackerQuestionnaire{*e.schema}
		return result, err
	}
	result.TrackerQuestionnaires = []api.TrackerQuestionnaire{{Tracker: "OE", Fields: []api.TrackerQuestionnaireField{{
		Key:      "source_notes",
		Kind:     "textarea",
		Required: true,
	}}}}
	return result, err
}

func TestLegacyQuestionnaireStagingPersistsWithoutInputControlsOrGate(t *testing.T) {
	preparer := testPreparer()
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{
			SourcePath: input.Release.SourcePath,
			Source:     "BluRay",
			Type:       "ENCODE",
		}, nil
	}
	module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(stagedQuestionnaireEvaluator{}))
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Example.Release.2026"},
	})
	for _, answer := range []*string{new("Reviewed source"), nil} {
		current = executeCommand(t, module, EvaluateInputReadinessCommand{
			WorkflowID:          current.Workflow.ID,
			ExpectedRevision:    current.Workflow.Revision,
			TrackerIDs:          []api.TrackerID{"OE"},
			TrackerInputAnswers: map[api.TrackerID]map[string]*string{"OE": {"source_notes": answer}},
		})
		if current.InputReadiness == nil || len(current.InputReadiness.Schemas) != 0 || current.InputReadiness.Status != api.StageStatusCompleted || inputReadinessBlocked(current.InputReadiness) || len(current.InputReadiness.RequiredActions) != 0 {
			t.Fatalf("tracker staging created Input gate/controls: %+v", current.InputReadiness)
		}
		state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		value, exists := state.TrackerInputAnswers["OE"]["source_notes"]
		if (answer == nil && exists) || (answer != nil && (!exists || value != *answer)) {
			t.Fatalf("staged answer=%q exists=%t", value, exists)
		}
	}
}

func TestTrackerInputStagingValidatesMultiselectChoices(t *testing.T) {
	for _, test := range []struct {
		name, answer string
		wantError    bool
	}{
		{"valid pair", " No English Subs, Hardcoded Subs (Non-English) ", false},
		{"unknown token", "No English Subs,Unknown choice", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			preparer := testPreparer()
			preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
				return api.UploadSubject{
					SourcePath: input.Release.SourcePath,
					Source:     "BluRay",
					Type:       "ENCODE",
				}, nil
			}
			evaluator := stagedQuestionnaireEvaluator{schema: &api.TrackerQuestionnaire{Tracker: "PTP", Fields: []api.TrackerQuestionnaireField{{
				Key:      "subtitle_tags",
				Kind:     "multiselect",
				Required: true,
				Options:  []string{"No English Subs", "Hardcoded Subs (Non-English)"},
			}}}}
			module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(evaluator))
			current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "Example.Release.2026"},
			})
			_, err := module.Execute(t.Context(), testOwnerID, EvaluateInputReadinessCommand{
				WorkflowID:          current.Workflow.ID,
				ExpectedRevision:    current.Workflow.Revision,
				TrackerIDs:          []api.TrackerID{"PTP"},
				TrackerInputAnswers: map[api.TrackerID]map[string]*string{"PTP": {"subtitle_tags": new(test.answer)}},
			})
			if (err != nil) != test.wantError || (err != nil && !errors.Is(err, ErrInvalidTransition)) {
				t.Fatalf("staged multiselect error=%v wantError=%t", err, test.wantError)
			}
			state, loadErr := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			answer, exists := state.TrackerInputAnswers["PTP"]["subtitle_tags"]
			if test.wantError && exists {
				t.Fatal("invalid choice persisted")
			}
			if !test.wantError && (!exists || answer != test.answer) {
				t.Fatalf("valid choices not retained: %q", answer)
			}
		})
	}
}

func TestTrackerInputChoiceValidationPreservesSingleAndResetSemantics(t *testing.T) {
	for _, test := range []struct {
		name, kind          string
		value               *string
		required, wantError bool
	}{
		{"single choice", "select", new("yes"), true, false},
		{"single rejects list", "select", new("yes,no"), true, true},
		{"required reset", "multiselect", nil, true, false},
		{"required empty", "multiselect", new(""), true, true},
		{"required empty tokens", "multiselect", new(" , , "), true, true},
		{"optional empty", "multiselect", new(""), false, false},
		{"trim nonempty tokens", "multiselect", new(" yes, , no ,"), true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateTrackerInputPatch(map[api.TrackerID]map[string]*string{"ONE": {"choice": test.value}}, []api.TrackerID{"ONE"}, []api.TrackerQuestionnaire{{Tracker: "ONE", Fields: []api.TrackerQuestionnaireField{{
				Key:      "choice",
				Kind:     test.kind,
				Required: test.required,
				Options:  []string{"yes", "no"},
			}}}}, nil)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v wantError=%t", err, test.wantError)
			}
		})
	}
}

type registryQuestionnaireEvaluator struct{ registry *trackers.Registry }

func (e registryQuestionnaireEvaluator) Requirements(_ context.Context, ids []api.TrackerID) (api.MetadataRequirementSet, error) {
	result, err := trackers.CollectMetadataRequirements(e.registry, ids)
	if err != nil {
		return result, fmt.Errorf("collect tracker metadata requirements: %w", err)
	}
	return result, nil
}
func (e registryQuestionnaireEvaluator) Evaluate(_ context.Context, subject api.UploadSubject, ids []api.TrackerID) (api.InputReadinessEvaluation, error) {
	result, err := trackers.EvaluateInputReadiness(e.registry, ids, subject)
	if err != nil {
		return result, fmt.Errorf("evaluate tracker input readiness: %w", err)
	}
	return result, nil
}

func TestTrackerInputResetClearsPreviouslyStagedConditionalField(t *testing.T) {
	preparer := testPreparer()
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{
			SourcePath:                  input.Release.SourcePath,
			Source:                      "BluRay",
			Type:                        "ENCODE",
			Identity:                    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			ProviderMetadata:            api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Action"}},
			TrackerQuestionnaireAnswers: input.QuestionnaireAnswers,
		}, nil
	}
	module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(registryQuestionnaireEvaluator{registry: trackerimpl.MustNewRegistry()}))
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Example.Release.2026"},
	})
	for _, value := range []*string{new("drama"), nil} {
		current = executeCommand(t, module, EvaluateInputReadinessCommand{
			WorkflowID:          current.Workflow.ID,
			ExpectedRevision:    current.Workflow.Revision,
			TrackerIDs:          []api.TrackerID{"ANT"},
			TrackerInputAnswers: map[api.TrackerID]map[string]*string{"ANT": {"tags": value}},
		})
	}
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.TrackerInputAnswers["ANT"]["tags"]; exists {
		t.Fatal("reset retained the tracker override")
	}
	_, err = module.Execute(t.Context(), testOwnerID, EvaluateInputReadinessCommand{
		WorkflowID:          current.Workflow.ID,
		ExpectedRevision:    current.Workflow.Revision,
		TrackerIDs:          []api.TrackerID{"ANT"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{"ANT": {"unknown_field": nil}},
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unknown new reset field accepted: %v", err)
	}
}

func TestPTPInputResetClearsHiddenReviewField(t *testing.T) {
	preparer := testPreparer()
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{
			SourcePath:                  input.Release.SourcePath,
			Source:                      "BluRay",
			Type:                        "ENCODE",
			AudioLanguages:              []string{"French"},
			Identity:                    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			TrackerQuestionnaireAnswers: input.QuestionnaireAnswers,
		}, nil
	}
	registry := trackerimpl.MustNewRegistry()
	module, repository := newTestModule(t, preparer, WithInputReadinessEvaluator(registryQuestionnaireEvaluator{registry: registry}))
	current := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "Example.Release.2026"},
	})
	for _, answers := range []map[string]*string{{"trumpable_review": new("yes")}, {"no_english_subtitles": new("yes")}} {
		current = executeCommand(t, module, EvaluateInputReadinessCommand{
			WorkflowID:          current.Workflow.ID,
			ExpectedRevision:    current.Workflow.Revision,
			TrackerIDs:          []api.TrackerID{"PTP"},
			TrackerInputAnswers: map[api.TrackerID]map[string]*string{"PTP": answers},
		})
	}
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"PTP"}, api.UploadSubject{
		AudioLanguages:              []string{"French"},
		TrackerQuestionnaireAnswers: cloneTrackerInputAnswers(state.TrackerInputAnswers),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range evaluation.TrackerQuestionnaires {
		for _, field := range schema.Fields {
			if field.Key == "trumpable_review" {
				t.Fatal("explicit no-English answer did not hide subtitle review")
			}
		}
	}
	current = executeCommand(t, module, EvaluateInputReadinessCommand{
		WorkflowID:          current.Workflow.ID,
		ExpectedRevision:    current.Workflow.Revision,
		TrackerIDs:          []api.TrackerID{"PTP"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{"PTP": {"trumpable_review": nil}},
	})
	state, err = repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.TrackerInputAnswers["PTP"]["trumpable_review"]; exists {
		t.Fatal("reset retained the hidden tracker override")
	}
	if state.TrackerInputAnswers["PTP"]["no_english_subtitles"] != "yes" {
		t.Fatal("reset changed the independent no-English answer")
	}
	_, err = module.Execute(t.Context(), testOwnerID, EvaluateInputReadinessCommand{
		WorkflowID:          current.Workflow.ID,
		ExpectedRevision:    current.Workflow.Revision,
		TrackerIDs:          []api.TrackerID{"PTP"},
		TrackerInputAnswers: map[api.TrackerID]map[string]*string{"PTP": {"unknown_field": nil}},
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unknown new reset field accepted: %v", err)
	}
}
