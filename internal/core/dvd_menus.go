// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

// DVDMenuCapability reports the pure-Go engine version and the external
// FFmpeg dvdvideo menu capability without returning executable paths.
func (c *Core) DVDMenuCapability(ctx context.Context) (api.DVDMenuEngineInfo, error) {
	return c.media.dvdMenuCapability(ctx)
}

// CaptureDVDMenus captures and persists bounded menu screenshots for one DVD.
// WebUI requests require a prepared metadata cache; CLI requests prepare metadata
// directly. Existing automatic captures are replaced only after rendering.
func (c *Core) CaptureDVDMenus(ctx context.Context, req api.Request) (api.DVDMenuCaptureResult, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return api.DVDMenuCaptureResult{}, err
	}
	return c.CaptureAcceptedDVDMenus(ctx, api.MediaPlanInput{Release: ref})
}

// CaptureAcceptedDVDMenus captures from one exact prepared generation.
func (c *Core) CaptureAcceptedDVDMenus(ctx context.Context, input api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
	result, err := c.media.captureAcceptedDVDMenus(ctx, input)
	return classifyOperationResult(api.OperationKindMedia, result, err)
}

// ListDVDMenuScreenshots lists persisted manual and generated menu screenshots
// for one prepared release.
func (c *Core) ListDVDMenuScreenshots(ctx context.Context, req api.Request) ([]api.ScreenshotImage, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return nil, err
	}
	return c.ListAcceptedDVDMenuScreenshots(ctx, api.MediaPlanInput{Release: ref})
}

// ListAcceptedDVDMenuScreenshots lists menu images for one exact generation.
func (c *Core) ListAcceptedDVDMenuScreenshots(ctx context.Context, input api.MediaPlanInput) ([]api.ScreenshotImage, error) {
	result, err := c.media.listAcceptedDVDMenuScreenshots(ctx, input)
	return classifyOperationResult(api.OperationKindMedia, result, err)
}

// DeleteDVDMenuScreenshot removes one owned menu screenshot and its local
// records for a prepared release. Remote-host assets are not deleted.
func (c *Core) DeleteDVDMenuScreenshot(ctx context.Context, req api.Request, imagePath string) error {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return err
	}
	return c.DeleteAcceptedDVDMenuScreenshot(ctx, api.MediaPlanInput{Release: ref}, imagePath)
}

// DeleteAcceptedDVDMenuScreenshot deletes one menu image for an exact generation.
func (c *Core) DeleteAcceptedDVDMenuScreenshot(ctx context.Context, input api.MediaPlanInput, imagePath string) error {
	return classifyOperationError(api.OperationKindMedia, c.media.deleteAcceptedDVDMenuScreenshot(ctx, input, imagePath))
}
