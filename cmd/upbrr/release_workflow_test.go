// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliWorkflowCoreFake struct {
	current        releaseworkflow.CommandResult
	commands       []releaseworkflow.Command
	continuations  []api.ContinueReleaseWorkflowRequest
	continueFn     func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error)
	operation      api.WorkflowOperationStatus
	events         []api.WorkflowEvent
	eventBatches   [][]api.WorkflowEvent
	eventBatch     int
	queueOperation bool
	cancelCalls    int
}

func (f *cliWorkflowCoreFake) ContinueReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
) (releaseworkflow.CommandResult, error) {
	f.continuations = append(f.continuations, request)
	if f.continueFn != nil {
		return f.continueFn(request)
	}
	if request.Authority == nil {
		instructions := api.ReleaseFactInstructions{}
		if request.Intent.FactInstructions != nil {
			instructions = *request.Intent.FactInstructions
		}
		return f.ExecuteReleaseWorkflow(ctx, ownerID, releaseworkflow.CreateWorkflowCommand{
			Instructions:   instructions,
			IdempotencyKey: request.IdempotencyKey,
		})
	}
	if request.Intent.FactInstructions != nil {
		factsMatch := false
		if f.current.FactInstructions != nil {
			currentFingerprint, currentErr := api.CanonicalWorkflowFingerprint(f.current.FactInstructions.Instructions)
			desiredFingerprint, desiredErr := api.CanonicalWorkflowFingerprint(*request.Intent.FactInstructions)
			if currentErr == nil && desiredErr == nil && currentFingerprint == desiredFingerprint {
				factsMatch = true
			}
		}
		if !factsMatch {
			return f.ExecuteReleaseWorkflow(ctx, ownerID, releaseworkflow.ReplaceFactInstructionsCommand{
				WorkflowID:       request.Authority.WorkflowID,
				ExpectedRevision: request.Authority.ExpectedRevision,
				Instructions:     *request.Intent.FactInstructions,
				IdempotencyKey:   request.IdempotencyKey,
			})
		}
	}
	if request.Intent.Preparation != nil {
		if f.current.Release != nil {
			return f.current, nil
		}
		command := releaseworkflow.PrepareReleaseCommand{
			WorkflowID:       request.Authority.WorkflowID,
			ExpectedRevision: request.Authority.ExpectedRevision,
			Input:            *request.Intent.Preparation,
			IdempotencyKey:   request.IdempotencyKey,
		}
		if f.queueOperation {
			operation, err := f.StartReleaseWorkflow(ctx, ownerID, command)
			if err != nil {
				return releaseworkflow.CommandResult{}, err
			}
			current := f.current
			current.Operation = &operation
			return current, nil
		}
		return f.ExecuteReleaseWorkflow(ctx, ownerID, command)
	}
	return releaseworkflow.CommandResult{}, errors.New("unexpected continuation")
}

func TestPrintCLIWorkflowDryRunIncludesClientOutcome(t *testing.T) {
	output := captureStdout(t, func() {
		printCLIWorkflowDryRun(api.UploadDryRunResult{Reports: []api.TrackerDryRunReport{
			{
				TrackerID:         "BETA",
				DisplayName:       "Beta",
				UploadReleaseName: "Example.Release.2026.BETA-GRP",
				Status:            api.StageStatusCompleted,
				ClientInjection: api.ClientInjectionOutcome{
					Status:  api.StageStatusCompleted,
					Message: "Client injection completed.",
				},
				Warnings: []string{"Synthetic warning."},
			},
		}}, false)
	})
	if !strings.Contains(output, "status=completed") ||
		!strings.Contains(output, "client injection was attempted for each ready tracker") ||
		!strings.Contains(output, "client injection: completed: Client injection completed.") ||
		!strings.Contains(output, "warning: Synthetic warning.") {
		t.Fatalf("upload dry-run output = %q", output)
	}
}

func TestPrintCLIWorkflowProjectionsIncludesAuditablePolicyDetails(t *testing.T) {
	output := captureStdout(t, func() {
		printCLIWorkflowProjections(&api.TrackerReleaseProjectionSet{
			Projections: []api.TrackerReleaseProjection{{
				DisplayName:      "Example",
				UploadReleaseName: "Example.Release.2026-GRP",
				Readiness:        api.ReadinessStatusIneligible,
				PolicyDecisions: []api.TrackerPolicyDecision{
					{Code: "release_name_policy", Decision: "standalone/example/v1"},
					{
						Code:     "unsupported_source",
						Decision: "ineligible",
						Blocking: true,
						Message:  "Example does not support the release source.",
					},
					{
						Code:     "banned_group",
						Decision: "bypassed",
						Message:  "Debug mode bypassed tracker banned-group policy.",
					},
				},
			}},
		})
	})
	for _, expected := range []string{
		"code=unsupported_source decision=ineligible blocking=true reason=Example does not support the release source.",
		"code=banned_group decision=bypassed blocking=false reason=Debug mode bypassed tracker banned-group policy.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("tracker projection output missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "release_name_policy") {
		t.Fatalf("tracker projection output included non-diagnostic policy provenance: %q", output)
	}
}

func (f *cliWorkflowCoreFake) ExecuteReleaseWorkflow(
	_ context.Context,
	ownerID string,
	command releaseworkflow.Command,
) (releaseworkflow.CommandResult, error) {
	if ownerID != cliWorkflowOwnerID {
		return releaseworkflow.CommandResult{}, errors.New("unexpected owner")
	}
	f.commands = append(f.commands, command)
	switch value := command.(type) {
	case releaseworkflow.CreateWorkflowCommand:
		f.current = releaseworkflow.CommandResult{
			Workflow:         api.ReleaseWorkflow{ID: "workflow_cli", Revision: 1},
			FactInstructions: &api.ReleaseFactInstructionSnapshot{Instructions: value.Instructions},
		}
	case releaseworkflow.ReplaceFactInstructionsCommand:
		f.current.Workflow.Revision++
		f.current.Workflow.RequiredActions = nil
		f.current.Release = nil
		f.current.FactInstructions = &api.ReleaseFactInstructionSnapshot{Instructions: value.Instructions}
	case releaseworkflow.PrepareReleaseCommand:
		f.current.Workflow.Revision++
		f.current.Release = &api.ReleaseSnapshot{Release: api.PreparedRelease{
			Generation: 1,
			Source: api.SourceManifest{
				SourcePath: value.Input.SourcePath,
			},
			Naming: api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
		}}
		if !value.Input.Instructions.Playlist.Set {
			f.current.Workflow.RequiredActions = []api.RequiredAction{{
				ID:               "action_playlist",
				Kind:             api.RequiredActionSelectPlaylist,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: f.current.Workflow.Revision,
				Prompt:           "Select playlist",
				Options: []api.RequiredActionOption{
					{Value: "00001.mpls", Label: "00001.mpls"},
					{Value: "00002.mpls", Label: "00002.mpls"},
				},
			}}
		} else {
			f.current.Workflow.RequiredActions = nil
		}
	default:
		return releaseworkflow.CommandResult{}, errors.New("unexpected command")
	}
	return f.current, nil
}

func (f *cliWorkflowCoreFake) StartReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	command releaseworkflow.Command,
) (api.WorkflowOperationStatus, error) {
	if f.queueOperation {
		f.commands = append(f.commands, command)
		f.operation = api.WorkflowOperationStatus{
			ID:         "operation_cli",
			WorkflowID: f.current.Workflow.ID,
			Revision:   f.current.Workflow.Revision,
			Sequence:   1,
			Command:    "test",
			Operation:  api.OperationKindPreparation,
			Status:     api.StageStatusQueued,
			StartedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		return f.operation, nil
	}
	result, err := f.ExecuteReleaseWorkflow(ctx, ownerID, command)
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	f.operation = api.WorkflowOperationStatus{
		ID:             "operation_cli",
		WorkflowID:     result.Workflow.ID,
		Revision:       max(1, result.Workflow.Revision-1),
		ResultRevision: result.Workflow.Revision,
		Sequence:       2,
		Command:        "test",
		Operation:      api.OperationKindPreparation,
		Status:         api.StageStatusCompleted,
		Progress:       100,
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	completedAt := f.operation.UpdatedAt
	f.operation.CompletedAt = &completedAt
	return f.operation, nil
}

func (f *cliWorkflowCoreFake) CurrentReleaseWorkflow(
	context.Context,
	string,
	api.WorkflowID,
) (releaseworkflow.CommandResult, error) {
	return f.current, nil
}

func (f *cliWorkflowCoreFake) ReleaseWorkflowOperation(
	context.Context,
	string,
	api.WorkflowID,
	api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	return f.operation, nil
}

func (f *cliWorkflowCoreFake) ReleaseWorkflowOperationEvents(
	_ context.Context,
	_ string,
	_ api.WorkflowID,
	operationID api.WorkflowOperationID,
	after uint64,
	limit int,
) ([]api.WorkflowEvent, error) {
	available := f.events
	if f.eventBatch < len(f.eventBatches) {
		available = f.eventBatches[f.eventBatch]
		f.eventBatch++
	}
	events := make([]api.WorkflowEvent, 0, len(available))
	for _, event := range available {
		if event.OperationID != operationID || event.Sequence <= after {
			continue
		}
		events = append(events, event)
		if limit > 0 && len(events) == limit {
			break
		}
	}
	return events, nil
}

func (f *cliWorkflowCoreFake) CancelReleaseWorkflowOperation(
	context.Context,
	string,
	api.WorkflowID,
	api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	f.cancelCalls++
	f.operation.Status = api.StageStatusCanceled
	return f.operation, nil
}

func TestCLIWorkflowCancellationCancelsAcceptedOperation(t *testing.T) {
	coreSvc := &cliWorkflowCoreFake{
		current:        releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{ID: "workflow-cli-cancel", Revision: 1}},
		queueOperation: true,
	}
	session := &cliWorkflowSession{core: coreSvc, current: coreSvc.current}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	input := api.PrepareInput{SourcePath: "Example.Release.2026.1080p-GRP"}
	err := session.executeContinuation(ctx, api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       coreSvc.current.Workflow.ID,
			ExpectedRevision: coreSvc.current.Workflow.Revision,
		},
		IdempotencyKey: "cancel-preparation",
		Goal:           api.WorkflowGoalPrepared,
		Intent:         api.WorkflowIntent{Preparation: &input},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled workflow error = %v", err)
	}
	if coreSvc.cancelCalls != 1 {
		t.Fatalf("operation cancel calls = %d, want 1", coreSvc.cancelCalls)
	}
}

func TestCLIWorkflowBlockedOperationPublishesResult(t *testing.T) {
	session := &cliWorkflowSession{core: &cliWorkflowCoreFake{}}
	operation := api.WorkflowOperationStatus{
		ID:         "operation_cli_blocked",
		WorkflowID: "workflow_cli_blocked",
		Status:     api.StageStatusBlocked,
		Message:    "Input required.",
	}
	completed, err := session.waitForOperation(context.Background(), operation)
	if err != nil {
		t.Fatalf("wait for blocked operation: %v", err)
	}
	if completed.Status != api.StageStatusBlocked {
		t.Fatalf("operation status = %q, want %q", completed.Status, api.StageStatusBlocked)
	}
}

func TestCLIWorkflowLoadsEveryRetainedOperationEventDelta(t *testing.T) {
	logger := &cliProgressTestLogger{}
	operation := api.WorkflowOperationStatus{
		ID:         "operation_cli_events",
		WorkflowID: "workflow_cli_events",
	}
	coreSvc := &cliWorkflowCoreFake{events: []api.WorkflowEvent{
		{
			Sequence:    101,
			WorkflowID:  operation.WorkflowID,
			OperationID: operation.ID,
			Command:     "check_duplicates",
			Phase:       "duplicate_check",
			Scope:       api.WorkflowEventScopeTracker,
			ScopeID:     "ALPHA",
			Lifecycle:   api.OperationLifecycleTerminal,
			State:       api.StageStatusCompleted,
			Disposition: api.WorkflowDispositionSucceeded,
			Severity:    api.WorkflowEventSeverityInfo,
			Message:     "No duplicate found.",
		},
		{
			Sequence:    102,
			WorkflowID:  operation.WorkflowID,
			OperationID: operation.ID,
			Command:     "check_duplicates",
			Phase:       "duplicate_check",
			Scope:       api.WorkflowEventScopeTracker,
			ScopeID:     "BETA",
			Lifecycle:   api.OperationLifecycleTerminal,
			State:       api.StageStatusCompleted,
			Disposition: api.WorkflowDispositionSucceeded,
			Severity:    api.WorkflowEventSeverityInfo,
			Message:     "No duplicate found.",
		},
	}}
	session := &cliWorkflowSession{core: coreSvc, logger: logger}
	state := cliWorkflowEventLogState{lastSequence: 100}

	if err := session.logNewOperationEvents(context.Background(), operation, &state); err != nil {
		t.Fatalf("load retained operation events: %v", err)
	}
	entries := logger.snapshot()
	if len(entries) != 2 || !strings.Contains(entries[0], "scope_id=ALPHA") || !strings.Contains(entries[1], "scope_id=BETA") {
		t.Fatalf("retained operation event logs = %#v", entries)
	}
}

func TestCLIWorkflowPollingDoesNotMixSnapshotAndRetainedEventSequences(t *testing.T) {
	logger := &cliProgressTestLogger{}
	initial := api.WorkflowOperationStatus{
		ID:         "operation_cli_event_domains",
		WorkflowID: "workflow_cli_event_domains",
		Status:     api.StageStatusQueued,
		Events: []api.WorkflowEvent{{
			Sequence: 2000,
			State:    api.StageStatusQueued,
		}},
	}
	terminal := initial
	terminal.Status = api.StageStatusCompleted
	terminal.Events = []api.WorkflowEvent{{
		Sequence: 5000,
		State:    api.StageStatusCompleted,
	}}
	event := func(sequence uint64, scope api.WorkflowEventScope, scopeID string, state api.StageStatus) api.WorkflowEvent {
		return api.WorkflowEvent{
			Sequence:    sequence,
			WorkflowID:  initial.WorkflowID,
			OperationID: initial.ID,
			Command:     "check_duplicates",
			Phase:       "duplicate_check",
			Scope:       scope,
			ScopeID:     scopeID,
			Lifecycle:   api.OperationLifecycleRunning,
			State:       state,
			Disposition: api.WorkflowDispositionNone,
			Severity:    api.WorkflowEventSeverityDebug,
			Message:     string(state),
		}
	}
	queued := event(1, api.WorkflowEventScopeWorkflow, string(initial.WorkflowID), api.StageStatusQueued)
	queued.Lifecycle = api.OperationLifecycleQueued
	running := event(2, api.WorkflowEventScopeWorkflow, string(initial.WorkflowID), api.StageStatusRunning)
	alpha := event(3, api.WorkflowEventScopeTracker, "ALPHA", api.StageStatusCompleted)
	alpha.Lifecycle = api.OperationLifecycleTerminal
	alpha.Disposition = api.WorkflowDispositionSucceeded
	alpha.Severity = api.WorkflowEventSeverityInfo
	beta := event(4, api.WorkflowEventScopeTracker, "BETA", api.StageStatusCompleted)
	beta.Lifecycle = api.OperationLifecycleTerminal
	beta.Disposition = api.WorkflowDispositionSucceeded
	beta.Severity = api.WorkflowEventSeverityInfo
	completed := event(5, api.WorkflowEventScopeWorkflow, string(initial.WorkflowID), api.StageStatusCompleted)
	completed.Lifecycle = api.OperationLifecycleTerminal
	completed.Disposition = api.WorkflowDispositionSucceeded
	completed.Severity = api.WorkflowEventSeverityInfo

	coreSvc := &cliWorkflowCoreFake{
		operation: terminal,
		eventBatches: [][]api.WorkflowEvent{
			{queued, running},
			{alpha, beta, completed},
		},
	}
	session := &cliWorkflowSession{core: coreSvc, logger: logger}

	result, err := session.waitForOperation(context.Background(), initial)
	if err != nil {
		t.Fatalf("wait for operation: %v", err)
	}
	if result.Status != api.StageStatusCompleted {
		t.Fatalf("operation status = %s, want completed", result.Status)
	}
	entries := logger.snapshot()
	if len(entries) != 5 {
		t.Fatalf("retained event logs = %#v", entries)
	}
	for index, expected := range []string{
		"lifecycle=queued state=queued",
		"lifecycle=running state=running",
		"scope_id=ALPHA lifecycle=terminal",
		"scope_id=BETA lifecycle=terminal",
		"scope=workflow",
	} {
		if !strings.Contains(entries[index], expected) {
			t.Fatalf("retained event log %d = %q, want %q", index, entries[index], expected)
		}
	}
}

func TestCLIWorkflowUnattendedPlaylistActionDoesNotPrompt(t *testing.T) {
	t.Parallel()

	coreSvc := &cliWorkflowCoreFake{}
	_, err := newCLIWorkflowSession(
		context.Background(),
		coreSvc,
		api.Request{
			SourcePath: "Example.Release.2026.1080p-GRP",
			Options:    api.UploadOptions{InteractionMode: api.InteractionModeUnattended},
		},
		api.PreparationIntentUpload,
		bufio.NewReader(strings.NewReader("1\n")),
		config.Config{},
		api.NopLogger{},
	)
	if err == nil || !strings.Contains(err.Error(), "unattended") {
		t.Fatalf("error = %v, want unattended playlist block", err)
	}
	if len(coreSvc.commands) != 2 {
		t.Fatalf("commands = %d, want create and prepare only", len(coreSvc.commands))
	}
}

func TestCLIWorkflowLargestPlaylistUsesTypedFactReplacement(t *testing.T) {
	t.Parallel()

	coreSvc := &cliWorkflowCoreFake{}
	_, err := newCLIWorkflowSession(
		context.Background(),
		coreSvc,
		api.Request{
			SourcePath: "Example.Release.2026.1080p-GRP",
			Options:    api.UploadOptions{InteractionMode: api.InteractionModeUnattended},
		},
		api.PreparationIntentUpload,
		nil,
		config.Config{Metadata: config.MetadataConfig{UseLargestPlaylist: true}},
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("new workflow session: %v", err)
	}
	if len(coreSvc.commands) != 4 {
		t.Fatalf("commands = %d, want create, prepare, replace, prepare", len(coreSvc.commands))
	}
	replace, ok := coreSvc.commands[2].(releaseworkflow.ReplaceFactInstructionsCommand)
	if !ok {
		t.Fatalf("third command = %T", coreSvc.commands[2])
	}
	if !replace.Instructions.Playlist.Set || !slices.Equal(replace.Instructions.Playlist.Selected, []string{"00001.mpls"}) {
		t.Fatalf("playlist instructions = %#v", replace.Instructions.Playlist)
	}
}

func TestCLIWorkflowSessionsUseDistinctDurableIdempotencyKeys(t *testing.T) {
	t.Parallel()

	request := api.Request{
		SourcePath: "Example.Release.2026.1080p-GRP",
		PlaylistInstruction: api.PlaylistInstruction{
			Set:      true,
			Selected: []string{"00001.mpls"},
		},
	}
	firstCore := &cliWorkflowCoreFake{}
	if _, err := newCLIWorkflowSession(
		context.Background(),
		firstCore,
		request,
		api.PreparationIntentUpload,
		nil,
		config.Config{},
		api.NopLogger{},
	); err != nil {
		t.Fatalf("create first workflow session: %v", err)
	}
	secondCore := &cliWorkflowCoreFake{}
	if _, err := newCLIWorkflowSession(
		context.Background(),
		secondCore,
		request,
		api.PreparationIntentUpload,
		nil,
		config.Config{},
		api.NopLogger{},
	); err != nil {
		t.Fatalf("create second workflow session: %v", err)
	}
	firstCreate, ok := firstCore.commands[0].(releaseworkflow.CreateWorkflowCommand)
	if !ok {
		t.Fatalf("first command = %T, want create", firstCore.commands[0])
	}
	secondCreate, ok := secondCore.commands[0].(releaseworkflow.CreateWorkflowCommand)
	if !ok {
		t.Fatalf("second command = %T, want create", secondCore.commands[0])
	}
	if firstCreate.IdempotencyKey == "" || secondCreate.IdempotencyKey == "" || firstCreate.IdempotencyKey == secondCreate.IdempotencyKey {
		t.Fatalf("creation idempotency keys must be non-empty and run-scoped: first=%q second=%q", firstCreate.IdempotencyKey, secondCreate.IdempotencyKey)
	}
	firstPrepare, ok := firstCore.commands[1].(releaseworkflow.PrepareReleaseCommand)
	if !ok {
		t.Fatalf("second first-session command = %T, want prepare", firstCore.commands[1])
	}
	secondPrepare, ok := secondCore.commands[1].(releaseworkflow.PrepareReleaseCommand)
	if !ok {
		t.Fatalf("second second-session command = %T, want prepare", secondCore.commands[1])
	}
	if firstPrepare.IdempotencyKey == "" || secondPrepare.IdempotencyKey == "" || firstPrepare.IdempotencyKey == secondPrepare.IdempotencyKey {
		t.Fatalf("preparation idempotency keys must be non-empty and run-scoped: first=%q second=%q", firstPrepare.IdempotencyKey, secondPrepare.IdempotencyKey)
	}
}

func TestCLIWorkflowMapsProjectionAndExecutionInstructions(t *testing.T) {
	t.Parallel()

	anon := false
	discType := "UHD"
	client := "example-client"
	maxPieceSize := 16
	request := api.Request{
		Trackers: []string{"btn"},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"btn": {"source": "web"},
		},
		TrackerConfigOverrides: api.TrackerConfigOverrides{Anon: &anon},
		TrackerSiteOverrides: api.TrackerSiteOverrides{TIK: api.TIKOverrides{
			DiscType: &discType,
		}},
		ClientOverrides:  api.ClientOverrides{Client: &client},
		TorrentOverrides: api.TorrentOverrides{MaxPieceSizeMiB: &maxPieceSize},
		Options:          api.UploadOptions{NoSeed: true, InteractionMode: api.InteractionModeUnattended},
	}
	intent := mapCLIWorkflowIntent(request)
	projection := intent.projectionInstructions[api.TrackerID("BTN")]
	if projection.TrackerConfig.Anon == nil || *projection.TrackerConfig.Anon ||
		projection.TrackerSite.TIK.DiscType == nil || *projection.TrackerSite.TIK.DiscType != discType ||
		projection.Questionnaire["source"] == nil || *projection.Questionnaire["source"] != "web" {
		t.Fatalf("projection instructions = %#v", projection)
	}
	description, err := cliWorkflowDescriptionInstructions(intent, intent.projectionInstructions, nil)
	if err != nil {
		t.Fatalf("description instructions: %v", err)
	}
	if description.TrackerConfig.Anon == nil || *description.TrackerConfig.Anon ||
		description.Client.Client == nil || *description.Client.Client != client ||
		description.Torrent.MaxPieceSizeMiB == nil || *description.Torrent.MaxPieceSizeMiB != maxPieceSize ||
		!description.Options.NoSeed {
		t.Fatalf("execution instructions = %#v", description)
	}
}

func TestCLIWorkflowCompletionSubmitsOneDesiredGoalWithDoubleDupeIntent(t *testing.T) {
	t.Parallel()

	current := releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-centralized", Revision: 2},
		Release: &api.ReleaseSnapshot{Release: api.PreparedRelease{
			Generation: 1,
			Source:     api.SourceManifest{SourcePath: "Example.Release.2026.1080p-GRP"},
			Naming:     api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
		}},
	}
	coreSvc := &cliWorkflowCoreFake{current: current}
	call := 0
	coreSvc.continueFn = func(_ api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
		call++
		next := coreSvc.current
		next.Workflow.Revision++
		if call == 1 {
			next.Selection = &api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA"}}
			next.Projections = &api.TrackerReleaseProjectionSet{
				ID:       "projections-centralized",
				Revision: next.Workflow.Revision,
				Projections: []api.TrackerReleaseProjection{{
					TrackerID:         "ALPHA",
					DisplayName:       "Alpha",
					UploadReleaseName: "Example.Release.2026.1080p-GRP",
					Readiness:         api.ReadinessStatusReady,
				}},
			}
		} else {
			next.DryRun = &api.UploadDryRunResult{
				ID:               "dry-run-centralized",
				WorkflowID:       next.Workflow.ID,
				Revision:         next.Workflow.Revision,
				InputFingerprint: api.WorkflowFingerprint(strings.Repeat("1", 64)),
			}
		}
		coreSvc.current = next
		return next, nil
	}
	session := &cliWorkflowSession{
		core:           coreSvc,
		current:        current,
		idempotencyRun: "centralized",
		intent: cliWorkflowIntent{
			sourcePath:      "Example.Release.2026.1080p-GRP",
			trackerIDs:      []api.TrackerID{"ALPHA"},
			doubleDupeCheck: true,
			media: api.MediaCaptureInstructions{
				ScreenshotCount: 2,
				Purpose:         api.ScreenshotPurposeFinal,
			},
			description: api.DescriptionInstructions{
				Options: api.UploadOptions{InteractionMode: api.InteractionModeUnattended, NoSeed: true},
			},
			interaction: api.InteractionModeUnattended,
			noSeed:      true,
		},
	}
	if _, err := session.complete(
		context.Background(),
		true,
		nil,
		config.Config{},
		api.NopLogger{},
	); err != nil {
		t.Fatalf("complete centralized workflow: %v", err)
	}
	if len(coreSvc.continuations) != 2 {
		t.Fatalf("continuation calls = %d, want 2 backend-owned transitions", len(coreSvc.continuations))
	}
	request := coreSvc.continuations[1]
	if request.Goal != api.WorkflowGoalDryRun || request.Intent.Interaction != api.InteractionModeUnattended ||
		request.Intent.DuplicateCheckCount != 2 ||
		request.Intent.Media == nil || request.Intent.Descriptions == nil || !request.Intent.NoSeed ||
		!slices.Equal(request.Intent.TrackerIDs, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("centralized completion intent = %#v", request)
	}
}

func TestCLIWorkflowUnattendedDefersManualTrackerInputsToCentralPolicy(t *testing.T) {
	t.Parallel()

	instructions := make(map[api.TrackerID]api.TrackerProjectionInstructions)
	changed, err := collectCLIWorkflowQuestionnaires(
		nil,
		api.InteractionModeUnattended,
		&api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
			TrackerID: "ALPHA",
			Questionnaire: []api.TrackerQuestionnaireRequirement{{
				Key:      "edition",
				Required: true,
			}},
		}}},
		instructions,
	)
	if err != nil {
		t.Fatalf("collect unattended questionnaire: %v", err)
	}
	if changed || len(instructions) != 0 {
		t.Fatalf("unattended questionnaire changed instructions = %#v", instructions)
	}

	session := cliWorkflowSession{
		intent: cliWorkflowIntent{interaction: api.InteractionModeUnattended},
		current: releaseworkflow.CommandResult{
			Workflow: api.ReleaseWorkflow{Revision: 3},
			Continuation: api.WorkflowContinuation{RequiredActions: []api.RequiredAction{{
				ID:        "authenticate-alpha",
				Kind:      api.RequiredActionAuthenticateTracker,
				Status:    api.RequiredActionStatusPending,
				TrackerID: "ALPHA",
			}}},
		},
	}
	answers, declined, err := session.collectContinuationActionAnswers(
		context.Background(),
		nil,
		config.Config{},
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("collect unattended tracker action: %v", err)
	}
	if declined || len(answers) != 0 {
		t.Fatalf("unattended tracker action answers = %#v declined=%t", answers, declined)
	}
}

func TestCLIWorkflowMediaInstructionsPreserveExplicitFrames(t *testing.T) {
	t.Parallel()

	instructions := cliWorkflowMediaInstructions(api.Request{
		Options:             api.UploadOptions{Screens: 0, CaptureDVDMenus: true},
		ScreenshotOverrides: api.ScreenshotOverrides{ManualFrames: []int{0, 42}},
	})
	if instructions.ScreenshotCount != 0 || !instructions.CaptureDVDMenus || len(instructions.Selections) != 2 ||
		instructions.Selections[0].Frame != 0 || instructions.Selections[1].Frame != 42 {
		t.Fatalf("media instructions = %#v", instructions)
	}
}

func TestCLIWorkflowMediaInstructionsUseAutomaticFramesWhenOverridesAbsent(t *testing.T) {
	t.Parallel()

	instructions := cliWorkflowMediaInstructions(api.Request{Options: api.UploadOptions{Screens: 0}})
	if instructions.Selections != nil {
		t.Fatalf("media selections = %#v, want nil automatic selection", instructions.Selections)
	}
}
