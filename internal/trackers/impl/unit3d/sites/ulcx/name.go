// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/ulcx/v3", trackers.StructuredNamePolicy{Defaults: applyULCXNameDefaults})
}

func applyULCXNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyULCXTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if err := editor.Omit(api.NameRoleEdition); err != nil {
		return fmt.Errorf("omit ULCX edition: %w", err)
	}
	if isULCXFullDisc(meta) {
		if err := insertULCXDiscDistributor(editor, meta); err != nil {
			return err
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") &&
		(strings.Contains(strings.ToLower(strings.TrimSpace(meta.Edition)), "hybrid") || meta.WebDV) {
		if err := editor.Omit(api.NameRoleHybrid); err != nil {
			return fmt.Errorf("omit ULCX hybrid: %w", err)
		}
	}
	return correctULCXX265Token(editor, meta)
}

func applyULCXTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) || meta.ProviderMetadata.TVDB == nil {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if !ulcxMatchesTVDBTitle(editor, evidence.CanonicalName) {
		return nil
	}
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move ULCX alternate title before year: %w", err)
	}
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		anchor := api.NameRoleAlternateTitle
		if alternate, ok := editor.Component(anchor); !ok || !alternate.Present {
			anchor = api.NameRoleTitle
		}
		if err := editor.InsertAfter(api.NameRoleLocale, evidence.Locale, anchor); err != nil {
			return fmt.Errorf("insert ULCX TVDB locale: %w", err)
		}
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit ULCX TVDB year with locale: %w", err)
		}
		return nil
	}
	if !evidence.IncludeYear {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit ULCX TVDB year: %w", err)
		}
	}
	return nil
}

func ulcxMatchesTVDBTitle(editor *trackers.NameEditor, title string) bool {
	component, ok := editor.Component(api.NameRoleTitle)
	return ok && component.Present && !component.Manual && strings.TrimSpace(title) != "" &&
		strings.EqualFold(strings.Join(strings.Fields(component.Value), " "), strings.Join(strings.Fields(title), " "))
}

func insertULCXDiscDistributor(editor *trackers.NameEditor, meta api.UploadSubject) error {
	distributor := strings.TrimSpace(trackers.PreferredDistributor(meta, meta.Distributor))
	if distributor == "" {
		return nil
	}
	if resolution, ok := editor.Component(api.NameRoleResolution); ok && resolution.Present {
		if err := editor.InsertAfter(api.NameRoleDistributor, distributor, api.NameRoleResolution); err != nil {
			return fmt.Errorf("insert ULCX disc distributor after resolution: %w", err)
		}
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleDistributor, distributor, api.NameRoleRegion); err != nil {
		return fmt.Errorf("insert ULCX disc distributor before region: %w", err)
	}
	return nil
}

func correctULCXX265Token(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if !strings.EqualFold(strings.TrimSpace(meta.VideoEncode), "x265") {
		return nil
	}
	for _, role := range []api.ReleaseNameRole{api.NameRoleVideoEncode, api.NameRoleVideoCodec} {
		component, ok := editor.Component(role)
		if !ok || !component.Present {
			continue
		}
		if err := editor.Set(role, "x265"); err != nil {
			return fmt.Errorf("set ULCX %s to x265: %w", role, err)
		}
		return nil
	}
	return nil
}

func isULCXFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}
