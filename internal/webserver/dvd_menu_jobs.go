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

type acceptedDVDCapture interface {
	CaptureAcceptedDVDMenus(context.Context, api.MediaPlanInput) (api.DVDMenuCaptureResult, error)
}

type webDVDRunner struct {
	core     acceptedDVDCapture
	target   PreparedGenerationTransfer
	seed     preparedrelease.Seed
	expected api.ReleaseRef
}

func (r webDVDRunner) CaptureAcceptedDVDMenus(ctx context.Context, input api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
	ref, err := r.target.ImportReleaseSeed(ctx, r.seed)
	if err != nil {
		return api.DVDMenuCaptureResult{}, fmt.Errorf("import canonical generation: %w", err)
	}
	if ref != r.expected {
		return api.DVDMenuCaptureResult{}, errors.New("import canonical generation: unexpected release reference")
	}
	result, err := r.core.CaptureAcceptedDVDMenus(ctx, input)
	if err != nil {
		return api.DVDMenuCaptureResult{}, fmt.Errorf("web: capture accepted DVD menus: %w", err)
	}
	return result, nil
}

// CaptureDVDMenus executes one exact-generation media operation. Request
// cancellation reaches Core and the operation is not retained as a Job.
func (b *Backend) CaptureDVDMenus(
	ctx context.Context,
	release api.ReleaseRef,
) (api.DVDMenuCaptureResult, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.DVDMenuCaptureResult{}, err
	}
	if !rt.capabilities.PreparedGenerationReady() {
		return api.DVDMenuCaptureResult{}, ErrPreparedGenerationUnavailable
	}
	if ctx == nil {
		return api.DVDMenuCaptureResult{}, errors.New("request context is required")
	}
	expectedRef, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return api.DVDMenuCaptureResult{}, err
	}
	seed, err := rt.capabilities.PreparedGenerationTransfer.ExportReleaseSeed(ctx, expectedRef)
	if err != nil {
		return api.DVDMenuCaptureResult{}, fmt.Errorf("DVD menu capture exact generation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return api.DVDMenuCaptureResult{}, fmt.Errorf("DVD menu capture canceled before setup: %w", err)
	}

	runCapabilities, runOwner, runLogger, err := b.buildRunCoreFromSnapshot(ctx, rt, runOptions{})
	if err != nil {
		return api.DVDMenuCaptureResult{}, err
	}
	defer closeJobResources(runOwner, runLogger)
	if err := ctx.Err(); err != nil {
		return api.DVDMenuCaptureResult{}, fmt.Errorf("DVD menu capture canceled after setup: %w", err)
	}
	if !runCapabilities.PreparedDVDReady() {
		return api.DVDMenuCaptureResult{}, ErrPreparedDVDUnavailable
	}
	result, err := (webDVDRunner{
		core:     runCapabilities.DVD,
		target:   runCapabilities.PreparedGenerationTransfer,
		seed:     seed,
		expected: expectedRef,
	}).CaptureAcceptedDVDMenus(ctx, api.MediaPlanInput{Release: expectedRef})
	if err != nil {
		return api.DVDMenuCaptureResult{}, err
	}
	return result, nil
}

// ListDVDMenuScreenshots returns persisted manual and automatic menu images for a prepared DVD.
func (b *Backend) ListDVDMenuScreenshots(
	release api.ReleaseRef,
) ([]api.ScreenshotImage, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	dvdMenuCore, err := rt.dvdMenuCore()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return nil, err
	}
	return wrapWebResult(dvdMenuCore.ListAcceptedDVDMenuScreenshots(ctx, api.MediaPlanInput{Release: ref}))
}

// DeleteDVDMenuScreenshot removes one managed DVD menu image and its prepared-release reference.
func (b *Backend) DeleteDVDMenuScreenshot(release api.ReleaseRef, imagePath string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	dvdMenuCore, err := rt.dvdMenuCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return err
	}
	return wrapWebError(dvdMenuCore.DeleteAcceptedDVDMenuScreenshot(ctx, api.MediaPlanInput{Release: ref}, imagePath))
}
