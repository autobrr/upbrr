// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	// ErrCanonicalPreparationUnavailable reports a runtime bundle missing the
	// canonical prepare/export/import seam.
	ErrCanonicalPreparationUnavailable = errors.New("canonical preparation capability unavailable")
)

// PrepareGeneration creates or reuses one canonical generation for a
// synchronous operation and returns its exact reference.
func PrepareGeneration(
	ctx context.Context,
	preparer ReleasePreparationCapability,
	request api.Request,
	intent api.PreparationIntent,
) (api.PrepareResult, api.ReleaseRef, error) {
	if capabilityIsNil(preparer) {
		return api.PrepareResult{}, api.ReleaseRef{}, ErrCanonicalPreparationUnavailable
	}
	input, err := api.MapPreparationRequest(request, intent)
	if err != nil {
		return api.PrepareResult{}, api.ReleaseRef{}, fmt.Errorf("map canonical preparation request: %w", err)
	}
	result, err := preparer.PrepareRelease(ctx, input)
	if err != nil {
		return api.PrepareResult{}, api.ReleaseRef{}, fmt.Errorf("prepare canonical generation: %w", err)
	}
	ref := api.ReleaseRef{SourcePath: result.Release.Source.SourcePath, Generation: result.Release.Generation}
	return result, ref, nil
}

// PrepareAndExportGeneration creates or reuses canonical facts and exports one
// exact opaque generation seed before a job is accepted.
func PrepareAndExportGeneration(
	ctx context.Context,
	preparer ReleasePreparationCapability,
	transfer PreparedGenerationTransfer,
	request api.Request,
	intent api.PreparationIntent,
) (api.PrepareResult, preparedrelease.Seed, error) {
	if capabilityIsNil(preparer) || capabilityIsNil(transfer) {
		return api.PrepareResult{}, preparedrelease.Seed{}, ErrCanonicalPreparationUnavailable
	}
	input, err := api.MapPreparationRequest(request, intent)
	if err != nil {
		return api.PrepareResult{}, preparedrelease.Seed{}, fmt.Errorf("map canonical preparation request: %w", err)
	}
	result, err := preparer.PrepareRelease(ctx, input)
	if err != nil {
		return api.PrepareResult{}, preparedrelease.Seed{}, fmt.Errorf("prepare canonical generation: %w", err)
	}
	ref := api.ReleaseRef{SourcePath: result.Release.Source.SourcePath, Generation: result.Release.Generation}
	seed, err := transfer.ExportReleaseSeed(ctx, ref)
	if err != nil {
		return api.PrepareResult{}, preparedrelease.Seed{}, fmt.Errorf("export canonical generation: %w", err)
	}
	return result, seed, nil
}
