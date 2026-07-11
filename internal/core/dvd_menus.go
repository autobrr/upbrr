// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// DVDMenuCapability reports the pure-Go engine version and the external
// FFmpeg dvdvideo menu capability without returning executable paths.
func (c *Core) DVDMenuCapability(ctx context.Context) (api.DVDMenuEngineInfo, error) {
	if c == nil || c.services.DVDMenus == nil {
		return api.DVDMenuEngineInfo{}, errors.New("core: DVD menu service not configured")
	}
	return wrapCoreResult(c.services.DVDMenus.Capability(ctx))
}

// CaptureDVDMenus captures and persists bounded menu screenshots for one DVD.
// GUI requests require a prepared metadata cache; CLI requests prepare metadata
// directly. Existing automatic captures are replaced only after rendering.
func (c *Core) CaptureDVDMenus(ctx context.Context, req api.Request) (api.DVDMenuCaptureResult, error) {
	if c.services.DVDMenus == nil {
		return api.DVDMenuCaptureResult{}, errors.New("core: DVD menu service not configured")
	}
	meta, err := c.resolveDVDMenuMetadata(ctx, req)
	if err != nil {
		return api.DVDMenuCaptureResult{}, err
	}
	return wrapCoreResult(c.services.DVDMenus.Capture(ctx, meta, c.cfg.ScreenshotHandling.ResolvedMaxMenuItems()))
}

// ListDVDMenuScreenshots lists persisted manual and generated menu screenshots
// for one prepared release.
func (c *Core) ListDVDMenuScreenshots(ctx context.Context, req api.Request) ([]api.ScreenshotImage, error) {
	if c.services.DVDMenus == nil {
		return nil, errors.New("core: DVD menu service not configured")
	}
	meta, err := c.resolveDVDMenuMetadata(ctx, req)
	if err != nil {
		return nil, err
	}
	return wrapCoreResult(c.services.DVDMenus.List(ctx, meta))
}

// DeleteDVDMenuScreenshot removes one owned menu screenshot and its local
// records for a prepared release. Remote-host assets are not deleted.
func (c *Core) DeleteDVDMenuScreenshot(ctx context.Context, req api.Request, imagePath string) error {
	if c.services.DVDMenus == nil {
		return errors.New("core: DVD menu service not configured")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	meta, err := c.resolveDVDMenuMetadata(ctx, req)
	if err != nil {
		return err
	}
	return wrapCoreError(c.services.DVDMenus.Delete(ctx, meta, imagePath))
}

// resolveDVDMenuMetadata requires exactly one prepared source. GUI requests use
// the preview cache; non-GUI requests prepare metadata through the service.
func (c *Core) resolveDVDMenuMetadata(ctx context.Context, req api.Request) (api.PreparedMetadata, error) {
	path, err := c.resolveSinglePreparedMetaPath(ctx, req.Paths)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	if req.Mode == api.ModeGUI {
		if cached, ok, cacheErr := c.resolveGUICachedPreparedMeta(ctx, req, path); cacheErr != nil {
			return api.PreparedMetadata{}, cacheErr
		} else if ok {
			return cached, nil
		}
		return api.PreparedMetadata{}, errors.New("core: DVD menu operation requires metadata preview")
	}
	if c.services.Metadata == nil {
		return api.PreparedMetadata{}, errors.New("core: metadata service not configured")
	}
	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	singleReq := req
	singleReq.Paths = []string{path}
	singleReq.Options = options
	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("core: %w", err)
	}
	return meta, nil
}
