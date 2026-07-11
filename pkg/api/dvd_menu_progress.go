// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "context"

// DVDMenuProgressUpdate reports bounded capture progress without source paths
// or disc-identifying material.
type DVDMenuProgressUpdate struct {
	// Phase is a stable capture stage such as preflight, discovering, capturing, or persisting.
	Phase string `json:"phase"`
	// Message is a user-facing summary of the current phase.
	Message string `json:"message"`
	// DiscoveredMenus is the current structural inventory count.
	DiscoveredMenus int `json:"discoveredMenus"`
	// VisitedStates is the current count of evaluated VM states.
	VisitedStates int `json:"visitedStates"`
	// VisitedButtons is the current count of evaluated button commands.
	VisitedButtons int `json:"visitedButtons"`
	// CapturedCount is the number of rendered menu images so far.
	CapturedCount int `json:"capturedCount"`
	// WarningCount is the number of distinct coverage warnings so far.
	WarningCount int `json:"warningCount"`
}

// DVDMenuCaptureSnapshot reports frontend-visible state for one capture job.
type DVDMenuCaptureSnapshot struct {
	// JobID is the opaque identifier used for polling and cancellation.
	JobID string `json:"jobID"`
	// SourcePath is the host filesystem path associated with the prepared metadata.
	SourcePath string `json:"sourcePath"`
	// Status is queued, running, completed, failed, or canceled.
	Status string `json:"status"`
	// Phase is the current capture stage within Status.
	Phase string `json:"phase"`
	// Message is a user-facing progress or terminal summary.
	Message string `json:"message"`
	// DiscoveredMenus is the latest structural inventory count.
	DiscoveredMenus int `json:"discoveredMenus"`
	// VisitedStates is the latest evaluated VM-state count.
	VisitedStates int `json:"visitedStates"`
	// VisitedButtons is the latest evaluated button-command count.
	VisitedButtons int `json:"visitedButtons"`
	// CapturedCount is the latest rendered or persisted image count.
	CapturedCount int `json:"capturedCount"`
	// WarningCount is the latest distinct coverage-warning count.
	WarningCount int `json:"warningCount"`
	// Result is populated with the final or partial engine result when available.
	Result DVDMenuCaptureResult `json:"result"`
	// Error contains terminal failure text and is empty for successful jobs.
	Error string `json:"error"`
	// StartedAt is an RFC3339 UTC timestamp.
	StartedAt string `json:"startedAt"`
	// FinishedAt is an RFC3339 UTC timestamp, or empty while the job is active.
	FinishedAt string `json:"finishedAt"`
}

// DVDMenuProgressReporter receives capture progress updates.
type DVDMenuProgressReporter func(update DVDMenuProgressUpdate)

type dvdMenuProgressReporterKey struct{}

// WithDVDMenuProgressReporter returns a child context that reports capture
// progress to reporter. A nil context or reporter is returned unchanged.
func WithDVDMenuProgressReporter(ctx context.Context, reporter DVDMenuProgressReporter) context.Context {
	if ctx == nil || reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, dvdMenuProgressReporterKey{}, reporter)
}

// ReportDVDMenuProgress synchronously sends update to the reporter stored in
// ctx, if any. A nil context is ignored.
func ReportDVDMenuProgress(ctx context.Context, update DVDMenuProgressUpdate) {
	if ctx == nil {
		return
	}
	reporter, _ := ctx.Value(dvdMenuProgressReporterKey{}).(DVDMenuProgressReporter)
	if reporter != nil {
		reporter(update)
	}
}
