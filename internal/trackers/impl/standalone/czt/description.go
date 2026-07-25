// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package czt implements uploads to CZTeam (CZT) via its dedicated JSON
// endpoint takeupload_api.php.
//
// Unlike most impls in this repo CZTeam is not a UNIT3D site and does not need a
// cookie jar: the user's passkey authenticates the multipart POST. The endpoint
// returns the registered .torrent inline as base64, already personalized with
// the uploader's announce passkey and source=CzT.
package czt

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// uploadDescriptionAssets uses caller-prepared assets when available, falling
// back to local resolution and an empty asset set on resolution failure.
func uploadDescriptionAssets(_ context.Context, req trackers.PreparationInput) trackers.DescriptionAssets {
	if req.Assets != nil {
		return *req.Assets
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		return trackers.DescriptionAssets{}
	}
	return assets
}

// buildDescription assembles the CZTeam `user_descr` body: the (possibly
// user-edited) description text followed by a BBCode screenshot block. Kept as a
// separate function so definition.BuildDescription can drive the description
// builder UI with the same output.
func buildDescription(_ trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	// A "final" description is the already-assembled body (saved override or
	// canonical group description) with screenshots embedded; the resolver does
	// not clear assets.Screenshots here, so re-appending would duplicate them.
	// Use it verbatim, matching the assets.Final convention other impls follow.
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	parts := make([]string, 0, 2)
	if body := strings.TrimSpace(assets.Description); body != "" {
		parts = append(parts, body)
	}
	if shots := bbcodeScreenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// bbcodeScreenshotBlock renders at most two raw screenshot image URLs. CZTeam's
// formatter accepts plain [img] tags here; linked/thumbnail URLs are ignored.
func bbcodeScreenshotBlock(images []api.ScreenshotImage) string {
	parts := make([]string, 0, 2)
	for _, image := range images {
		raw := strings.TrimSpace(image.RawURL)
		if raw == "" {
			continue
		}
		parts = append(parts, "[img]"+raw+"[/img]")
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, "\n")
}
