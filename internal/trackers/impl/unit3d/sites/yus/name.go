// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package yus

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/yus/v4", trackers.StructuredNamePolicy{Defaults: applyYUSNameDefaults})
}

func applyYUSNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyYUSTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if edition, ok := editor.Component(api.NameRoleEdition); ok && strings.TrimSpace(edition.Value) != "" {
		if !isYUSCut(edition.Value) {
			if err := editor.Omit(api.NameRoleEdition); err != nil {
				return fmt.Errorf("omit YUS non-cut edition: %w", err)
			}
		} else {
			if err := editor.Include(api.NameRoleEdition); err != nil {
				return fmt.Errorf("include YUS cut: %w", err)
			}
			if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
				if err := editor.MoveBefore(api.NameRoleEdition, api.NameRoleRepack); err != nil {
					return fmt.Errorf("move YUS DVD cut before version: %w", err)
				}
			}
		}
	}
	if isYUSFullDisc(meta) {
		if err := insertYUSDiscDistributor(editor, meta); err != nil {
			return err
		}
	}
	return nil
}

func applyYUSTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) || meta.ProviderMetadata.TVDB == nil {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if !yusMatchesTVDBTitle(editor, evidence.CanonicalName) {
		return nil
	}
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move YUS alternate title before year: %w", err)
	}
	if !evidence.IncludeYear {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit YUS TVDB year: %w", err)
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
		return fmt.Errorf("insert YUS TVDB locale: %w", err)
	}
	return nil
}

func yusMatchesTVDBTitle(editor *trackers.NameEditor, title string) bool {
	component, ok := editor.Component(api.NameRoleTitle)
	return ok && component.Present && !component.Manual && strings.TrimSpace(title) != "" &&
		strings.EqualFold(strings.Join(strings.Fields(component.Value), " "), strings.Join(strings.Fields(title), " "))
}

func insertYUSDiscDistributor(editor *trackers.NameEditor, meta api.UploadSubject) error {
	distributor := strings.TrimSpace(trackers.PreferredDistributor(meta, meta.Distributor))
	if distributor == "" {
		return nil
	}
	if resolution, ok := editor.Component(api.NameRoleResolution); ok && resolution.Present {
		if err := editor.InsertAfter(api.NameRoleDistributor, distributor, api.NameRoleResolution); err != nil {
			return fmt.Errorf("insert YUS disc distributor after resolution: %w", err)
		}
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleDistributor, distributor, api.NameRoleRegion); err != nil {
		return fmt.Errorf("insert YUS disc distributor before region: %w", err)
	}
	return nil
}

func isYUSFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}

// isYUSCut reports whether an edition contains a cut or presentation marker
// retained by YUS's naming policy.
func isYUSCut(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"cut", "director", "extended", "unrated", "uncut", "censored", "imax", "3d", "open matte"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
