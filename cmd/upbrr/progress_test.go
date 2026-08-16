// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliProgressTestLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *cliProgressTestLogger) append(level string, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, level+": "+fmt.Sprintf(format, args...))
}

func (l *cliProgressTestLogger) Tracef(format string, args ...any) {
	l.append("TRACE", format, args...)
}

func (l *cliProgressTestLogger) Debugf(format string, args ...any) {
	l.append("DEBUG", format, args...)
}

func (l *cliProgressTestLogger) Infof(format string, args ...any) {
	l.append("INFO", format, args...)
}

func (l *cliProgressTestLogger) Warnf(format string, args ...any) {
	l.append("WARN", format, args...)
}

func (l *cliProgressTestLogger) Errorf(format string, args ...any) {
	l.append("ERROR", format, args...)
}

func (l *cliProgressTestLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

func TestCLIUploadProgressUsesStructuredLogger(t *testing.T) {
	logger := &cliProgressTestLogger{}

	ctx := withCLIUploadProgressLogger(context.Background(), logger)
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      "movie.mkv",
		Tracker:         "beta",
		Task:            "torrent",
		Status:          "running",
		Message:         "Hashing pieces... 10% (10/100 pieces)",
		CompletedPieces: 10,
		TotalPieces:     100,
		Percent:         10,
	})
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      "movie.mkv",
		Tracker:         "beta",
		Task:            "torrent",
		Status:          "running",
		Message:         "Hashing pieces... 15% (15/100 pieces)",
		CompletedPieces: 15,
		TotalPieces:     100,
		Percent:         15,
	})

	entries := logger.snapshot()
	if len(entries) != 2 ||
		!strings.Contains(entries[0], "INFO: torrent: tracker=BETA state=running progress=10 completed=10 total=100") ||
		!strings.Contains(entries[1], "INFO: torrent: tracker=BETA state=running progress=15 completed=15 total=100") {
		t.Fatalf("progress log entries = %#v", entries)
	}
}

func TestCLIUploadProgressFailureUsesWarning(t *testing.T) {
	logger := &cliProgressTestLogger{}

	ctx := withCLIUploadProgressLogger(context.Background(), logger)
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: "movie.mkv",
		Task:       "torrent",
		Status:     "failed",
		Message:    "Torrent creation failed",
	})

	entries := logger.snapshot()
	if len(entries) != 1 || !strings.Contains(entries[0], "WARN: torrent: tracker=none state=failed") {
		t.Fatalf("progress log entries = %#v", entries)
	}
}

func TestCLIUploadProgressFinalStatusResetsStateForNextRun(t *testing.T) {
	logger := &cliProgressTestLogger{}
	states := make(map[string]cliProgressLogState)
	var mu sync.Mutex

	logCLIProgress(logger, api.UploadProgressUpdate{
		SourcePath:      "movie.mkv",
		Task:            "torrent",
		Status:          "completed",
		Message:         "Torrent ready",
		CompletedPieces: 100,
		TotalPieces:     100,
		Percent:         100,
	}, states, &mu)
	logCLIProgress(logger, api.UploadProgressUpdate{
		SourcePath:      "movie.mkv",
		Task:            "torrent",
		Status:          "running",
		Message:         "Hashing pieces... 1% (1/100 pieces)",
		CompletedPieces: 1,
		TotalPieces:     100,
		Percent:         1,
	}, states, &mu)

	entries := logger.snapshot()
	if len(entries) != 2 || !strings.Contains(entries[0], "state=completed") || !strings.Contains(entries[1], "state=running progress=1") {
		t.Fatalf("progress log entries = %#v", entries)
	}
}

func TestCLIUploadProgressIgnoresNonTorrentTasks(t *testing.T) {
	logger := &cliProgressTestLogger{}

	ctx := withCLIUploadProgressLogger(context.Background(), logger)
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: "movie.mkv",
		Task:       "tracker_upload",
		Status:     "running",
		Message:    "Uploading",
	})

	if entries := logger.snapshot(); len(entries) != 0 {
		t.Fatalf("unexpected progress log entries = %#v", entries)
	}
}

func TestCLIWorkflowProgressUsesDebugLogger(t *testing.T) {
	logger := &cliProgressTestLogger{}
	var state cliWorkflowLogState
	logCLIWorkflowOperation(logger, api.WorkflowOperationStatus{
		Command:   "check_duplicates",
		Phase:     "dupes",
		Status:    api.StageStatusRunning,
		Message:   "Checking trackers",
		Completed: 2,
		Total:     4,
	}, &state)
	logCLIWorkflowOperation(logger, api.WorkflowOperationStatus{
		Command:   "check_duplicates",
		Phase:     "dupes",
		Status:    api.StageStatusBlocked,
		Message:   "Input required.",
		Progress:  100,
		Completed: 4,
		Total:     4,
	}, &state)

	entries := logger.snapshot()
	if len(entries) != 2 ||
		!strings.Contains(entries[0], "DEBUG: workflow: command=check_duplicates phase=dupes state=running progress=0 completed=2 total=4") ||
		!strings.Contains(entries[1], "DEBUG: workflow: command=check_duplicates phase=dupes state=blocked progress=100 completed=4 total=4") {
		t.Fatalf("workflow log entries = %#v", entries)
	}
}

func TestCLIWorkflowProgressRendersCanonicalScopedEventsAtDebug(t *testing.T) {
	logger := &cliProgressTestLogger{}
	var state cliWorkflowEventLogState
	events := []api.WorkflowEvent{
		{
			Sequence:    1001,
			Command:     "upload_media_images",
			Phase:       "image_hosting",
			Scope:       api.WorkflowEventScopeHost,
			ScopeID:     "primary",
			Lifecycle:   api.OperationLifecycleTerminal,
			State:       api.StageStatusFailed,
			Disposition: api.WorkflowDispositionFailed,
			Severity:    api.WorkflowEventSeverityWarn,
			FailureCode: api.OperationFailureImageHostUnavailable,
			Recovery:    api.OperationRecoveryRetry,
			Message:     "Primary host failed; fallback remains available.",
		},
		{
			Sequence:    1002,
			Command:     "upload_media_images",
			Phase:       "image_hosting",
			Scope:       api.WorkflowEventScopeTracker,
			ScopeID:     "ALPHA",
			Lifecycle:   api.OperationLifecycleTerminal,
			State:       api.StageStatusFailed,
			Disposition: api.WorkflowDispositionFailed,
			Severity:    api.WorkflowEventSeverityError,
			FailureCode: api.OperationFailureImageHostUnavailable,
			Recovery:    api.OperationRecoveryRetry,
			Message:     "Required host coverage is unavailable.",
		},
	}

	logCLIWorkflowEvents(logger, events, &state)
	logCLIWorkflowEvents(logger, events, &state)
	entries := logger.snapshot()
	if len(entries) != 2 || !strings.HasPrefix(entries[0], "DEBUG: ") || !strings.Contains(entries[0], "scope=host") ||
		!strings.HasPrefix(entries[1], "DEBUG: ") || !strings.Contains(entries[1], "scope=tracker") {
		t.Fatalf("canonical workflow event logs = %#v", entries)
	}
}

func TestCLIWorkflowProgressPrintsOnlyTopLevelOperations(t *testing.T) {
	var output bytes.Buffer

	printCLIWorkflowProgress(&output, api.OperationKindPreparation)
	printCLIWorkflowProgress(&output, api.OperationKindDuplicateCheck)
	printCLIWorkflowProgress(&output, api.OperationKindUploadDryRun)
	printCLIWorkflowProgress(&output, api.OperationKindUploadExecute)

	const expected = "Preparing release...\nPreparing tracker uploads...\nPreparing tracker uploads...\n"
	if output.String() != expected {
		t.Fatalf("CLI workflow progress = %q, want %q", output.String(), expected)
	}
}

func TestCLIWorkflowSessionSuppressesRepeatedProgressAndLogsSuccessiveOperations(t *testing.T) {
	var output bytes.Buffer
	logger := &cliProgressTestLogger{}
	event := api.WorkflowEvent{
		Sequence:    1,
		WorkflowID:  "workflow-cli-events",
		OperationID: "operation-one",
		Command:     "composite_upload",
		Phase:       "prepare",
		Scope:       api.WorkflowEventScopeWorkflow,
		ScopeID:     "prepare",
		Lifecycle:   api.OperationLifecycleRunning,
		State:       api.StageStatusRunning,
		Message:     "running",
	}
	nextEvent := event
	nextEvent.Sequence = 2
	nextEvent.OperationID = "operation-two"
	coreSvc := &cliWorkflowCoreFake{events: []api.WorkflowEvent{event, nextEvent}}
	session := cliWorkflowSession{
		core:           coreSvc,
		logger:         logger,
		progressWriter: &output,
	}
	operation := api.WorkflowOperationStatus{
		ID:         event.OperationID,
		WorkflowID: event.WorkflowID,
		Operation:  api.OperationKindUploadDryRun,
		Status:     api.StageStatusCompleted,
	}
	if _, err := session.waitForOperation(t.Context(), operation); err != nil {
		t.Fatalf("wait for first completed operation: %v", err)
	}
	operation.ID = nextEvent.OperationID
	if _, err := session.waitForOperation(t.Context(), operation); err != nil {
		t.Fatalf("wait for second completed operation: %v", err)
	}

	if got := strings.Count(output.String(), "Preparing tracker uploads..."); got != 1 {
		t.Fatalf("tracker upload progress count = %d, want 1: %q", got, output.String())
	}
	entries := logger.snapshot()
	if len(entries) != 2 || entries[0] != entries[1] || !strings.Contains(entries[0], "lifecycle=running state=running") {
		t.Fatalf("workflow event logs = %#v", entries)
	}
	if len(coreSvc.eventAfters) != 2 || coreSvc.eventAfters[0] != 0 || coreSvc.eventAfters[1] != event.Sequence {
		t.Fatalf("workflow event cursors = %#v", coreSvc.eventAfters)
	}
}
