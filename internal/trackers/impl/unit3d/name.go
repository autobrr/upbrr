// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"strings"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	languageTagLookupOnce sync.Once
	languageTagLookup     map[string]language.Tag
)

var localizedCodecReplacer = strings.NewReplacer("DD+ ", "DDP", "DD ", "DD", "AAC ", "AAC", "FLAC ", "FLAC")

var portugueseLanguageNames = map[string]struct{}{
	"português":  {},
	"portuguese": {},
	"pt-br":      {},
	"pt":         {},
}

func buildUnit3DName(_ string, meta api.UploadSubject, cfg config.TrackerConfig, profiles ...SiteProfile) string {
	profile := firstSiteProfile(profiles)
	if profile.BuildName != nil {
		return profile.BuildName(meta, cfg)
	}
	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}

	return name
}

func baseReleaseName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

// ApplyDVDNameDefaults presents DVD encodes and remuxes using their finalized
// source system. Compact names omit separate video tokens and use DVD; otherwise
// encodes use DVDRip and retain their layout. Direct manual components are protected.
// Generic DVD requires a supported release-level resolution unless Source is manual.
func ApplyDVDNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, compact bool) error {
	releaseType := strings.ToUpper(strings.TrimSpace(meta.Type))
	if releaseType != "ENCODE" && releaseType != "REMUX" {
		return nil
	}
	source := strings.ToUpper(strings.TrimSpace(meta.Source))
	if source != "DVD" && source != "PAL DVD" && source != "NTSC DVD" {
		return nil
	}
	component, _ := editor.Component(api.NameRoleSource)
	if !component.Manual {
		system := strings.TrimSuffix(source, " DVD")
		if source == "DVD" {
			switch strings.ToLower(strings.TrimSpace(meta.Release.Resolution)) {
			case "576i", "576p":
				system = "PAL"
			case "480i", "480p":
				system = "NTSC"
			default:
				return &trackers.NameRuleError{
					Rule: "dvd_system",
					Role: api.NameRoleSource,
					Reason: "DVD naming requires PAL DVD or NTSC DVD Source, or finalized 480i/480p/576i/576p Resolution; " +
						"correct Source or Resolution and reprepare",
				}
			}
		}
		label := system + " DVD"
		if !compact && releaseType == "ENCODE" {
			label += "Rip"
		}
		if err := editor.Set(api.NameRoleSource, label); err != nil {
			return fmt.Errorf("set DVD source: %w", err)
		}
	}
	if compact {
		for _, role := range []api.ReleaseNameRole{
			api.NameRoleVideoFormat, api.NameRoleResolution, api.NameRoleVideoCodec, api.NameRoleVideoEncode,
		} {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit DVD %s: %w", role, err)
			}
		}
	}
	return nil
}

// LocalizedReleaseNamePolicy applies the shared Portuguese-localized naming
// convention used by Unit3D sites that opt into it.
func LocalizedReleaseNamePolicy(id string) trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy(id, trackers.StructuredNamePolicy{Defaults: applyLocalizedNameDefaults})
}

func applyLocalizedNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, cfg config.TrackerConfig) error {
	if err := normalizeLocalizedGroup(editor, meta); err != nil {
		return fmt.Errorf("normalize localized group: %w", err)
	}
	if (resolveUnit3DCategory(meta) == "TV" || meta.Anime) && meta.Release.Year > 0 {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit localized year: %w", err)
		}
	}
	if err := applyLocalizedAlternateTitle(editor, meta); err != nil {
		return fmt.Errorf("apply localized alternate title: %w", err)
	}
	if audio, ok := editor.Component(api.NameRoleAudio); ok && audio.Present {
		if err := editor.Set(api.NameRoleAudio, localizedCodecReplacer.Replace(audio.Value)); err != nil {
			return fmt.Errorf("normalize localized audio: %w", err)
		}
	}
	if IsDiscType(meta.DiscType) {
		return nil
	}

	label := localizedAudioLabel(meta.AudioLanguages)
	if label == "" {
		return nil
	}
	if err := editor.Omit(api.NameRoleDubbed); err != nil {
		return fmt.Errorf("omit localized dubbed marker: %w", err)
	}
	if err := insertLocalizedOriginalGroup(editor, meta, cfg); err != nil {
		return fmt.Errorf("insert localized original group: %w", err)
	}
	if err := editor.InsertBefore(api.NameRoleDualAudio, label, api.NameRoleGroup); err != nil {
		return fmt.Errorf("insert localized audio marker: %w", err)
	}
	return nil
}

func normalizeLocalizedGroup(editor *trackers.NameEditor, meta api.UploadSubject) error {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag == "" {
		if group, ok := editor.Component(api.NameRoleGroup); ok && group.Present {
			tag = strings.TrimSpace(strings.TrimPrefix(group.Value, "-"))
		}
	}
	if tag != "" && !IsNoGroupTag(tag) {
		return nil
	}
	if err := editor.Set(api.NameRoleGroup, "-NoGroup"); err != nil {
		return fmt.Errorf("set localized no-group suffix: %w", err)
	}
	if err := editor.Include(api.NameRoleGroup); err != nil {
		return fmt.Errorf("include localized no-group suffix: %w", err)
	}
	return nil
}

func applyLocalizedAlternateTitle(editor *trackers.NameEditor, meta api.UploadSubject) error {
	alternate, ok := editor.Component(api.NameRoleAlternateTitle)
	if !ok || !alternate.Present {
		return nil
	}
	_, portuguese := portugueseLanguageNames[strings.ToLower(resolveOriginalLanguage(meta))]
	if portuguese {
		title := strings.TrimSpace(strings.TrimPrefix(alternate.Value, "AKA"))
		if title != "" {
			if err := editor.Set(api.NameRoleTitle, title); err != nil {
				return fmt.Errorf("set localized Portuguese title: %w", err)
			}
		}
	}
	if err := editor.Omit(api.NameRoleAlternateTitle); err != nil {
		return fmt.Errorf("omit localized alternate title: %w", err)
	}
	return nil
}

func localizedAudioLabel(values []string) string {
	hasPortuguese := false
	languages := make(map[string]struct{})
	for _, value := range values {
		if _, ok := portugueseLanguageNames[strings.ToLower(strings.TrimSpace(value))]; ok {
			hasPortuguese = true
		}
		if code, _, ok := languageCode(value); ok {
			languages[code] = struct{}{}
		}
	}
	if !hasPortuguese {
		return ""
	}
	if len(languages) >= 3 {
		return "MULTI"
	}
	if len(languages) == 2 {
		return "DUAL"
	}
	return ""
}

func insertLocalizedOriginalGroup(editor *trackers.NameEditor, meta api.UploadSubject, cfg config.TrackerConfig) error {
	customTag := strings.TrimSpace(strings.TrimPrefix(cfg.TagForCustomRelease, "-"))
	originalGroup := strings.TrimSpace(meta.Release.Group)
	group, ok := editor.Component(api.NameRoleGroup)
	if customTag == "" || originalGroup == "" || strings.EqualFold(customTag, originalGroup) || !ok || !group.Present ||
		!strings.EqualFold(strings.TrimPrefix(group.Value, "-"), customTag) {
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleOriginalGroup, "-"+originalGroup, api.NameRoleGroup); err != nil {
		return fmt.Errorf("insert localized original group: %w", err)
	}
	if err := editor.SetJoin(api.NameRoleOriginalGroup, ""); err != nil {
		return fmt.Errorf("attach localized original group: %w", err)
	}
	return nil
}

func languageCode(value string) (string, bool, bool) {
	normalized := languageutil.NormalizeLanguageDisplay(value)
	if normalized == "" {
		normalized = strings.TrimSpace(value)
	}
	tag, ok := parseLanguageTag(normalized)
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
	return strings.ToUpper(code), isEnglishLanguageTag(base.String()), true
}

func parseLanguageTag(value string) (language.Tag, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return language.Tag{}, false
	}
	if tag, err := language.Parse(trimmed); err == nil && tag != language.Und {
		return tag, true
	}
	normalized := languageutil.NormalizeLanguageDisplay(trimmed)
	if normalized == "" {
		normalized = trimmed
	}
	languageTagLookupOnce.Do(buildLanguageTagLookup)
	tag, ok := languageTagLookup[strings.ToLower(strings.TrimSpace(normalized))]
	if ok {
		return tag, true
	}
	return language.Tag{}, false
}

func buildLanguageTagLookup() {
	languageTagLookup = make(map[string]language.Tag)
	namer := display.Languages(language.English)
	for _, tag := range display.Supported.Tags() {
		name := strings.ToLower(strings.TrimSpace(namer.Name(tag)))
		if name == "" {
			continue
		}
		if _, exists := languageTagLookup[name]; exists {
			continue
		}
		languageTagLookup[name] = tag
	}
}

func resolveOriginalLanguage(meta api.UploadSubject) string {
	if meta.EffectiveMetadata.OriginalLanguageProvenance.IsManual() {
		return trackers.PreferredOriginalLanguage(meta, "")
	}
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return ""
	}
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	default:
		return ""
	}
}

func isEnglishLanguageTag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "english", "en", "eng", "en-us", "en-gb":
		return true
	default:
		return false
	}
}

func isNoGroupTag(tag string) bool {
	value := strings.ToLower(strings.TrimSpace(tag))
	switch value {
	case "nogrp", "nogroup", "unknown", "-unk-":
		return true
	default:
		return false
	}
}
