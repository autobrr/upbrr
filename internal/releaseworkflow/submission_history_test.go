// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type confirmedSubmissionFilter struct{}

func (confirmedSubmissionFilter) FilterConfirmedSubmissions(_ context.Context, _ api.UploadSubject, trackers []api.TrackerID) ([]api.TrackerID, []api.SubmissionExclusion, error) {
	exclusions := make([]api.SubmissionExclusion, 0, len(trackers))
	for _, tracker := range trackers {
		exclusions = append(exclusions, api.SubmissionExclusion{TrackerID: tracker, Reason: "already_uploaded"})
	}
	return nil, exclusions, nil
}

func TestConfirmedSubmissionsCompleteWithoutTrackerPreparation(t *testing.T) {
	t.Parallel()
	module, repository := newTestModule(t, testPreparer(), WithSubmissionHistoryFilter(confirmedSubmissionFilter{}))
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-confirmed"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID: result.Workflow.ID,
 ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{SourcePath: `C:\releases\Example.Release.2026.1080p-GRP`},
	})
	command := ProjectTrackersCommand{
WorkflowID: result.Workflow.ID,
 ExpectedRevision: result.Workflow.Revision,
 TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
 IdempotencyKey: "confirmed",
}
	started, err := module.Start(t.Context(), testOwnerID, command)
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForWorkflowOperation(t, module, result.Workflow.ID, started.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	if terminal.Status != api.StageStatusCompleted || terminal.Result == nil || terminal.Result.Kind != api.WorkflowOperationResultAlreadyUploaded {
		t.Fatalf("asynchronous no-op = %#v", terminal)
	}
	result, err = module.Current(t.Context(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Workflow.AllSelectedTrackersAlreadyUploaded() || len(result.Workflow.SubmissionExclusions) != 2 {
		t.Fatalf("confirmed input did not complete: %#v", result.Workflow)
	}
	descriptor, err := operationResultForCommand(command, result)
	if err != nil || descriptor == nil || descriptor.Kind != api.WorkflowOperationResultAlreadyUploaded {
		t.Fatalf("terminal descriptor = %#v, %v", descriptor, err)
	}
	state, err := repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !module.operationResultIsCurrent(testOwnerID, descriptor, state) {
		t.Fatal("completed no-op descriptor is not current")
	}
	if len(state.Projections) != 0 || len(state.Dupes) != 0 || len(state.Media) != 0 {
		t.Fatal("confirmed trackers ran downstream preparation")
	}
}
