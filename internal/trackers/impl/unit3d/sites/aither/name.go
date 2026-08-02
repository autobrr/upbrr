// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// buildName applies AITHER's v2 component ordering and omission rules to the
// reviewed release name. It returns an empty string when no release name exists.
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if name == "" {
		return ""
	}

	name = applyAitherTVDBDisambiguation(name, meta)
	name = stripParsedLanguages(name, meta.Release.Language)
	resolution := unit3d.Resolution(meta)
	videoCodec, videoEncode := strings.TrimSpace(meta.VideoCodec), strings.TrimSpace(meta.VideoEncode)
	nameType, source, audio := strings.ToUpper(strings.TrimSpace(meta.Type)), strings.TrimSpace(meta.Source), strings.TrimSpace(meta.Audio)
	edition := effectiveAitherEdition(meta)
	if edition != "" && !isAitherCut(edition) {
		name = removeLastComponent(name, edition)
		edition = ""
	}

	if nameType == "DVDRIP" {
		if meta.Source != "" {
			name = replaceLast(name, meta.Source+" ", "")
		}
		if videoEncode != "" {
			name = replaceLast(name, videoEncode, "")
		}
		if resolution != "" && !containsComponent(name, resolution) {
			name = replaceLast(name, "DVDRip", resolution+" DVDRip")
		}
		if audio != "" && videoEncode != "" {
			name = replaceLast(name, audio, audio+" "+videoEncode)
		}
	} else if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") || (nameType == "REMUX" && isDVDSource(source)) {
		if unit3d.IsDiscType(meta.DiscType) && edition != "" && strings.TrimSpace(meta.Repack) != "" {
			name = replaceLast(name, strings.TrimSpace(meta.Repack)+" "+edition, edition+" "+strings.TrimSpace(meta.Repack))
		}
		if resolution != "" && !containsComponent(name, resolution) {
			anchor := source
			if unit3d.IsDiscType(meta.DiscType) && strings.TrimSpace(meta.Region) != "" {
				anchor = strings.TrimSpace(meta.Region)
			}
			if anchor != "" {
				name = replaceLast(name, anchor, resolution+" "+anchor)
			}
		}
		if audio != "" && videoCodec != "" && !containsComponent(name, videoCodec) {
			name = replaceLast(name, audio, videoCodec+" "+audio)
		}
	}

	if language := aitherLanguage(meta); language != "" && !containsComponent(name, language) {
		anchors := []string{strings.TrimSpace(meta.Is3D), edition, meta.Repack, resolution, source, "DVDRip"}
		if meta.WebDV {
			anchors = append(anchors, "Hybrid")
		}
		name = insertBeforeComponents(name, language, anchors...)
	}
	name = omitNoGroupTag(name, meta.Tag)
	return cleanName(name)
}

// applyAitherTVDBDisambiguation rebuilds TV titles when the shared splitter can
// identify title components from TVDB disambiguation evidence.
func applyAitherTVDBDisambiguation(name string, meta api.UploadSubject) string {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil {
		return name
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, alternate, tail, ok := unit3d.SplitTVDBName(name, meta, evidence)
	if !ok {
		return name
	}
	parts := []string{title, alternate}
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		parts = append(parts, evidence.Locale)
	}
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
		parts = append(parts, strconv.Itoa(evidence.SeriesYear))
	}
	return cleanName(strings.Join(append(parts, tail), " "))
}

// stripParsedLanguages removes the last whole-component occurrence of each
// parser-supplied language and its normalized AITHER label.
func stripParsedLanguages(name string, languages []string) string {
	for _, value := range languages {
		name = removeLastComponent(name, value)
		name = removeLastComponent(name, aitherLanguageComponent(value))
	}
	return name
}

// aitherLanguage returns the first AITHER language marker for a non-disc release
// without English audio.
func aitherLanguage(meta api.UploadSubject) string {
	if unit3d.IsDiscType(meta.DiscType) || unit3d.HasEnglishLanguage(meta.AudioLanguages) {
		return ""
	}
	for _, value := range meta.AudioLanguages {
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

// effectiveAitherEdition applies release-name overrides and removes Hybrid,
// which AITHER positions as a separate release modifier.
func effectiveAitherEdition(meta api.UploadSubject) string {
	if meta.ReleaseNameOverrides.NoEdition != nil && *meta.ReleaseNameOverrides.NoEdition {
		return ""
	}
	value := meta.Edition
	if meta.ReleaseNameOverrides.Edition != nil {
		value = *meta.ReleaseNameOverrides.Edition
	}
	fields := strings.Fields(value)
	kept := fields[:0]
	for _, field := range fields {
		if !strings.EqualFold(field, "Hybrid") {
			kept = append(kept, field)
		}
	}
	return strings.Join(kept, " ")
}

// isAitherCut reports whether an edition contains a cut or presentation marker
// retained by AITHER's naming policy.
func isAitherCut(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"cut", "director", "extended", "unrated", "uncut", "censored", "imax", "3d", "open matte"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// omitNoGroupTag removes a trailing no-group placeholder when value identifies
// that placeholder as the release tag.
func omitNoGroupTag(name, value string) string {
	tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "-"))
	if !unit3d.IsNoGroupTag(value) && !unit3d.IsNoGroupTag(tag) {
		return name
	}
	for _, separator := range []string{"-", ".", "_", " "} {
		suffix := separator + tag
		if len(name) >= len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
			return strings.TrimSpace(name[:len(name)-len(suffix)])
		}
	}
	return name
}

// insertBeforeComponents resolves each anchor to its final whole-component
// match, inserts addition before the earliest resolved anchor, or appends it
// when no anchor is present.
func insertBeforeComponents(value, addition string, components ...string) string {
	padded := " " + cleanName(value) + " "
	lower := strings.ToLower(padded)
	index := -1
	for _, component := range components {
		component = cleanName(component)
		if component == "" {
			continue
		}
		candidate := strings.LastIndex(lower, " "+strings.ToLower(component)+" ")
		if candidate >= 0 && (index < 0 || candidate < index) {
			index = candidate
		}
	}
	if index < 0 {
		return cleanName(value + " " + addition)
	}
	return cleanName(padded[:index] + " " + addition + padded[index:])
}

// removeLastComponent removes the final case-insensitive whole-component match.
func removeLastComponent(value, component string) string {
	component = cleanName(component)
	if component == "" {
		return value
	}
	padded := " " + cleanName(value) + " "
	needle := " " + strings.ToLower(component) + " "
	index := strings.LastIndex(strings.ToLower(padded), needle)
	if index < 0 {
		return value
	}
	return cleanName(padded[:index] + " " + padded[index+len(needle):])
}

// containsComponent reports whether value contains a case-insensitive whole-
// component match.
func containsComponent(value, component string) bool {
	component = cleanName(component)
	return component != "" && strings.Contains(strings.ToLower(" "+cleanName(value)+" "), " "+strings.ToLower(component)+" ")
}

// replaceLast replaces the final case-insensitive substring match.
func replaceLast(value, old, replacement string) string {
	index := strings.LastIndex(strings.ToLower(value), strings.ToLower(old))
	if index < 0 {
		return value
	}
	return value[:index] + replacement + value[index+len(old):]
}

// cleanName trims value and collapses whitespace runs to single spaces.
func cleanName(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}

func isDVDSource(source string) bool {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "PAL DVD", "NTSC DVD", "DVD":
		return true
	default:
		return false
	}
}
