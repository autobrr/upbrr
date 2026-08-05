// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "context"

// WorkflowProgressUpdate is one frontend-safe absolute stage snapshot used by
// workflow stages that do not have a narrower existing progress contract.
type WorkflowProgressUpdate struct {
	Phase     string
	ItemID    string
	Kind      string
	Label     string
	Status    StageStatus
	Completed int
	Total     int
	Message   string
}

// WorkflowProgressReporter receives generic workflow progress snapshots.
type WorkflowProgressReporter func(WorkflowProgressUpdate)

type workflowProgressReporterKey struct{}

// WithWorkflowProgressReporter attaches a generic workflow progress reporter.
func WithWorkflowProgressReporter(ctx context.Context, reporter WorkflowProgressReporter) context.Context {
	if ctx == nil || reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, workflowProgressReporterKey{}, reporter)
}

// EmitWorkflowProgress reports one generic workflow progress snapshot.
func EmitWorkflowProgress(ctx context.Context, update WorkflowProgressUpdate) {
	if ctx == nil {
		return
	}
	reporter, _ := ctx.Value(workflowProgressReporterKey{}).(WorkflowProgressReporter)
	if reporter != nil {
		reporter(update)
	}
}
