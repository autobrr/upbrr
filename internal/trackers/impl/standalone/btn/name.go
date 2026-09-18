// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/autobrr/upbrr/internal/config"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var btnMediaExtensionPattern = regexp.MustCompile(`(?i)\.(?:avi|mkv|mp4|ts|m4v|m2ts|wmv|mpeg|mpg|vob)$`)

var btnAudioNormalizationRules = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(?:^|\.)DDP\.(\d+(?:\.\d+)?)\.Atmos`), `.DDPA$1`},
	{regexp.MustCompile(`(?i)(?:^|\.)TrueHD\.(\d+(?:\.\d+)?)\.Atmos`), `.TrueHDA$1`},
	{regexp.MustCompile(`(?:^|\.)DDP\.(\d)`), `.DDP$1`},
	{regexp.MustCompile(`(?:^|\.)DD\.(\d)`), `.DD$1`},
	{regexp.MustCompile(`(?:^|\.)AC3\.(\d)`), `.AC3$1`},
	{regexp.MustCompile(`(?:^|\.)DTS\.(\d)`), `.DTS$1`},
	{regexp.MustCompile(`(?:^|\.)AAC\.(\d)`), `.AAC$1`},
	{regexp.MustCompile(`(?:^|\.)FLAC\.(\d)`), `.FLAC$1`},
	{regexp.MustCompile(`(?i)(?:^|\.)TrueHD\.(\d)`), `.TrueHD$1`},
	{regexp.MustCompile(`(?i)(?:^|\.)PCM\.(\d)`), `.PCM$1`},
	{regexp.MustCompile(`(?i)(?:^|\.)LPCM\.(\d)`), `.LPCM$1`},
}

// btnExactName retains scene and single-episode anime filenames as opaque BTN
// inputs. Generated names continue through the structured role policy.
func btnExactName(meta api.UploadSubject, _ config.TrackerConfig) string {
	if isBTNSceneRelease(meta) {
		for _, candidate := range []string{meta.SceneName, meta.ReleaseName, meta.ReleaseNameNoTag} {
			if name := strings.TrimSpace(candidate); name != "" {
				return name
			}
		}
	}
	if meta.Anime && !meta.TVPack {
		return btnMediaExtensionPattern.ReplaceAllString(strings.TrimSpace(pathutil.Base(meta.Filename)), "")
	}
	return ""
}

// applyBTNNameDefaults expresses BTN's generated-name conventions by role.
func applyBTNNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := normalizeBTNComponents(editor); err != nil {
		return btnNameEditorError("normalize components", err)
	}
	if strings.TrimSpace(meta.DailyEpisodeDate) != "" {
		if err := editor.Omit(api.NameRoleSeason); err != nil {
			return btnNameEditorError("omit daily season", err)
		}
		if err := editor.Omit(api.NameRoleEpisode); err != nil {
			return btnNameEditorError("omit daily episode", err)
		}
		if err := editor.Include(api.NameRoleDailyDate); err != nil {
			return btnNameEditorError("include daily date", err)
		}
		if err := editor.Set(api.NameRoleDailyDate, strings.ReplaceAll(strings.TrimSpace(meta.DailyEpisodeDate), "-", ".")); err != nil {
			return btnNameEditorError("set daily date", err)
		}
	} else if !releaseTitleContainsYear(meta.Release.Title) {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return btnNameEditorError("omit broadcast year", err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(meta.Release.Resolution)) {
	case "sd", "480i", "576i":
		if err := editor.Omit(api.NameRoleResolution); err != nil {
			return btnNameEditorError("omit SD resolution", err)
		}
	}
	if err := applyBTNGroupDefaults(editor, meta); err != nil {
		return btnNameEditorError("apply group", err)
	}
	if source := mapSource(meta, nil); source != "" && source != "Unknown" && source != "Mixed" {
		role := api.NameRoleSource
		switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
		case "WEBDL", "WEBRIP", "DVDRIP":
			role = api.NameRoleVideoFormat
		}
		if err := editor.Set(role, source); err != nil {
			return btnNameEditorError("set source", err)
		}
	}
	if codec := mapCodec(meta, nil); codec != "" && codec != "Mixed" {
		role := api.NameRoleVideoCodec
		if component, exists := editor.Component(api.NameRoleVideoEncode); exists && component.Present {
			role = api.NameRoleVideoEncode
		}
		if err := editor.Set(role, codec); err != nil {
			return btnNameEditorError("set video codec", err)
		}
	}
	return nil
}

func normalizeBTNComponents(editor *trackers.NameEditor) error {
	for _, role := range editor.PresentRoles() {
		component, _ := editor.Component(role)
		value := normalizeBTNComponent(component.Value, role)
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return btnNameEditorError("omit empty component", err)
			}
			continue
		}
		if err := editor.Set(role, value); err != nil {
			return btnNameEditorError("normalize component", err)
		}
	}
	return nil
}

func normalizeBTNComponent(value string, role api.ReleaseNameRole) string {
	transformer := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	value, _, _ = transform.String(transformer, value)
	value = strings.ReplaceAll(value, "&", " and ")
	value = strings.NewReplacer("'", "", "’", "").Replace(value)
	value = strings.ReplaceAll(strings.Join(strings.Fields(value), " "), " ", ".")
	if role == api.NameRoleAudio {
		value = strings.ReplaceAll(value, "DD+", "DDP")
		for _, rule := range btnAudioNormalizationRules {
			value = rule.pattern.ReplaceAllString(value, rule.replacement)
		}
	}
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '.'
	}, value)
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r == '.' }), ".")
}

func releaseTitleContainsYear(title string) bool {
	return regexp.MustCompile(`(?:^|\s)(?:19|20)\d{2}(?:\s|$)`).MatchString(strings.Join(strings.Fields(title), " "))
}

func applyBTNGroupDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	group := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if seasonPackHasMixedGroups(meta) {
		group = "BTN"
	}
	if group == "" || isNoGroupTag(group) {
		if component, exists := editor.Component(api.NameRoleGroup); exists && component.Present && !isNoGroupTag(strings.TrimPrefix(component.Value, "-")) {
			return nil
		}
		group = "NOGRP"
	}
	group = normalizeBTNComponent(group, api.NameRoleGroup)
	if group == "" {
		if err := editor.Omit(api.NameRoleGroup); err != nil {
			return btnNameEditorError("omit empty group", err)
		}
		return nil
	}
	if err := editor.Set(api.NameRoleGroup, "-"+group); err != nil {
		return btnNameEditorError("set group", err)
	}
	if err := editor.Include(api.NameRoleGroup); err != nil {
		return btnNameEditorError("include group", err)
	}
	return nil
}

func isNoGroupTag(tag string) bool {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "nogrp", "nogroup", "unknown", "unk":
		return true
	default:
		return false
	}
}

// resolveSearchName retains fact-based search terms only. Returning no search
// term for canonical tracker identities leaves duplicate comparison to the
// central resolved upload semantics rather than reparsing a rendered name.
func resolveSearchName(meta api.UploadSubject) string {
	for tracker, value := range meta.TrackerIDs {
		if strings.EqualFold(strings.TrimSpace(tracker), "BTN") && strings.TrimSpace(value) != "" {
			return ""
		}
	}
	if meta.Identity.IMDBID != 0 || meta.Identity.TVDBID != 0 {
		return ""
	}
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return strings.TrimSpace(meta.EffectiveMetadata.Title)
	}
	candidates := []string{strings.TrimSpace(meta.Release.Title)}
	if meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) && meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.Name), strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish))
	}
	if meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) && meta.ProviderMetadata.TVmaze != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVmaze.Name))
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func btnNameEditorError(action string, err error) error {
	return fmt.Errorf("BTN name policy %s: %w", action, err)
}
