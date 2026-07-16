// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package jobs owns the WebUI background job lifecycle.
package jobs

import "github.com/autobrr/upbrr/pkg/api"

const (
	// KindDuplicateCheck identifies retained duplicate-check Jobs.
	KindDuplicateCheck = "duplicate_check"
	// KindTrackerUpload identifies retained tracker-upload Jobs.
	KindTrackerUpload = "tracker_upload"
)

const (
	// StatusQueued indicates that a job or tracker is accepted but has not started.
	StatusQueued = "queued"
	// StatusRunning indicates that work is in progress.
	StatusRunning = "running"
	// StatusCompleted indicates that all requested work succeeded.
	StatusCompleted = "completed"
	// StatusCompletedWithErrors indicates that work finished with at least one tracker failure.
	StatusCompletedWithErrors = "completed_with_errors"
	// StatusFailed indicates that the job engine or runner could not complete the job.
	StatusFailed = "failed"
	// StatusCanceled indicates that cancellation stopped the job or tracker.
	StatusCanceled = "canceled"
)

// DupeCheckTrackerState is the frontend-visible state for one tracker in a duplicate-check job.
type DupeCheckTrackerState struct {
	// Tracker is the normalized uppercase tracker name.
	Tracker string `json:"tracker"`
	// Status is the tracker lifecycle state.
	Status string `json:"status"`
	// Message is sanitized progress or outcome detail suitable for WebUI event delivery.
	Message string `json:"message"`
	// Result is the latest duplicate-check result for the tracker.
	Result api.DupeCheckResult `json:"result"`
	// StartedAt is an RFC3339 timestamp, or empty when work has not started.
	StartedAt string `json:"startedAt"`
	// FinishedAt is an RFC3339 timestamp, or empty while work is active.
	FinishedAt string `json:"finishedAt"`
}

// DupeCheckSnapshot is an immutable frontend payload describing one duplicate-check job.
type DupeCheckSnapshot struct {
	// JobID identifies the job within its owner scope.
	JobID string `json:"jobID"`
	// CorrelationID is the caller-generated opaque start identity.
	CorrelationID string `json:"correlationID"`
	// Release identifies the exact prepared generation accepted for this Job.
	Release api.ReleaseRef `json:"release"`
	// RuntimeGeneration identifies the exact config/resource generation accepted for this job.
	RuntimeGeneration uint64 `json:"runtimeGeneration"`
	// Status is the job lifecycle state.
	Status string `json:"status"`
	// Trackers lists requested trackers first, followed by any dynamically discovered trackers.
	Trackers []DupeCheckTrackerState `json:"trackers"`
	// CompletedCount counts trackers that reached a terminal state.
	CompletedCount int `json:"completedCount"`
	// TotalCount is the greatest known tracker total, including dynamically discovered trackers.
	TotalCount int `json:"totalCount"`
	// Summary contains the accumulated duplicate-check results.
	Summary api.DupeCheckSummary `json:"summary"`
	// Failure is the stable safe terminal failure, when one occurred.
	Failure *api.OperationFailure `json:"failure,omitempty"`
	// StartedAt is the job start time in RFC3339 format.
	StartedAt string `json:"startedAt"`
	// FinishedAt is an RFC3339 timestamp, or empty while the job is active.
	FinishedAt string `json:"finishedAt"`
}

// TrackerUploadTrackerState is the frontend-visible state for one tracker upload.
type TrackerUploadTrackerState struct {
	// Tracker is the tracker name supplied by the caller.
	Tracker string `json:"tracker"`
	// Status is the tracker lifecycle state.
	Status string `json:"status"`
	// Task identifies the current upload task.
	Task string `json:"task"`
	// TaskStatus describes the current task outcome or progress state.
	TaskStatus string `json:"taskStatus"`
	// Message is sanitized progress detail suitable for WebUI event delivery.
	Message string `json:"message"`
	// CompletedPieces is the number of completed hashing or processing pieces.
	CompletedPieces int `json:"completedPieces"`
	// TotalPieces is the total number of hashing or processing pieces.
	TotalPieces int `json:"totalPieces"`
	// Percent is integer completion percentage.
	Percent int `json:"percent"`
	// HashRateMiB is the current hashing throughput in MiB/s.
	HashRateMiB float64 `json:"hashRateMiB"`
	// UploadedCount is the number of successful uploads attributed to this tracker.
	UploadedCount int `json:"uploadedCount"`
	// StartedAt is an RFC3339 timestamp, or empty before tracker work starts.
	StartedAt string `json:"startedAt"`
	// FinishedAt is an RFC3339 timestamp, or empty while tracker work is active.
	FinishedAt string `json:"finishedAt"`
}

// TrackerUploadSnapshot is an immutable frontend payload describing one tracker-upload job.
type TrackerUploadSnapshot struct {
	// JobID identifies the job within its owner scope.
	JobID string `json:"jobID"`
	// CorrelationID is the caller-generated opaque start identity.
	CorrelationID string `json:"correlationID"`
	// RetryOf identifies the prior upload Job when this Job is a linked retry.
	RetryOf string `json:"retryOf,omitempty"`
	// Release identifies the exact prepared generation accepted for this Job.
	Release api.ReleaseRef `json:"release"`
	// RuntimeGeneration identifies the exact config/resource generation accepted for this job.
	RuntimeGeneration uint64 `json:"runtimeGeneration"`
	// Status is the job lifecycle state.
	Status string `json:"status"`
	// CurrentTask is the latest job-wide task label.
	CurrentTask string `json:"currentTask"`
	// CurrentTaskStatus is the latest job-wide task state.
	CurrentTaskStatus string `json:"currentTaskStatus"`
	// CurrentMessage is sanitized progress detail suitable for WebUI event delivery.
	CurrentMessage string `json:"currentMessage"`
	// CurrentCompletedPieces is the latest completed piece count.
	CurrentCompletedPieces int `json:"currentCompletedPieces"`
	// CurrentTotalPieces is the latest total piece count.
	CurrentTotalPieces int `json:"currentTotalPieces"`
	// CurrentPercent is the latest integer completion percentage.
	CurrentPercent int `json:"currentPercent"`
	// CurrentHashRateMiB is the latest hashing throughput in MiB/s.
	CurrentHashRateMiB float64 `json:"currentHashRateMiB"`
	// Trackers contains per-tracker states in request order.
	Trackers []TrackerUploadTrackerState `json:"trackers"`
	// FailedTrackers contains the tracker names eligible for retry.
	FailedTrackers []string `json:"failedTrackers"`
	// UploadedCount is the sum of positive per-tracker upload counts.
	UploadedCount int `json:"uploadedCount"`
	// Failure is the stable safe terminal failure, when one occurred.
	Failure *api.OperationFailure `json:"failure,omitempty"`
	// StartedAt is the job start time in RFC3339 format.
	StartedAt string `json:"startedAt"`
	// FinishedAt is an RFC3339 timestamp, or empty while the job is active.
	FinishedAt string `json:"finishedAt"`
}

// OwnerJobSnapshot is the discriminated owner-listing representation of one retained Job.
type OwnerJobSnapshot struct {
	Kind          string                 `json:"kind"`
	JobID         string                 `json:"jobID"`
	CorrelationID string                 `json:"correlationID"`
	RetryOf       string                 `json:"retryOf,omitempty"`
	Release       api.ReleaseRef         `json:"release"`
	Status        string                 `json:"status"`
	StartedAt     string                 `json:"startedAt"`
	FinishedAt    string                 `json:"finishedAt"`
	Dupe          *DupeCheckSnapshot     `json:"dupe,omitempty"`
	Upload        *TrackerUploadSnapshot `json:"upload,omitempty"`
}
