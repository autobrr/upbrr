// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ldu

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/ldu/v2", trackers.StructuredNamePolicy{Defaults: applyLDUNameDefaults})
}

func applyLDUNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if unit3d.IsDiscType(meta.DiscType) {
		return nil
	}
	original := originalLanguage(meta)
	nonEnglishOriginal := original != "" && !isEnglish(original)
	audio, nonEnglishAudio := firstAudio(meta.AudioLanguages)
	subtitle := firstSubtitle(meta.SubtitleLanguages)
	if categoryID(meta) == "18" && subtitle != "" {
		if err := appendLDURole(editor, api.NameRoleSubtitleMarker, "[Subs "+subtitle+"]", api.NameRoleGroup); err != nil {
			return fmt.Errorf("insert LDU subtitle marker: %w", err)
		}
		return nil
	}
	if !nonEnglishOriginal && !nonEnglishAudio {
		return nil
	}
	if audio != "" {
		if err := appendLDURole(editor, api.NameRoleLanguageMarker, "["+audio+"]", api.NameRoleGroup); err != nil {
			return fmt.Errorf("insert LDU audio language marker: %w", err)
		}
	}
	if subtitle != "" {
		anchor := api.NameRoleGroup
		if audio != "" {
			anchor = api.NameRoleLanguageMarker
		}
		if err := appendLDURole(editor, api.NameRoleSubtitleMarker, "[Subs "+subtitle+"]", anchor); err != nil {
			return fmt.Errorf("insert LDU subtitle marker: %w", err)
		}
	}
	return nil
}

func appendLDURole(editor *trackers.NameEditor, role api.ReleaseNameRole, value string, preferred api.ReleaseNameRole) error {
	if component, ok := editor.Component(preferred); ok && component.Present {
		if err := editor.InsertAfter(role, value, preferred); err != nil {
			return fmt.Errorf("insert LDU role after preferred anchor: %w", err)
		}
		return nil
	}
	roles := editor.PresentRoles()
	if len(roles) == 0 {
		return nil
	}
	if err := editor.InsertAfter(role, value, roles[len(roles)-1]); err != nil {
		return fmt.Errorf("insert LDU role after fallback anchor: %w", err)
	}
	return nil
}

func firstAudio(values []string) (string, bool) {
	for _, value := range values {
		if code, english, ok := languageCode(value); ok {
			return code, !english
		}
	}
	return "", false
}

func firstSubtitle(values []string) string {
	for _, value := range values {
		if code, _, ok := languageCode(value); ok {
			return code
		}
	}
	return ""
}

func languageCode(value string) (string, bool, bool) {
	tag, ok := unit3d.ParseLanguageTag(value)
	if !ok {
		return "", false, false
	}
	base, _ := tag.Base()
	if base.String() == "und" {
		return "", false, false
	}
	code := base.ISO3()
	if code == "" {
		return "", false, false
	}
	return strings.ToUpper(code), base.String() == "en", true
}

func originalLanguage(meta api.UploadSubject) string {
	provider := ""
	if meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage) != "" {
			provider = meta.ProviderMetadata.TMDB.OriginalLanguage
		}
		if provider == "" && meta.ProviderMetadata.IMDB != nil {
			provider = meta.ProviderMetadata.IMDB.OriginalLanguage
		}
	}
	if meta.EffectiveMetadata.OriginalLanguageProvenance.IsManual() {
		return trackers.PreferredOriginalLanguage(meta, provider)
	}
	return strings.TrimSpace(provider)
}

func isEnglish(value string) bool {
	tag, ok := unit3d.ParseLanguageTag(value)
	if !ok {
		return false
	}
	base, _ := tag.Base()
	return base.String() == "en"
}
