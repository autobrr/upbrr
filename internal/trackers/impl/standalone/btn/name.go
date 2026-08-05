// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

type btnNameNormalizationRule struct {
	pattern     *regexp.Regexp
	replacement string
}

var btnNameNormalizationRules = []btnNameNormalizationRule{
	{pattern: regexp.MustCompile(`(?i)\.DDP\.(\d+(?:\.\d+)?)\.Atmos`), replacement: `.DDPA$1`},
	{pattern: regexp.MustCompile(`(?i)\.TrueHD\.(\d+(?:\.\d+)?)\.Atmos`), replacement: `.TrueHDA$1`},
	{pattern: regexp.MustCompile(`\.DDP\.(\d)`), replacement: `.DDP$1`},
	{pattern: regexp.MustCompile(`\.DD\.(\d)`), replacement: `.DD$1`},
	{pattern: regexp.MustCompile(`\.AC3\.(\d)`), replacement: `.AC3$1`},
	{pattern: regexp.MustCompile(`\.DTS\.(\d)`), replacement: `.DTS$1`},
	{pattern: regexp.MustCompile(`\.AAC\.(\d)`), replacement: `.AAC$1`},
	{pattern: regexp.MustCompile(`\.FLAC\.(\d)`), replacement: `.FLAC$1`},
	{pattern: regexp.MustCompile(`(?i)\.TrueHD\.(\d)`), replacement: `.TrueHD$1`},
	{pattern: regexp.MustCompile(`(?i)\.PCM\.(\d)`), replacement: `.PCM$1`},
	{pattern: regexp.MustCompile(`(?i)\.LPCM\.(\d)`), replacement: `.LPCM$1`},
	{pattern: regexp.MustCompile(`[^a-zA-Z0-9.\-]`), replacement: `.`},
	{pattern: regexp.MustCompile(`\.{2,}`), replacement: `.`},
}

func resolveUploadName(meta api.UploadSubject) string {
	var name string
	if n := strings.TrimSpace(meta.ReleaseName); n != "" {
		name = n
	} else if n := strings.TrimSpace(meta.ReleaseNameNoTag); n != "" {
		name = n
	} else if n := strings.TrimSpace(meta.Filename); n != "" {
		name = n
	} else {
		name = pathutil.Base(meta.SourcePath)
	}
	name = cleanAndNormalizeBTNName(name)
	name = applyBTNNoGroupSuffix(name, meta)
	codec := mapCodec(meta, nil)
	if codec == "Mixed" {
		codec = ""
	}
	source := mapSource(meta, nil)
	if source == "Unknown" {
		source = ""
	}
	return applyBTNNameMapping(name, codec, source)
}

func resolveSearchName(meta api.UploadSubject) string {
	for tracker, value := range meta.TrackerIDs {
		if strings.EqualFold(strings.TrimSpace(tracker), "BTN") && strings.TrimSpace(value) != "" {
			return resolveUploadName(meta)
		}
	}
	if meta.Identity.IMDBID != 0 || meta.Identity.TVDBID != 0 {
		return resolveUploadName(meta)
	}
	candidates := []string{strings.TrimSpace(meta.Release.Title)}
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.Name), strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish))
	}
	if meta.ProviderMetadata.TVmaze != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVmaze.Name))
	}
	candidates = append(candidates, strings.TrimSpace(meta.Filename), strings.TrimSpace(meta.ReleaseName))
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return resolveUploadName(meta)
}

func applyBTNNoGroupSuffix(name string, meta api.UploadSubject) string {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag != "" && !isNoGroupTag(tag) {
		if selectedBTNReleaseNameNoTag(name, meta) || !hasBTNGroupSuffix(name) {
			return strings.TrimRight(name, ".-") + "-" + tag
		}
		return name
	}
	if tag == "" && hasBTNGroupSuffix(name) && !hasBTNNoGroupSuffix(name) {
		return name
	}
	normalizedName := regexp.MustCompile(`(?i)-(nogrp|nogroup|unknown|unk)$`).ReplaceAllString(name, "")
	return strings.TrimRight(normalizedName, ".-") + "-NOGRP"
}

func selectedBTNReleaseNameNoTag(name string, meta api.UploadSubject) bool {
	if strings.TrimSpace(meta.ReleaseName) != "" || strings.TrimSpace(meta.ReleaseNameNoTag) == "" {
		return false
	}
	candidate := cleanAndNormalizeBTNName(strings.TrimSpace(meta.ReleaseNameNoTag))
	return strings.TrimSpace(name) == candidate
}

func hasBTNGroupSuffix(name string) bool {
	return regexp.MustCompile(`-[^-.\s]+$`).MatchString(strings.TrimSpace(name))
}

func hasBTNNoGroupSuffix(name string) bool {
	return regexp.MustCompile(`(?i)-(nogrp|nogroup|unknown|unk)$`).MatchString(strings.TrimSpace(name))
}

func isNoGroupTag(tag string) bool {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "nogrp", "nogroup", "unknown", "unk":
		return true
	default:
		return false
	}
}

func removeDiacritics(value string) string {
	transformer := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(transformer, value)
	return result
}

func cleanAndNormalizeBTNName(value string) string {
	value = removeDiacritics(value)
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " ", ".")
	value = strings.ReplaceAll(value, "DD+", "DDP")
	for _, rule := range btnNameNormalizationRules {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return strings.TrimSpace(value)
}

func applyBTNNameMapping(releaseName string, mappedCodec string, mappedSource string) string {
	updated := releaseName
	if mappedSource != "" {
		sourcePattern := regexp.MustCompile(`(?i)\b(bluray|blu-ray|bdrip|brrip|web-dl|webrip|hdtv|dvdrip|hddvd|dvd5|dvd9|bd5|bd9|bd25|bd50)\b`)
		updated = sourcePattern.ReplaceAllString(updated, mappedSource)
	}
	if mappedCodec == "" {
		return updated
	}
	codecPatterns := map[string]*regexp.Regexp{
		"H.264":      regexp.MustCompile(`(?i)\b(x264|h\.264|h264|avc)\b`),
		"H.265":      regexp.MustCompile(`(?i)\b(x265|h\.265|h265|hevc)\b`),
		"x264-Hi10P": regexp.MustCompile(`(?i)\b(x264-hi10p|hi10p)\b`),
		"XViD":       regexp.MustCompile(`(?i)\b(xvid)\b`),
		"DiVX":       regexp.MustCompile(`(?i)\b(divx)\b`),
		"MPEG2":      regexp.MustCompile(`(?i)\b(mpeg-2|mpeg2)\b`),
		"VC-1":       regexp.MustCompile(`(?i)\b(vc-1)\b`),
		"WMV":        regexp.MustCompile(`(?i)\b(wmv)\b`),
		"VP9":        regexp.MustCompile(`(?i)\b(vp9)\b`),
	}
	if pattern, ok := codecPatterns[mappedCodec]; ok {
		updated = pattern.ReplaceAllString(updated, mappedCodec)
	}
	return updated
}
