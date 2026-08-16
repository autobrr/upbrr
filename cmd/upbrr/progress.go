// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliProgressLogState struct {
	lastPercent int
	lastLog     time.Time
}

type cliWorkflowLogState struct {
	command   string
	phase     string
	status    api.StageStatus
	progress  int
	completed int
	total     int
	message   string
	logged    bool
}

type cliWorkflowEventLogState struct {
	workflowID   api.WorkflowID
	operationID  api.WorkflowOperationID
	lastSequence uint64
	loggedEvents map[string]struct{}
}

func withCLIUploadProgressLogger(ctx context.Context, logger api.Logger) context.Context {
	if logger == nil {
		return ctx
	}

	var mu sync.Mutex
	states := make(map[string]cliProgressLogState)

	return api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		if strings.TrimSpace(update.Task) != "torrent" {
			return
		}
		logCLIProgress(logger, update, states, &mu)
	})
}

func logCLIProgress(logger api.Logger, update api.UploadProgressUpdate, states map[string]cliProgressLogState, mu *sync.Mutex) {
	if logger == nil {
		return
	}

	key := update.SourcePath + "\x00" + update.Tracker + "\x00" + update.Task
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()

	state := states[key]
	if !shouldRenderCLIProgress(update, state, now) {
		return
	}

	if progressStatusFinal(update.Status) {
		delete(states, key)
	} else {
		state.lastPercent = update.Percent
		state.lastLog = now
		states[key] = state
	}

	tracker := strings.ToUpper(strings.TrimSpace(update.Tracker))
	if tracker == "" {
		tracker = "none"
	}
	status := strings.ToLower(strings.TrimSpace(update.Status))
	if status == "" {
		status = "unknown"
	}
	message := strings.TrimSpace(update.Message)
	if message == "" {
		message = status
	}
	format := "torrent: tracker=%s state=%s progress=%d completed=%d total=%d rate_mib=%.1f message=%s"
	if status == "failed" {
		logger.Warnf(format, tracker, status, update.Percent, update.CompletedPieces, update.TotalPieces, update.HashRateMiB, message)
		return
	}
	logger.Infof(format, tracker, status, update.Percent, update.CompletedPieces, update.TotalPieces, update.HashRateMiB, message)
}

func shouldRenderCLIProgress(update api.UploadProgressUpdate, state cliProgressLogState, now time.Time) bool {
	status := strings.ToLower(strings.TrimSpace(update.Status))
	if progressStatusFinal(status) {
		return true
	}
	if update.TotalPieces <= 0 {
		return true
	}

	if state.lastLog.IsZero() {
		return true
	}
	if update.Percent >= state.lastPercent+5 || now.Sub(state.lastLog) >= 10*time.Second {
		return true
	}
	return false
}

func progressStatusFinal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed":
		return true
	default:
		return false
	}
}

func logCLIWorkflowOperation(logger api.Logger, operation api.WorkflowOperationStatus, state *cliWorkflowLogState) {
	if logger == nil || state == nil {
		return
	}
	command := strings.TrimSpace(operation.Command)
	if command == "" {
		command = string(operation.Operation)
	}
	if command == "" {
		command = "unknown"
	}
	phase := strings.TrimSpace(operation.Phase)
	if phase == "" {
		phase = "none"
	}
	message := strings.TrimSpace(operation.Message)
	if message == "" {
		message = string(operation.Status)
	}
	next := cliWorkflowLogState{
		command:   command,
		phase:     phase,
		status:    operation.Status,
		progress:  operation.Progress,
		completed: operation.Completed,
		total:     operation.Total,
		message:   message,
		logged:    true,
	}
	if *state == next {
		return
	}
	*state = next
	format := "workflow: command=%s phase=%s state=%s progress=%d completed=%d total=%d message=%s"
	logger.Debugf(format, command, phase, operation.Status, operation.Progress, operation.Completed, operation.Total, message)
}

func logCLIWorkflowEvents(logger api.Logger, events []api.WorkflowEvent, state *cliWorkflowEventLogState) {
	if state == nil {
		return
	}
	for _, event := range events {
		if event.Sequence <= state.lastSequence {
			continue
		}
		message := strings.TrimSpace(event.Message)
		if message == "" {
			message = string(event.State)
		}
		format := "workflow: command=%s phase=%s scope=%s scope_id=%s lifecycle=%s state=%s disposition=%s completed=%d total=%d code=%s recovery=%s message=%s"
		args := []any{
			event.Command,
			event.Phase,
			event.Scope,
			event.ScopeID,
			event.Lifecycle,
			event.State,
			event.Disposition,
			event.Completed,
			event.Total,
			event.FailureCode,
			event.Recovery,
			message,
		}
		key := fmt.Sprintf(format, args...)
		if _, logged := state.loggedEvents[key]; logged {
			state.lastSequence = event.Sequence
			continue
		}
		if state.loggedEvents == nil {
			state.loggedEvents = make(map[string]struct{})
		}
		state.loggedEvents[key] = struct{}{}
		if logger != nil {
			logger.Debugf(format, args...)
		}
		state.lastSequence = event.Sequence
	}
}

func printCLIWorkflowDupeProgress(output io.Writer, update api.DupeProgressUpdate) {
	if output == nil || update.Completed <= 0 || update.Total <= 0 {
		return
	}
	lineEnd := "\r"
	if update.Completed >= update.Total {
		lineEnd = "\n"
	}
	_, _ = fmt.Fprintf(output, "Dupe checking: %d/%d%s", update.Completed, update.Total, lineEnd)
}

func printCLIWorkflowProgress(output io.Writer, operation api.OperationKind) {
	if output == nil {
		return
	}
	message := ""
	if operation == api.OperationKindPreparation {
		message = "Preparing release..."
	}
	if operation == api.OperationKindUploadDryRun || operation == api.OperationKindUploadExecute {
		message = "Preparing tracker uploads..."
	}
	if message != "" {
		_, _ = fmt.Fprintln(output, message)
	}
}
