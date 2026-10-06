// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/aither/v4", trackers.StructuredNamePolicy{
		Defaults: applyAitherNameDefaults,
	})
}

func applyAitherNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := trackers.ApplyEnglishAudioNameDefaults(editor, meta); err != nil {
		return fmt.Errorf("apply AITHER language markers: %w", err)
	}
	if err := applyAitherTVDBDisambiguation(editor, meta); err != nil {
		return err
	}

	if err := editor.Omit(api.NameRoleEdition); err != nil {
		return fmt.Errorf("omit AITHER edition: %w", err)
	}

	nameType := strings.ToUpper(strings.TrimSpace(meta.Type))
	source := strings.TrimSpace(meta.Source)
	switch {
	case nameType == "DVDRIP":
		if err := applyAitherDVDRipNameOrder(editor); err != nil {
			return err
		}
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") || (nameType == "REMUX" && isDVDSource(source)):
		if err := applyAitherDVDNameOrder(editor, meta); err != nil {
			return err
		}
	}

	if err := addAitherLanguageMarker(editor, meta); err != nil {
		return err
	}
	if unit3d.IsNoGroupTag(meta.Tag) {
		if err := editor.Omit(api.NameRoleGroup); err != nil {
			return fmt.Errorf("omit AITHER no-group tag: %w", err)
		}
	}
	return nil
}

func applyAitherTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, ok := editor.Component(api.NameRoleTitle)
	if !ok || !title.Present || !strings.EqualFold(strings.Join(strings.Fields(title.Value), " "), strings.Join(strings.Fields(evidence.CanonicalName), " ")) {
		return nil
	}
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		evidence.SeriesYear = meta.EffectiveMetadata.Year
	}
	if !evidence.IncludeYear || evidence.SeriesYear <= 0 {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit AITHER TVDB year: %w", err)
		}
	} else {
		if err := editor.Set(api.NameRoleYear, strconv.Itoa(evidence.SeriesYear)); err != nil {
			return fmt.Errorf("set AITHER TVDB year: %w", err)
		}
		if err := editor.Include(api.NameRoleYear); err != nil {
			return fmt.Errorf("include AITHER TVDB year: %w", err)
		}
	}
	anchor := api.NameRoleAlternateTitle
	component, ok := editor.Component(anchor)
	if !ok || !component.Present {
		anchor = api.NameRoleTitle
	}
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		if err := editor.InsertAfter(api.NameRoleLocale, strings.TrimSpace(evidence.Locale), anchor); err != nil {
			return fmt.Errorf("insert AITHER TVDB locale: %w", err)
		}
		anchor = api.NameRoleLocale
	}
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
		if err := editor.MoveAfter(api.NameRoleYear, anchor); err != nil {
			return fmt.Errorf("move AITHER TVDB year: %w", err)
		}
	}
	return nil
}

func applyAitherDVDRipNameOrder(editor *trackers.NameEditor) error {
	if source, ok := editor.Component(api.NameRoleSource); ok && strings.TrimSpace(source.Value) != "" {
		if err := editor.Omit(api.NameRoleSource); err != nil {
			return fmt.Errorf("omit AITHER DVDRip source: %w", err)
		}
	}
	if resolution, ok := editor.Component(api.NameRoleResolution); ok && strings.TrimSpace(resolution.AvailableValue) != "" {
		if err := editor.Include(api.NameRoleResolution); err != nil {
			return fmt.Errorf("include AITHER DVDRip resolution: %w", err)
		}
		if err := editor.MoveBefore(api.NameRoleResolution, api.NameRoleVideoFormat); err != nil {
			return fmt.Errorf("move AITHER DVDRip resolution: %w", err)
		}
	}
	encode, hasEncode := editor.Component(api.NameRoleVideoEncode)
	if !hasEncode || strings.TrimSpace(encode.Value) == "" {
		return nil
	}
	anchor := firstAitherPresentRole(editor.PresentRoles(), api.NameRoleDualAudio, api.NameRoleAudio)
	if !anchor.Valid() {
		if err := editor.Omit(api.NameRoleVideoEncode); err != nil {
			return fmt.Errorf("omit AITHER DVDRip video encode without audio: %w", err)
		}
		return nil
	}
	if err := editor.MoveAfter(api.NameRoleVideoEncode, anchor); err != nil {
		return fmt.Errorf("move AITHER DVDRip video encode: %w", err)
	}
	return nil
}

func applyAitherDVDNameOrder(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.IsDiscType(meta.DiscType) {
		for _, role := range []api.ReleaseNameRole{api.NameRoleEditionSet, api.NameRoleCut, api.NameRoleEdition, api.NameRolePresentation} {
			if err := editor.MoveBefore(role, api.NameRoleRepack); err != nil {
				return fmt.Errorf("move AITHER DVD %s: %w", role, err)
			}
		}
	}
	if strings.TrimSpace(unit3d.Resolution(meta)) != "" {
		if err := editor.Include(api.NameRoleResolution); err != nil {
			return fmt.Errorf("include AITHER DVD resolution: %w", err)
		}
		anchors := []api.ReleaseNameRole{api.NameRoleSource, api.NameRoleDVDSystem, api.NameRoleDVDSize, api.NameRoleVideoFormat}
		if unit3d.IsDiscType(meta.DiscType) && strings.TrimSpace(meta.Region) != "" {
			anchors = append([]api.ReleaseNameRole{api.NameRoleRegion}, anchors...)
		}
		if anchor := firstAitherPresentRole(editor.PresentRoles(), anchors...); anchor.Valid() {
			if err := editor.MoveBefore(api.NameRoleResolution, anchor); err != nil {
				return fmt.Errorf("move AITHER DVD resolution: %w", err)
			}
		}
	}
	if strings.TrimSpace(meta.Audio) != "" && strings.TrimSpace(meta.VideoCodec) != "" {
		if err := editor.Include(api.NameRoleVideoCodec); err != nil {
			return fmt.Errorf("include AITHER DVD video codec: %w", err)
		}
		if err := editor.MoveBefore(api.NameRoleVideoCodec, api.NameRoleAudio); err != nil {
			return fmt.Errorf("move AITHER DVD video codec: %w", err)
		}
	}
	return nil
}

func addAitherLanguageMarker(editor *trackers.NameEditor, meta api.UploadSubject) error {
	language := aitherLanguage(meta)
	if language == "" {
		return nil
	}
	anchor := firstAitherPresentRole(
		editor.PresentRoles(),
		api.NameRoleThreeD,
		api.NameRoleEditionSet, api.NameRoleCut,
		api.NameRoleEdition,
		api.NameRolePresentation,
		api.NameRoleRepack,
		api.NameRoleResolution,
		api.NameRoleSource,
		api.NameRoleVideoFormat,
	)
	if !anchor.Valid() {
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleLanguageMarker, language, anchor); err != nil {
		return fmt.Errorf("insert AITHER language marker: %w", err)
	}
	return nil
}

func firstAitherPresentRole(present []api.ReleaseNameRole, candidates ...api.ReleaseNameRole) api.ReleaseNameRole {
	for _, candidate := range candidates {
		for _, role := range present {
			if role == candidate {
				return role
			}
		}
	}
	return ""
}

// aitherLanguage returns AITHER's non-English marker, preserving the legacy
// aggregate-language behavior for full-disc uploads.
func aitherLanguage(meta api.UploadSubject) string {
	languages := meta.LanguageFacts.ProgrammeLanguages
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) {
		if unit3d.IsDiscType(meta.DiscType) {
			return ""
		}
		languages = meta.AudioLanguages
	}
	if unit3d.HasEnglishLanguage(languages) {
		return ""
	}
	for _, value := range languages {
		if language := aitherLanguageComponent(value); language != "" {
			return language
		}
	}
	return ""
}

// aitherLanguageComponent returns AITHER's uppercase label for a language value,
// preserving unrecognized values and canonicalizing special language markers.
func aitherLanguageComponent(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "zxx", "no linguistic content":
		return "ZXX"
	case "mul", "multiple", "multiple languages":
		return "MULTIPLE LANGUAGES"
	}
	if normalized := languageutil.NormalizeLanguageDisplay(trimmed); normalized != "" {
		return strings.ToUpper(normalized)
	}
	return strings.ToUpper(trimmed)
}

func isDVDSource(source string) bool {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "PAL DVD", "NTSC DVD", "DVD":
		return true
	default:
		return false
	}
}
