// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"errors"
)

// UploadProgressUpdate describes one source- and tracker-scoped upload progress event.
type UploadProgressUpdate struct {
	// SourcePath is the host filesystem release path.
	SourcePath string `json:"sourcePath"`
	// Tracker identifies the tracker when the task is tracker-specific.
	Tracker string `json:"tracker"`
	// Task is the stable progress operation identifier.
	Task string `json:"task"`
	// Status is the stable lifecycle state for Task.
	Status string `json:"status"`
	// Message contains sanitized user-facing progress detail.
	Message string `json:"message"`
	// CompletedPieces is the number of torrent pieces hashed so far.
	CompletedPieces int `json:"completedPieces"`
	// TotalPieces is the total number of torrent pieces to hash.
	TotalPieces int `json:"totalPieces"`
	// Percent is integer completion percentage from 0 through 100.
	Percent int `json:"percent"`
	// HashRateMiB is the current hashing throughput in MiB per second.
	HashRateMiB float64 `json:"hashRateMiB"`
	// Timestamp is the event time serialized by the producer.
	Timestamp string `json:"timestamp"`
}

// UploadProgressReporter receives synchronous progress notifications from an upload context.
type UploadProgressReporter func(update UploadProgressUpdate)

// TrackerLocalUploadError distinguishes attributable tracker failures from operation-wide errors.
type TrackerLocalUploadError interface {
	error
	// TrackerLocalUploadFailures returns tracker names whose attributable work failed.
	TrackerLocalUploadFailures() []string
}

// IsTrackerLocalUploadError reports whether err contains tracker-local failure classification.
func IsTrackerLocalUploadError(err error) bool {
	var local TrackerLocalUploadError
	return errors.As(err, &local)
}

// TrackerLocalUploadFailureNames returns a defensive tracker list from a classified error.
func TrackerLocalUploadFailureNames(err error) []string {
	var local TrackerLocalUploadError
	if !errors.As(err, &local) {
		return nil
	}
	return append([]string(nil), local.TrackerLocalUploadFailures()...)
}

type uploadProgressReporterKey struct{}

// WithUploadProgressReporter returns a context carrying reporter; nil reporters leave the context unchanged.
func WithUploadProgressReporter(ctx context.Context, reporter UploadProgressReporter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadProgressReporterKey{}, reporter)
}

// EmitUploadProgress synchronously invokes the reporter attached to ctx, when present.
func EmitUploadProgress(ctx context.Context, update UploadProgressUpdate) {
	if ctx == nil {
		return
	}
	reporter, _ := ctx.Value(uploadProgressReporterKey{}).(UploadProgressReporter)
	if reporter == nil {
		return
	}
	reporter(update)
}
