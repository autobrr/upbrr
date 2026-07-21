// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"strings"
)

// ImageUploadProgressStatus describes the latest advisory state of one host attempt.
type ImageUploadProgressStatus string

const (
	// ImageUploadProgressRunning marks a host attempt that still has pending images.
	ImageUploadProgressRunning ImageUploadProgressStatus = "running"
	// ImageUploadProgressCompleted marks a host attempt whose images are all ready.
	ImageUploadProgressCompleted ImageUploadProgressStatus = "completed"
	// ImageUploadProgressFailed marks a host attempt with one or more failed images.
	ImageUploadProgressFailed ImageUploadProgressStatus = "failed"
)

// ImageUploadProgressTarget identifies one host/scope attempt and its immutable
// presentation metadata. Total includes reused images as well as network uploads.
type ImageUploadProgressTarget struct {
	// AttemptID is stable for the host/scope pair within one command.
	AttemptID string
	// Host is the normalized configured image-host name.
	Host string
	// UsageScope distinguishes global and tracker-owned upload records.
	UsageScope string
	// Trackers lists upload targets served by this host attempt.
	Trackers []string
	// Fallback reports whether this attempt follows an earlier host failure.
	Fallback bool
	// Total is the number of selected images required on this host.
	Total int
	// Reused is the subset already available from persisted host records.
	Reused int
}

// ImageUploadProgressUpdate is one frontend-safe absolute snapshot for a host
// attempt. CorrelationID and Timestamp are injected by the WebUI boundary.
type ImageUploadProgressUpdate struct {
	// CorrelationID binds the update to one frontend upload command.
	CorrelationID string `json:"correlationID"`
	// AttemptID identifies the host/scope row updated by this snapshot.
	AttemptID string `json:"attemptID"`
	// Host is the normalized configured image-host name.
	Host string `json:"host"`
	// UsageScope distinguishes global and tracker-owned upload records.
	UsageScope string `json:"usageScope"`
	// Trackers lists upload targets served by this host attempt.
	Trackers []string `json:"trackers"`
	// Fallback reports whether this attempt follows an earlier host failure.
	Fallback bool `json:"fallback"`
	// Completed counts succeeded, failed, and reused images in terminal states.
	Completed int `json:"completed"`
	// Total is the number of selected images required on this host.
	Total int `json:"total"`
	// Succeeded counts completed network uploads in this attempt.
	Succeeded int `json:"succeeded"`
	// Failed counts completed network uploads that did not produce a usable link.
	Failed int `json:"failed"`
	// Reused counts images satisfied by existing persisted host records.
	Reused int `json:"reused"`
	// Status is the latest advisory state for this host attempt.
	Status ImageUploadProgressStatus `json:"status"`
	// Message provides frontend-safe detail about the latest transition.
	Message string `json:"message"`
	// Timestamp is an RFC3339 timestamp injected by the WebUI boundary.
	Timestamp string `json:"timestamp"`
}

// ImageUploadProgressReporter receives advisory image-host progress snapshots.
type ImageUploadProgressReporter func(update ImageUploadProgressUpdate)

type imageUploadProgressReporterKey struct{}
type imageUploadProgressTargetKey struct{}

// WithImageUploadProgressReporter attaches an optional reporter to ctx.
func WithImageUploadProgressReporter(ctx context.Context, reporter ImageUploadProgressReporter) context.Context {
	if ctx == nil || reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, imageUploadProgressReporterKey{}, reporter)
}

// WithImageUploadProgressTarget attaches immutable metadata for one host attempt.
func WithImageUploadProgressTarget(ctx context.Context, target ImageUploadProgressTarget) context.Context {
	if ctx == nil {
		return ctx
	}
	target.AttemptID = strings.TrimSpace(target.AttemptID)
	target.Host = strings.ToLower(strings.TrimSpace(target.Host))
	target.UsageScope = strings.TrimSpace(target.UsageScope)
	target.Trackers = append([]string(nil), target.Trackers...)
	target.Total = max(0, target.Total)
	target.Reused = max(0, min(target.Reused, target.Total))
	return context.WithValue(ctx, imageUploadProgressTargetKey{}, target)
}

// ImageUploadProgressTargetFromContext returns the current host attempt when present.
func ImageUploadProgressTargetFromContext(ctx context.Context) (ImageUploadProgressTarget, bool) {
	if ctx == nil {
		return ImageUploadProgressTarget{}, false
	}
	target, ok := ctx.Value(imageUploadProgressTargetKey{}).(ImageUploadProgressTarget)
	return target, ok
}

// EmitImageUploadProgress reports one absolute snapshot when a reporter is installed.
func EmitImageUploadProgress(ctx context.Context, update ImageUploadProgressUpdate) {
	if ctx == nil {
		return
	}
	reporter, _ := ctx.Value(imageUploadProgressReporterKey{}).(ImageUploadProgressReporter)
	if reporter != nil {
		reporter(update)
	}
}
