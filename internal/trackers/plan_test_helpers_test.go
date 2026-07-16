// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

type testDescriptionPreparer interface {
	prepareDescription(context.Context, PreparationInput) (DescriptionResult, error)
}

type testDryRunPreparer interface {
	prepareDryRun(context.Context, PreparationInput) (api.TrackerDryRunEntry, error)
}

type testSubmitter interface {
	submit(context.Context, PreparationInput) (api.UploadSummary, error)
}

func prepareTestDefinition(ctx context.Context, input PreparationInput, definition any) (TrackerPlan, *PreparationFailure) {
	description := func(context.Context, PreparationInput) (DescriptionResult, error) {
		return DescriptionResult{Group: input.Tracker}, nil
	}
	if builder, ok := definition.(testDescriptionPreparer); ok {
		description = builder.prepareDescription
	}
	dryRun := func(context.Context, PreparationInput) (api.TrackerDryRunEntry, error) {
		return api.TrackerDryRunEntry{Tracker: input.Tracker, Status: "ready"}, nil
	}
	if builder, ok := definition.(testDryRunPreparer); ok {
		dryRun = builder.prepareDryRun
	}
	submit := func(context.Context, PreparationInput) (api.UploadSummary, error) {
		return api.UploadSummary{}, nil
	}
	if uploader, ok := definition.(testSubmitter); ok {
		submit = uploader.submit
	}
	return PrepareAdapter(ctx, input, description, dryRun, submit)
}
