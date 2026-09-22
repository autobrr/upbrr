// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cbr

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/cbr/v3", trackers.StructuredNamePolicy{
		Defaults: applyCBRNameDefaults,
	})
}

func applyCBRNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, cfg config.TrackerConfig) error {
	if err := unit3d.ApplyLocalizedNameDefaults(editor, meta, cfg); err != nil {
		return fmt.Errorf("apply CBR localized defaults: %w", err)
	}
	return applyCBRTVDBDisambiguation(editor, meta)
}

// applyCBRTVDBDisambiguation restores the year only when current TVDB evidence
// proves that the series title collides with another series. CBR forbids AKA,
// which the shared localized policy has already removed before this runs.
func applyCBRTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil ||
		!meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, ok := editor.Component(api.NameRoleTitle)
	if !ok || !title.Present || title.Manual || strings.TrimSpace(evidence.CanonicalName) == "" ||
		!strings.EqualFold(strings.Join(strings.Fields(title.Value), " "), strings.Join(strings.Fields(evidence.CanonicalName), " ")) {
		return nil
	}
	if !evidence.IncludeYear || evidence.SeriesYear <= 0 {
		return nil
	}
	if err := editor.Set(api.NameRoleYear, strconv.Itoa(evidence.SeriesYear)); err != nil {
		return fmt.Errorf("set CBR TVDB year: %w", err)
	}
	if err := editor.Include(api.NameRoleYear); err != nil {
		return fmt.Errorf("include CBR TVDB year: %w", err)
	}
	anchor := api.NameRoleTitle
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		if err := editor.InsertAfter(api.NameRoleLocale, strings.TrimSpace(evidence.Locale), anchor); err != nil {
			return fmt.Errorf("insert CBR TVDB locale: %w", err)
		}
		anchor = api.NameRoleLocale
	}
	if err := editor.MoveAfter(api.NameRoleYear, anchor); err != nil {
		return fmt.Errorf("move CBR TVDB year: %w", err)
	}
	return nil
}
