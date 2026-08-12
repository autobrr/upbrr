// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"regexp"
	"strings"
	"time"
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

var (
	btnMediaExtensionPattern = regexp.MustCompile(`(?i)\.(?:avi|mkv|mp4|ts|m4v|m2ts|wmv|mpeg|mpg|vob)$`)
	btnEpisodeTokenPattern   = regexp.MustCompile(`(?i)S\d{1,3}E\d{1,4}(?:-E?\d{1,4})?`)
	btnDailyTokenPattern     = regexp.MustCompile(`\b20\d{2}[.\-_]\d{2}[.\-_]\d{2}\b`)
	btnYearBeforeSeason      = regexp.MustCompile(`(?i)\.(?:19|20)\d{2}(\.S\d{1,3}(?:E\d{1,4})?)`)
)

func resolveUploadName(meta api.UploadSubject) string {
	if isBTNSceneRelease(meta) {
		for _, candidate := range []string{meta.SceneName, meta.ReleaseName, meta.ReleaseNameNoTag} {
			if name := strings.TrimSpace(candidate); name != "" {
				return name
			}
		}
	}
	if meta.Anime && !meta.TVPack {
		if name := strings.TrimSpace(pathutil.Base(meta.Filename)); name != "" {
			return btnMediaExtensionPattern.ReplaceAllString(name, "")
		}
	}
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
	name = btnMediaExtensionPattern.ReplaceAllString(name, "")
	name = cleanAndNormalizeBTNName(name)
	name = applyBTNDailyDate(name, meta.DailyEpisodeDate)
	name = applyBTNYearRule(name, meta)
	name = applyBTNSDResolutionRule(name, meta.Release.Resolution)
	name = applyBTNNoGroupSuffix(name, meta)
	if seasonPackHasMixedGroups(meta) {
		name = regexp.MustCompile(`-[^-\.]+$`).ReplaceAllString(name, "-BTN")
	}
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

func applyBTNDailyDate(name string, value string) string {
	date, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return name
	}
	token := date.Format("2006.01.02")
	if btnDailyTokenPattern.MatchString(name) {
		return btnDailyTokenPattern.ReplaceAllString(name, token)
	}
	return btnEpisodeTokenPattern.ReplaceAllString(name, token)
}

func applyBTNYearRule(name string, meta api.UploadSubject) string {
	if strings.TrimSpace(meta.DailyEpisodeDate) != "" || releaseTitleContainsYear(meta.Release.Title) {
		return name
	}
	return btnYearBeforeSeason.ReplaceAllString(name, "$1")
}

func releaseTitleContainsYear(title string) bool {
	return regexp.MustCompile(`(?:^|\s)(?:19|20)\d{2}(?:\s|$)`).MatchString(strings.Join(strings.Fields(title), " "))
}

func applyBTNSDResolutionRule(name string, resolution string) string {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "sd", "480i", "480p", "576i", "576p":
	default:
		return name
	}
	parts := strings.Split(name, ".")
	kept := parts[:0]
	for _, part := range parts {
		lower := strings.ToLower(part)
		if lower == "sd" || btnDisallowedSDResolutionToken(lower) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ".")
}

func btnDisallowedSDResolutionToken(value string) bool {
	if value == "480p" || value == "576p" {
		return false
	}
	return regexp.MustCompile(`^\d{3,4}[pi]$`).MatchString(value)
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
	value = strings.ReplaceAll(value, "&", " and ")
	value = strings.NewReplacer("'", "", "’", "").Replace(value)
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
