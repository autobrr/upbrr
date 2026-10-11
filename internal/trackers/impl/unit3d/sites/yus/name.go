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
	return trackers.StructuredReleaseNamePolicy("unit3d/yus/v8", trackers.StructuredNamePolicy{Defaults: applyYUSNameDefaults})
}

func applyYUSNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if !isYUSFullDisc(meta) {
		for _, role := range []api.ReleaseNameRole{api.NameRoleDualAudio, api.NameRoleDubbed} {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit YUS audio-language marker: %w", err)
			}
		}
		if trackers.KnownProgrammeLanguageCount(meta.LanguageFacts) >= 2 {
			if err := editor.InsertBefore(api.NameRoleDualAudio, "Multi-Audio", api.NameRoleAudio); err != nil {
				return fmt.Errorf("set YUS audio-language marker: %w", err)
			}
		}
		if err := trackers.ApplyDefaultAudioName(editor, meta); err != nil {
			return fmt.Errorf("apply YUS default audio: %w", err)
		}
	}
	if err := applyYUSTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if err := editor.Omit(api.NameRoleEdition); err != nil {
		return fmt.Errorf("omit YUS edition: %w", err)
	}
	for _, role := range []api.ReleaseNameRole{api.NameRoleEditionSet, api.NameRoleCut, api.NameRolePresentation} {
		if err := editor.Include(role); err != nil {
			return fmt.Errorf("include YUS %s: %w", role, err)
		}
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
			if err := editor.MoveBefore(role, api.NameRoleRepack); err != nil {
				return fmt.Errorf("move YUS DVD %s before version: %w", role, err)
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
	if unit3d.Category(meta) != "TV" {
		return nil
	}
	evidence, ok := trackers.CurrentTVDBNameDisambiguation(editor, meta)
	if !ok {
		return nil
	}
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move YUS alternate title before year: %w", err)
	}
	if !meta.EffectiveMetadata.YearProvenance.IsManual() && !evidence.IncludeYear {
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
	return trackers.IsFullDiscUpload(meta.DiscType, meta.Type)
}
