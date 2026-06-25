// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	rhdRegradedRegex   = rhdTokenRegex(`regraded`)
	rhdUpscaleRegex    = rhdTokenRegex(`upscaled?`, `upscl`, `upsuhd`)
	rhdInternalRegex   = rhdTokenRegex(`internal`)
	rhdIncompleteRegex = rhdTokenRegex(`incomplete`)
	rhdDubbedRegex     = rhdTokenRegex(`dubbed`, `synced`, `ac3d`, `ld`, `line`, `mic`, `md`)
)

// rhdTokenRegex matches RHD marker tokens only when they are delimited outside
// alphanumeric release-name text.
func rhdTokenRegex(tokens ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[^[:alnum:]])(?:` + strings.Join(tokens, "|") + `)([^[:alnum:]]|$)`)
}

func siteRHDProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveResolutionID: resolveUnit3DRHDResolutionID,
	}
}

func resolveUnit3DRHDResolutionID(meta api.PreparedMetadata) string {
	resolution := resolveResolution(meta)
	mapping := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "12",
		"576i":  "13",
		"480p":  "11",
		"480i":  "18",
	}
	if value, ok := mapping[resolution]; ok {
		return value
	}
	return "10"
}

// buildRHDName formats RocketHD names. Full-disc names intentionally omit the
// language tag segment even though RHD upload rules still require German audio.
func buildRHDName(meta api.PreparedMetadata) string {
	parts := make([]string, 0)
	isFullDisc := strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") || isDiscType(meta.DiscType)
	markerText := rhdMarkerText(meta)

	// 1. Title (German title preferred)
	title := ""
	tmdb := meta.ExternalMetadata.TMDB
	if tmdb != nil && tmdb.LocalizedTitles != nil {
		if t, ok := tmdb.LocalizedTitles["de"]; ok && strings.TrimSpace(t) != "" {
			title = strings.TrimSpace(t)
		}
	}
	if title == "" {
		title = strings.TrimSpace(meta.Release.Title)
	}
	if title == "" && tmdb != nil {
		title = strings.TrimSpace(tmdb.Title)
	}
	if title == "" && tmdb != nil {
		title = strings.TrimSpace(tmdb.OriginalTitle)
	}
	if title != "" {
		parts = append(parts, title)
	}

	// 2. Year (always)
	year := meta.Release.Year
	if year == 0 && meta.ExternalMetadata.TMDB != nil {
		year = meta.ExternalMetadata.TMDB.Year
	}
	yearStr := strconv.Itoa(year)
	if year == 0 {
		yearStr = "0000"
	}
	parts = append(parts, yearStr)

	// 2.5 Season / Episode
	if meta.SeasonStr != "" || meta.EpisodeStr != "" {
		parts = append(parts, strings.TrimSpace(meta.SeasonStr+meta.EpisodeStr))

		if rhdIncompleteRegex.MatchString(markerText) {
			parts = append(parts, "iNCOMPLETE")
		}
	}

	// 3. CUT / Edition / 3D
	if meta.Edition != "" {
		parts = append(parts, meta.Edition)
	}
	if meta.Is3D != "" {
		parts = append(parts, meta.Is3D)
	}

	// 4. Language. RHD full-disc naming uses COMPLETE/region/source details
	// instead of GERMAN/DL/ML language tags.
	if !isFullDisc {
		parts = append(parts, resolveRHDLanguage(meta))
	}

	// 5. REPACK
	if meta.Repack != "" {
		parts = append(parts, meta.Repack)
	}

	// 6. Resolution
	if meta.Release.Resolution != "" {
		parts = append(parts, meta.Release.Resolution)

		// Rocket-HD naming requires REGRADED and UPSCALE tags after resolution
		if rhdRegradedRegex.MatchString(markerText) {
			parts = append(parts, "REGRADED")
		}
		if rhdUpscaleRegex.MatchString(markerText) {
			parts = append(parts, "UPSCALE")
		}
	}

	// 7. Service/UHD
	if strings.Contains(strings.ToUpper(meta.Type), "WEB") && meta.Service != "" {
		parts = append(parts, meta.Service)
	} else if meta.UHD != "" {
		parts = append(parts, meta.UHD)
	}

	// 8. Type/Source
	parts = append(parts, resolveRHDTypeAndSource(meta)...)

	// 9. AudioCodec & Channels
	if meta.Audio != "" {
		parts = append(parts, meta.Audio)
	}
	if meta.Channels != "" && !strings.Contains(meta.Audio, meta.Channels) {
		parts = append(parts, meta.Channels)
	}

	// 10. HDR
	if meta.HDR != "" {
		parts = append(parts, meta.HDR)
	}

	// 11. BitDepth
	if meta.BitDepth != "" && meta.BitDepth != "8" && meta.BitDepth != "0" {
		if strings.Contains(strings.ToLower(meta.BitDepth), "bit") {
			parts = append(parts, meta.BitDepth)
		} else {
			parts = append(parts, meta.BitDepth+"bit")
		}
	}

	// 12. VideoCodec
	videoCodec := meta.VideoEncode
	if videoCodec == "" {
		videoCodec = meta.VideoCodec
	}
	if videoCodec != "" {
		parts = append(parts, videoCodec)
	}

	// 13. INTERNAL
	if rhdInternalRegex.MatchString(markerText) {
		parts = append(parts, "iNTERNAL")
	}

	// Group
	group := meta.Tag
	if group == "" || isNoGroupTag(group) {
		group = "NOGRP"
	} else {
		group = strings.TrimPrefix(group, "-")
	}

	name := strings.Join(parts, " ")
	name = strings.Join(strings.Fields(name), " ")
	return name + "-" + group
}

// resolveRHDTypeAndSource formats RHD source/type segments, promoting an empty
// type with a disc-like DiscType to the full-disc COMPLETE form.
func resolveRHDTypeAndSource(meta api.PreparedMetadata) []string {
	var parts []string
	typeName := strings.TrimSpace(meta.Type)
	if typeName == "" && isDiscType(meta.DiscType) {
		typeName = "DISC"
	}
	if typeName == "" {
		return nil
	}

	switch strings.ToUpper(typeName) {
	case "WEBDL":
		typeName = "WEB-DL"
	case "WEBRIP":
		typeName = "WEBRip"
	case "ENCODE":
		if meta.Source != "" {
			parts = append(parts, meta.Source)
			typeName = ""
		}
	case "REMUX":
		if meta.Source != "" {
			parts = append(parts, meta.Source)
		}
	case "DISC":
		parts = append(parts, "COMPLETE")
		if meta.Region != "" {
			parts = append(parts, meta.Region)
		}
		if meta.Release.Source != "" {
			parts = append(parts, meta.Release.Source)
		}
		if meta.Release.Size != "" {
			parts = append(parts, meta.Release.Size)
		}
		typeName = ""
	}

	if typeName != "" {
		parts = append(parts, typeName)
	}
	return parts
}

// rhdMarkerText strips the terminal release group tag before scanning for RHD
// marker tokens, so short group tags do not look like language or source flags.
func rhdMarkerText(meta api.PreparedMetadata) string {
	value := strings.TrimSpace(meta.ReleaseName)
	tag := strings.TrimSpace(meta.Tag)
	if value == "" || tag == "" {
		return value
	}
	tag = strings.TrimPrefix(tag, "-")
	if tag == "" {
		return value
	}
	for _, suffix := range []string{"-" + tag, "." + tag, "_" + tag, " " + tag} {
		if strings.HasSuffix(strings.ToLower(value), strings.ToLower(suffix)) {
			return strings.TrimSpace(value[:len(value)-len(suffix)])
		}
	}
	return value
}

// resolveRHDLanguage builds the RHD language segment from unique parseable audio
// languages, with German subtitle-only and dubbed markers handled separately.
func resolveRHDLanguage(meta api.PreparedMetadata) string {
	audioLanguages := normalizedRHDAudioLanguages(meta.AudioLanguages)
	hasGermanAudio := slices.ContainsFunc(audioLanguages, isGermanLanguage)

	numAudio := len(audioLanguages)
	var baseTag string

	if hasGermanAudio {
		baseTag = "GERMAN"
	} else {
		// No German audio, check subs
		hasGermanSubs := slices.ContainsFunc(meta.SubtitleLanguages, isGermanLanguage)
		if hasGermanSubs {
			return "GERMAN SUBBED"
		}

		// Use main language name
		if numAudio > 0 {
			baseTag = getRHDLanguageName(audioLanguages[0])
		} else {
			baseTag = "ENGLISH"
		}
	}

	isDubbed := rhdDubbedRegex.MatchString(rhdMarkerText(meta))
	if isDubbed {
		baseTag += " DUBBED"
	}

	if numAudio == 2 {
		return baseTag + " DL"
	} else if numAudio > 2 {
		return baseTag + " ML"
	}
	return baseTag
}

// normalizedRHDAudioLanguages returns first-seen base language subtags, dropping
// blank, unparsable, undefined, and duplicate audio entries before DL/ML counting.
func normalizedRHDAudioLanguages(values []string) []string {
	languages := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag, ok := parseLanguageTag(value)
		if !ok {
			continue
		}
		base, _ := tag.Base()
		key := base.String()
		if key == "" || key == "und" {
			continue
		}
		if isGermanLanguage(key) {
			key = "de"
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		languages = append(languages, key)
	}
	return languages
}

// isGermanLanguage accepts common German names, ISO codes, and Swiss German
// tags for RHD audio and subtitle language decisions.
func isGermanLanguage(l string) bool {
	tag, ok := parseLanguageTag(l)
	if !ok {
		l = strings.ToLower(strings.TrimSpace(l))
		return slices.Contains([]string{"german", "ger", "de", "deu", "gsw"}, l)
	}
	base, _ := tag.Base()
	switch base.String() {
	case "de", "gsw":
		return true
	default:
		return false
	}
}

// getRHDLanguageName returns the English display name RHD expects for a
// parseable language tag, falling back to the uppercased input.
func getRHDLanguageName(l string) string {
	tag, ok := parseLanguageTag(l)
	if !ok {
		return strings.ToUpper(strings.TrimSpace(l))
	}
	name := display.Languages(language.English).Name(tag)
	if name == "" {
		return strings.ToUpper(strings.TrimSpace(l))
	}
	return strings.ToUpper(name)
}
