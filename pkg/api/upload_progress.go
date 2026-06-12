// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "context"

type UploadProgressUpdate struct {
	SourcePath      string  `json:"sourcePath"`
	Tracker         string  `json:"tracker"`
	Task            string  `json:"task"`
	Status          string  `json:"status"`
	Message         string  `json:"message"`
	CompletedPieces int     `json:"completedPieces"`
	TotalPieces     int     `json:"totalPieces"`
	Percent         int     `json:"percent"`
	HashRateMiB     float64 `json:"hashRateMiB"`
	Timestamp       string  `json:"timestamp"`
}

type UploadProgressReporter func(update UploadProgressUpdate)

type uploadProgressReporterKey struct{}

func WithUploadProgressReporter(ctx context.Context, reporter UploadProgressReporter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadProgressReporterKey{}, reporter)
}

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
