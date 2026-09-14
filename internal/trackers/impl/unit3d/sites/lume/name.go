// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/lume/v3", trackers.StructuredNamePolicy{Defaults: applyLumeNameDefaults})
}

func applyLumeNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyLumeTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if !isLumeFullDisc(meta) {
		if err := editor.Omit(api.NameRoleEdition); err != nil {
			return fmt.Errorf("omit LUME edition: %w", err)
		}
	}
	if err := applyLumeHDR(editor, meta.HDRFacts); err != nil {
		return err
	}
	if err := editor.Omit(api.NameRoleHybrid); err != nil {
		return fmt.Errorf("omit LUME hybrid: %w", err)
	}
	return omitLumeHi10P(editor)
}

func applyLumeTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) || meta.ProviderMetadata.TVDB == nil {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if !lumeMatchesTVDBTitle(editor, evidence.CanonicalName) {
		return nil
	}
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move LUME alternate title before year: %w", err)
	}
	if !evidence.IncludeYear {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit LUME TVDB year: %w", err)
		}
	}
	return nil
}

func lumeMatchesTVDBTitle(editor *trackers.NameEditor, title string) bool {
	component, ok := editor.Component(api.NameRoleTitle)
	return ok && component.Present && !component.Manual && strings.TrimSpace(title) != "" &&
		strings.EqualFold(strings.Join(strings.Fields(component.Value), " "), strings.Join(strings.Fields(title), " "))
}

func applyLumeHDR(editor *trackers.NameEditor, facts api.HDRFacts) error {
	if facts.Status == "" || facts.Status == api.HDREvidenceMissing {
		return nil
	}
	hdr := lumeHDR(facts)
	if hdr == "" {
		if err := editor.Omit(api.NameRoleHDR); err != nil {
			return fmt.Errorf("omit LUME HDR: %w", err)
		}
		return nil
	}
	if err := editor.Set(api.NameRoleHDR, hdr); err != nil {
		return fmt.Errorf("set LUME HDR: %w", err)
	}
	return nil
}

func omitLumeHi10P(editor *trackers.NameEditor) error {
	for _, role := range []api.ReleaseNameRole{api.NameRoleVideoEncode, api.NameRoleVideoCodec} {
		component, ok := editor.Component(role)
		if !ok || !component.Present {
			continue
		}
		value := strings.Join(slices.DeleteFunc(strings.Fields(component.Value), func(field string) bool {
			return strings.EqualFold(field, "Hi10P")
		}), " ")
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit LUME Hi10P %s: %w", role, err)
			}
			continue
		}
		if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("remove LUME Hi10P from %s: %w", role, err)
		}
	}
	return nil
}

func isLumeFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}

func lumeHDR(facts api.HDRFacts) string {
	hasDV := slices.Contains(facts.Formats, api.HDRFormatDolbyVision)
	hasHDR10Plus := slices.Contains(facts.Formats, api.HDRFormatHDR10Plus)
	hasHDR := hasHDR10Plus || slices.Contains(facts.Formats, api.HDRFormatHDR10) || slices.Contains(facts.Formats, api.HDRFormatHLG) ||
		slices.Contains(facts.Formats, api.HDRFormatPQ10) || slices.Contains(facts.Formats, api.HDRFormatHDRVivid)
	switch {
	case hasDV && hasHDR10Plus:
		return "DV HDR10+"
	case hasDV && hasHDR:
		return "DV HDR"
	case hasDV:
		return "DV"
	case hasHDR10Plus:
		return "HDR10+"
	case hasHDR:
		return "HDR"
	default:
		return ""
	}
}
