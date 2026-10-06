// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/dp/v4", trackers.StructuredNamePolicy{
		Defaults: applyDPNameDefaults,
	})
}

func applyDPNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyDPTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return nil
	}
	if label := audioLabel(meta.AudioLanguages); label != "" {
		if err := editor.Set(api.NameRoleDualAudio, label); err != nil {
			return fmt.Errorf("set DP audio label: %w", err)
		}
	}
	return nil
}

func applyDPTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return nil
	}
	tvdb := meta.ProviderMetadata.TVDB
	evidence := tvdb.NameDisambiguation
	title, ok := editor.Component(api.NameRoleTitle)
	if !ok || !title.Present || title.Manual || meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return nil
	}
	if meta.Identity.TVDBID > 0 && tvdb.TVDBID > 0 && meta.Identity.TVDBID != tvdb.TVDBID {
		return nil
	}
	titleKey := dpTitleIdentityKey(title.Value)
	canonicalKey := dpTitleIdentityKey(evidence.CanonicalName)
	sameSeries := titleKey != "" && canonicalKey != "" && titleKey == canonicalKey
	if !sameSeries && titleKey != "" {
		if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil && tmdb.TMDBID > 0 && tmdb.TMDBID == meta.Identity.TMDBID {
			tmdbKey := dpTitleIdentityKey(tmdb.Title)
			sameSeries = tmdbKey != "" && titleKey == tmdbKey
		}
	}
	if !sameSeries {
		return nil
	}
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		evidence.SeriesYear = meta.EffectiveMetadata.Year
	}
	if !evidence.IncludeYear || evidence.SeriesYear <= 0 {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit DP TVDB year: %w", err)
		}
	} else {
		if err := editor.Set(api.NameRoleYear, strconv.Itoa(evidence.SeriesYear)); err != nil {
			return fmt.Errorf("set DP TVDB year: %w", err)
		}
		if err := editor.Include(api.NameRoleYear); err != nil {
			return fmt.Errorf("include DP TVDB year: %w", err)
		}
	}

	anchor := api.NameRoleAlternateTitle
	component, ok := editor.Component(anchor)
	if !ok || !component.Present {
		anchor = api.NameRoleTitle
	}
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		if err := editor.InsertAfter(api.NameRoleLocale, evidence.Locale, anchor); err != nil {
			return fmt.Errorf("insert DP TVDB locale: %w", err)
		}
		anchor = api.NameRoleLocale
	}
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
		if err := editor.MoveAfter(api.NameRoleYear, anchor); err != nil {
			return fmt.Errorf("move DP TVDB year: %w", err)
		}
	}
	return nil
}

// dpTitleIdentityKey compares titles across punctuation and spacing differences.
func dpTitleIdentityKey(value string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return builder.String()
}

func audioLabel(values []string) string {
	unique := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			unique[strings.ToUpper(value)] = struct{}{}
		}
	}
	switch len(unique) {
	case 0:
		return ""
	case 1:
		for value := range unique {
			return value
		}
		return ""
	case 2:
		return "Dual-Audio"
	default:
		return "MULTi"
	}
}
