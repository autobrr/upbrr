// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) evaluateInputReadiness(
	ctx context.Context,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command EvaluateInputReadinessCommand,
) (CommandResult, error) {
	release, err := currentReleaseSnapshot(state)
	if err != nil {
		return CommandResult{}, err
	}
	trackerInputAnswers, err := normalizeTrackerInputAnswers(command.TrackerInputAnswers)
	if err != nil {
		return CommandResult{}, err
	}
	command.TrackerInputAnswers = trackerInputAnswers
	trackerIDs := normalizeContinuationTrackerIDs(command.TrackerIDs)
	trackerNames := make([]string, len(trackerIDs))
	for index := range trackerIDs {
		trackerNames[index] = string(trackerIDs[index])
	}
	if trackerInputAnswersChanged(state.TrackerInputAnswers, command.TrackerInputAnswers) {
		applyTrackerInputAnswers(state, command.TrackerInputAnswers)
		invalidateTrackerAndDownstream(&state.Workflow)
	}
	subject, err := m.preparer.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:              api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation},
		Trackers:             trackerNames,
		QuestionnaireAnswers: cloneTrackerInputAnswers(state.TrackerInputAnswers),
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow resolve input readiness subject: %w", err)
	}
	globalEvaluation := defaultInputReadinessEvaluation(subject, trackerIDs)
	evaluation := globalEvaluation
	if m.inputReadiness != nil {
		evaluation, err = m.inputReadiness.Evaluate(ctx, subject, trackerIDs)
		if err != nil {
			return CommandResult{}, fmt.Errorf("release workflow evaluate input readiness: %w", err)
		}
		evaluation.Fields = append(globalEvaluation.Fields, evaluation.Fields...)
	}
	if err := validateTrackerInputPatch(command.TrackerInputAnswers, trackerIDs, evaluation.Schemas); err != nil {
		return CommandResult{}, err
	}
	id, err := m.newID("input_readiness")
	if err != nil {
		return CommandResult{}, err
	}
	snapshot := api.InputReadinessSnapshot{
		Schemas:                 evaluation.Schemas,
		ID:                      api.InputReadinessSnapshotID(id),
		WorkflowID:              state.Workflow.ID,
		Revision:                nextRevision,
		Release:                 api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation},
		FactInstructions:        state.Workflow.FactInstructions,
		SelectedTrackerIDs:      trackerIDs,
		RequirementsFingerprint: evaluation.RequirementsFingerprint,
		Fields:                  slices.Clone(evaluation.Fields),
		RequiredActions:         slices.Clone(evaluation.RequiredActions),
		Status:                  api.StageStatusCompleted,
		CreatedAt:               now,
	}
	if state.Corrections != nil {
		snapshot.CorrectionRevision = state.Corrections.Revision
	}
	if err := m.stampInputReadinessActions(&snapshot, nextRevision, now); err != nil {
		return CommandResult{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow publish input readiness: %w", err)
	}
	state.InputReadiness[snapshot.ID] = snapshot
	state.Workflow.InputReadiness = &api.InputReadinessSnapshotRef{ID: snapshot.ID, Revision: snapshot.Revision}
	setWorkflowStageStatus(&state.Workflow, snapshot.Status, snapshot.RequiredActions, nil)
	return CommandResult{InputReadiness: &snapshot}, nil
}

func validateTrackerInputPatch(patch map[api.TrackerID]map[string]*string, selected []api.TrackerID, schemas []api.TrackerQuestionnaire) error {
	for trackerID, fields := range patch {
		if !slices.Contains(selected, trackerID) {
			return fmt.Errorf("%w: tracker input target is not selected", ErrInvalidTransition)
		}
		index := slices.IndexFunc(schemas, func(schema api.TrackerQuestionnaire) bool {
			return strings.EqualFold(schema.Tracker, string(trackerID))
		})
		if index < 0 {
			return fmt.Errorf("%w: tracker has no local input schema", ErrInvalidTransition)
		}
		for key, value := range fields {
			fieldIndex := slices.IndexFunc(schemas[index].Fields, func(field api.TrackerQuestionnaireField) bool { return field.Key == key })
			if fieldIndex < 0 {
				return fmt.Errorf("%w: unknown tracker input field", ErrInvalidTransition)
			}
			field := schemas[index].Fields[fieldIndex]
			if value != nil && ((len(field.Options) > 0 && !slices.Contains(field.Options, *value)) || (field.Required && strings.TrimSpace(*value) == "")) {
				return fmt.Errorf("%w: invalid tracker input value", ErrInvalidTransition)
			}
		}
	}
	return nil
}

func normalizeTrackerInputAnswers(
	patch map[api.TrackerID]map[string]*string,
) (map[api.TrackerID]map[string]*string, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	normalized := make(map[api.TrackerID]map[string]*string, len(patch))
	for trackerID, fields := range patch {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if _, exists := normalized[trackerID]; exists {
			return nil, fmt.Errorf("%w: tracker input target conflicts after normalization", ErrInvalidTransition)
		}
		normalized[trackerID] = maps.Clone(fields)
	}
	return normalized, nil
}

func trackerInputAnswersChanged(current map[api.TrackerID]map[string]string, patch map[api.TrackerID]map[string]*string) bool {
	for trackerID, fields := range patch {
		for field, value := range fields {
			previous, exists := current[trackerID][field]
			if value == nil {
				if exists {
					return true
				}
			} else if !exists || previous != *value {
				return true
			}
		}
	}
	return false
}

func applyTrackerInputAnswers(state *State, patch map[api.TrackerID]map[string]*string) {
	if len(patch) == 0 {
		return
	}
	if state.TrackerInputAnswers == nil {
		state.TrackerInputAnswers = make(map[api.TrackerID]map[string]string)
	}
	for trackerID, values := range patch {
		answers := state.TrackerInputAnswers[trackerID]
		if answers == nil {
			answers = make(map[string]string)
			state.TrackerInputAnswers[trackerID] = answers
		}
		for key, value := range values {
			if value == nil {
				delete(answers, key)
				continue
			}
			answers[key] = *value
		}
	}
}

func cloneTrackerInputAnswers(values map[api.TrackerID]map[string]string) map[string]map[string]string {
	cloned := make(map[string]map[string]string, len(values))
	for trackerID, answers := range values {
		answerCopy := make(map[string]string, len(answers))
		maps.Copy(answerCopy, answers)
		cloned[string(trackerID)] = answerCopy
	}
	return cloned
}

func defaultInputReadinessEvaluation(subject api.UploadSubject, trackerIDs []api.TrackerID) api.InputReadinessEvaluation {
	fingerprint, _ := api.CanonicalWorkflowFingerprint(struct {
		Version    string
		TrackerIDs []api.TrackerID
	}{"global-input-v1", trackerIDs})
	fields := []api.InputReadinessFieldOutcome{
		{
			Key:         "source",
			Status:      api.InputReadinessFieldReady,
			Disposition: api.RuleDispositionStrict,
		},
		{
			Key:         "type",
			Status:      api.InputReadinessFieldReady,
			Disposition: api.RuleDispositionStrict,
		},
	}
	if strings.TrimSpace(subject.Source) == "" {
		fields[0].Status = api.InputReadinessFieldMissing
		fields[0].Message = "Release source is required."
	}
	if strings.TrimSpace(subject.Type) == "" {
		fields[1].Status = api.InputReadinessFieldMissing
		fields[1].Message = "Release type is required."
	}
	return api.InputReadinessEvaluation{RequirementsFingerprint: fingerprint, Fields: fields}
}

func (m *Module) stampInputReadinessActions(snapshot *api.InputReadinessSnapshot, revision api.WorkflowRevision, now time.Time) error {
	for index := range snapshot.RequiredActions {
		action := &snapshot.RequiredActions[index]
		if action.ID == "" {
			id, err := m.newID("action")
			if err != nil {
				return err
			}
			action.ID = api.RequiredActionID(id)
		}
		action.Status = api.RequiredActionStatusPending
		action.WorkflowRevision = revision
		action.CreatedAt = now
	}
	return nil
}

func inputReadinessMatches(snapshot *api.InputReadinessSnapshot, current CommandResult, trackerIDs []api.TrackerID) bool {
	if snapshot == nil || current.Release == nil || snapshot.Status != api.StageStatusCompleted {
		return false
	}
	if snapshot.Release.SourcePath != current.Release.Release.Source.SourcePath || snapshot.Release.Generation != current.Release.Release.Generation {
		return false
	}
	if !slices.Equal(snapshot.SelectedTrackerIDs, normalizeContinuationTrackerIDs(trackerIDs)) {
		return false
	}
	return !slices.ContainsFunc(snapshot.Fields, func(field api.InputReadinessFieldOutcome) bool {
		if len(field.TrackerIDs) != 0 {
			return false
		}
		return (field.Status == api.InputReadinessFieldMissing || field.Status == api.InputReadinessFieldInvalid) &&
			api.NormalizeRuleDisposition(field.Disposition) != api.RuleDispositionAdvisory
	})
}

func inputReadinessGoalSatisfied(snapshot *api.InputReadinessSnapshot, current CommandResult, trackerIDs []api.TrackerID) bool {
	if !inputReadinessMatches(snapshot, current, trackerIDs) {
		return false
	}
	return !slices.ContainsFunc(snapshot.Fields, func(field api.InputReadinessFieldOutcome) bool {
		return (field.Status == api.InputReadinessFieldMissing || field.Status == api.InputReadinessFieldInvalid) &&
			api.NormalizeRuleDisposition(field.Disposition) != api.RuleDispositionAdvisory
	})
}

func inputReadinessBlocked(snapshot *api.InputReadinessSnapshot) bool {
	return snapshot != nil && slices.ContainsFunc(snapshot.Fields, func(field api.InputReadinessFieldOutcome) bool {
		if len(field.TrackerIDs) != 0 {
			return false
		}
		return (field.Status == api.InputReadinessFieldMissing || field.Status == api.InputReadinessFieldInvalid) &&
			api.NormalizeRuleDisposition(field.Disposition) != api.RuleDispositionAdvisory
	})
}
