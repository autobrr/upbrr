// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

const sourceVerificationProgressStep = 5

// sourceVerificationProgressReporter projects private source hashing progress
// into frontend-safe operation telemetry. It deliberately excludes file paths.
func sourceVerificationProgressReporter(ctx context.Context) preparedrelease.SourceIdentityProgressReporter {
	lastPercent := -sourceVerificationProgressStep
	return func(progress preparedrelease.SourceIdentityProgress) {
		completed := max(progress.CompletedBytes, 0)
		total := max(progress.TotalBytes, 0)
		if total > 0 {
			completed = min(completed, total)
		}
		percent := 0
		if total > 0 {
			percent = int(float64(completed) / float64(total) * 100)
		}
		if completed < total && percent < lastPercent+sourceVerificationProgressStep {
			return
		}
		lastPercent = percent
		update := api.NewPreparationProgressUpdate(
			api.PreparationPhaseSourceInspection,
			api.PreparationProgressRunning,
			"Verifying source content.",
		)
		update.CompletedBytes = completed
		update.TotalBytes = total
		api.EmitPreparationProgress(ctx, update)
	}
}
