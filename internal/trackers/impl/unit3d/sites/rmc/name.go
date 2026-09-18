// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	disallowedNameCharsRegex = regexp.MustCompile(`[^A-Za-z0-9 ._+-]+`)
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/rmc/v3", trackers.StructuredNamePolicy{Defaults: applyRMCNameDefaults})
}

func applyRMCNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	tmdb := currentRMCTMDB(meta.SourcePath, meta.Identity, meta.ProviderMetadata)
	if tmdb == nil || strings.TrimSpace(tmdb.Title) == "" {
		return &trackers.NameRuleError{
			Rule:   "unit3d/rmc/v3",
			Role:   api.NameRoleTitle,
			Reason: "current TMDB title is required",
		}
	}
	title := tmdb.Title
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		title = trackers.PreferredTitle(meta, tmdb.Title)
	}
	title = sanitizeName(title)
	if title == "" {
		return &trackers.NameRuleError{
			Rule:   "unit3d/rmc/v3",
			Role:   api.NameRoleTitle,
			Reason: "title has no supported characters",
		}
	}
	if err := editor.Set(api.NameRoleTitle, title); err != nil {
		return fmt.Errorf("set RMC title: %w", err)
	}
	year := tmdb.Year
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		year = trackers.PreferredYear(meta, tmdb.Year)
	}
	if year > 0 {
		if err := editor.Set(api.NameRoleYear, strconv.Itoa(year)); err != nil {
			return fmt.Errorf("set RMC year: %w", err)
		}
	}
	if err := editor.Omit(api.NameRoleAlternateTitle); err != nil {
		return fmt.Errorf("omit RMC alternate title: %w", err)
	}
	for _, role := range editor.PresentRoles() {
		component, ok := editor.Component(role)
		if !ok {
			continue
		}
		value := sanitizeName(component.Value)
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit unsupported RMC component %s: %w", role, err)
			}
			continue
		}
		if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("sanitize RMC component %s: %w", role, err)
		}
	}
	return nil
}

// markerName returns the prepared release name, falling back to its no-tag variant.
func markerName(meta api.UploadSubject) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	return strings.TrimSpace(meta.ReleaseNameNoTag)
}

// currentRMCTMDB returns metadata only when it matches the canonical TMDB ID
// and the current prepared source and identity generation.
func currentRMCTMDB(sourcePath string, identity api.ExternalIdentity, metadata api.SourceScopedMetadata) *api.TMDBMetadata {
	if identity.TMDBID <= 0 || metadata.TMDB == nil || metadata.TMDB.TMDBID != identity.TMDBID || !metadata.IsCurrentFor(sourcePath, identity) {
		return nil
	}
	return metadata.TMDB
}

// sanitizeName removes characters outside RMC's accepted name set and collapses whitespace.
func sanitizeName(name string) string {
	cleaned := disallowedNameCharsRegex.ReplaceAllString(name, "")
	return strings.TrimSpace(strings.Join(strings.Fields(cleaned), " "))
}
