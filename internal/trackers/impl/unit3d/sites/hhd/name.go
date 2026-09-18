// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/hhd/v3", trackers.StructuredNamePolicy{Defaults: applyHHDNameDefaults})
}

func applyHHDNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyHHDTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if err := editor.Omit(api.NameRoleEdition); err != nil {
		return fmt.Errorf("omit HHD edition: %w", err)
	}
	if isHHDFullDisc(meta) {
		if err := insertHHDDiscDistributor(editor, meta); err != nil {
			return err
		}
	}
	return nil
}

func applyHHDTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) || meta.ProviderMetadata.TVDB == nil {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if !hhdMatchesTVDBTitle(editor, evidence.CanonicalName) {
		return nil
	}
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move HHD alternate title before year: %w", err)
	}
	if !evidence.IncludeYear {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit HHD TVDB year: %w", err)
		}
	}
	if !evidence.IncludeLocale || strings.TrimSpace(evidence.Locale) == "" {
		return nil
	}
	anchor := api.NameRoleAlternateTitle
	if alternate, ok := editor.Component(anchor); !ok || !alternate.Present {
		anchor = api.NameRoleTitle
	}
	if err := editor.InsertAfter(api.NameRoleLocale, evidence.Locale, anchor); err != nil {
		return fmt.Errorf("insert HHD TVDB locale: %w", err)
	}
	return nil
}

func hhdMatchesTVDBTitle(editor *trackers.NameEditor, title string) bool {
	component, ok := editor.Component(api.NameRoleTitle)
	return ok && component.Present && !component.Manual && strings.TrimSpace(title) != "" &&
		strings.EqualFold(strings.Join(strings.Fields(component.Value), " "), strings.Join(strings.Fields(title), " "))
}

func insertHHDDiscDistributor(editor *trackers.NameEditor, meta api.UploadSubject) error {
	distributor := strings.TrimSpace(trackers.PreferredDistributor(meta, meta.Distributor))
	if distributor == "" {
		return nil
	}
	if resolution, ok := editor.Component(api.NameRoleResolution); ok && resolution.Present {
		if err := editor.InsertAfter(api.NameRoleDistributor, distributor, api.NameRoleResolution); err != nil {
			return fmt.Errorf("insert HHD disc distributor after resolution: %w", err)
		}
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleDistributor, distributor, api.NameRoleRegion); err != nil {
		return fmt.Errorf("insert HHD disc distributor before region: %w", err)
	}
	return nil
}

func isHHDFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}
