// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// SiteProfile contains optional site-owned Unit3D payload callbacks.
type SiteProfile struct {
	// BuildName optionally overrides the generic Unit3D release-name builder.
	BuildName func(meta api.UploadSubject, cfg config.TrackerConfig) string
	// BuildDescription optionally renders site-specific tracker markup.
	BuildDescription func(ctx context.Context, meta api.UploadSubject, appConfig config.Config, trackerConfig config.TrackerConfig, logger api.Logger, keptDescription string, menuImages []api.ScreenshotImage, screenshots []api.ScreenshotImage) (string, error)
	// ResolveKeywords optionally maps prepared metadata to Unit3D keywords.
	ResolveKeywords func(meta api.UploadSubject) string
	// ResolveTypeID optionally maps prepared metadata to a site type identifier.
	ResolveTypeID func(meta api.UploadSubject) string
	// ResolveResolutionID optionally maps prepared metadata to a site resolution identifier.
	ResolveResolutionID func(meta api.UploadSubject) string
	// ResolveCategoryID optionally maps prepared metadata to a site category identifier.
	ResolveCategoryID func(meta api.UploadSubject) string
	// ApplyAdditionalPayload appends site-owned fields to a prepared payload.
	ApplyAdditionalPayload func(req trackers.PreparationInput, data map[string]string)
	// FinalizeDescription applies final site-owned description transformations.
	FinalizeDescription func(description string, meta api.UploadSubject) string
}

func firstSiteProfile(profiles []SiteProfile) SiteProfile {
	if len(profiles) == 0 {
		return SiteProfile{}
	}
	return profiles[0]
}
