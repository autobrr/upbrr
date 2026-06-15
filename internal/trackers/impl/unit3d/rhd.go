// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	rhdRegradedRegex   = regexp.MustCompile(`(?i)(^|[.\-_ ])regraded([.\-_ ]|$)`)
	rhdUpscaleRegex    = regexp.MustCompile(`(?i)(^|[.\-_ ])(upscaled?|upscl|upsuhd)([.\-_ ]|$)`)
	rhdInternalRegex   = regexp.MustCompile(`(?i)(^|[.\-_ ])internal([.\-_ ]|$)`)
	rhdIncompleteRegex = regexp.MustCompile(`(?i)(^|[.\-_ ])incomplete([.\-_ ]|$)`)
	rhdDubbedRegex     = regexp.MustCompile(`(?i)(^|[.\-_ ])(dubbed|synced|ac3d|ld|line|mic|md)([.\-_ ]|$)`)
)

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

func buildRHDName(meta api.PreparedMetadata) string {
	parts := make([]string, 0)
	isFullDisc := meta.Type == "DISC" || isDiscType(meta.DiscType)

	// 1. Title (German title preferred)
	title := ""
	if meta.ExternalMetadata.TMDB != nil && meta.ExternalMetadata.TMDB.LocalizedTitles != nil {
		if t, ok := meta.ExternalMetadata.TMDB.LocalizedTitles["de"]; ok && t != "" {
			title = t
		}
	}
	if title == "" {
		title = meta.Release.Title
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

		if rhdIncompleteRegex.MatchString(meta.ReleaseName) {
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

	// 4. Language
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
		if rhdRegradedRegex.MatchString(meta.ReleaseName) {
			parts = append(parts, "REGRADED")
		}
		if rhdUpscaleRegex.MatchString(meta.ReleaseName) {
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
	if rhdInternalRegex.MatchString(meta.ReleaseName) {
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

func resolveRHDTypeAndSource(meta api.PreparedMetadata) []string {
	var parts []string
	if meta.Type == "" {
		return nil
	}

	typeName := meta.Type
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

func resolveRHDLanguage(meta api.PreparedMetadata) string {
	hasGermanAudio := false
	for _, l := range meta.AudioLanguages {
		if isGermanLanguage(l) {
			hasGermanAudio = true
			break
		}
	}

	numAudio := len(meta.AudioLanguages)
	var baseTag string

	if hasGermanAudio {
		baseTag = "GERMAN"
	} else {
		// No German audio, check subs
		hasGermanSubs := false
		for _, l := range meta.SubtitleLanguages {
			if isGermanLanguage(l) {
				hasGermanSubs = true
				break
			}
		}
		if hasGermanSubs {
			return "GERMAN SUBBED"
		}

		// Use main language name
		if numAudio > 0 {
			baseTag = getRHDLanguageName(meta.AudioLanguages[0])
		} else {
			baseTag = "ENGLISH"
		}
	}

	isDubbed := rhdDubbedRegex.MatchString(meta.ReleaseName)
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

func isGermanLanguage(l string) bool {
	tag, ok := parseLanguageTag(l)
	if !ok {
		l = strings.ToLower(strings.TrimSpace(l))
		for _, g := range []string{"german", "ger", "de", "deu", "gsw"} {
			if l == g {
				return true
			}
		}
		return false
	}
	base, _ := tag.Base()
	return base.String() == "de"
}

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
