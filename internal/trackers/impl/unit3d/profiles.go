// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// SiteProfile contains site-owned Unit3D taxonomy and preparation callbacks.
type SiteProfile struct {
	// InputSchema declares site-specific controls using finalized prepared facts.
	InputSchema func(api.UploadSubject) *api.TrackerQuestionnaire
	// InputReadiness evaluates site-specific required evidence without prompting.
	InputReadiness func(api.UploadSubject) []api.InputReadinessFieldOutcome
	// BuildName optionally overrides the generic Unit3D release-name builder.
	BuildName func(meta api.UploadSubject, cfg config.TrackerConfig) string
	// BuildNameVersion identifies a custom BuildName implementation.
	BuildNameVersion string
	// BuildDescription optionally renders site-specific tracker markup.
	BuildDescription func(ctx context.Context, meta api.UploadSubject, appConfig config.Config, trackerConfig config.TrackerConfig, logger api.Logger, keptDescription string, menuImages []api.ScreenshotImage, screenshots []api.ScreenshotImage) (string, error)
	// ResolveKeywords optionally maps prepared metadata to Unit3D keywords.
	ResolveKeywords func(meta api.UploadSubject) string
	// ResolveTypeID optionally maps prepared metadata to a site type identifier.
	ResolveTypeID func(meta api.UploadSubject) string
	// ResolveResolutionID optionally maps prepared metadata to a site resolution identifier.
	ResolveResolutionID func(meta api.UploadSubject) string
	// ResolveCategoryID optionally selects the upload category from finalized facts.
	// A custom resolver requires CategoryIDs; empty or out-of-family IDs are unsupported.
	ResolveCategoryID func(meta api.UploadSubject) string
	// CategoryIDs returns the complete native family for a canonical movie or TV category.
	// Upload validation checks membership; duplicate search uses every returned ID,
	// including related subtype, language and pack categories. The callback must be pure
	// and return positive integer IDs; empty or invalid families are unsupported.
	// Without either category callback, movies use category 1 and TV uses category 2.
	CategoryIDs func(api.CanonicalCategory) []string
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
