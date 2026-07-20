// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	regradedRegex   = tokenRegex(`regraded`)
	upscaleRegex    = tokenRegex(`upscaled?`, `upscl`, `upsuhd`)
	internalRegex   = tokenRegex(`internal`)
	incompleteRegex = tokenRegex(`incomplete`)
	dubbedRegex     = tokenRegex(`dubbed`, `synced`, `ac3d`, `ld`, `line`, `mic`, `md`)
)

// Profile returns RHD's German-oriented release-name construction, resolution
// mapping, tracker-owned rules, and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "RHD",
		BaseURL:      "https://rocket-hd.cc",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			ResolveResolutionID: resolutionID,
		},
	}
}

func tokenRegex(tokens ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[^[:alnum:]])(?:` + strings.Join(tokens, "|") + `)([^[:alnum:]]|$)`)
}

func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "12",
		"576i":  "13",
		"480p":  "11",
		"480i":  "18",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	parts := make([]string, 0)
	fullDisc := strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") || unit3d.IsDiscType(meta.DiscType)
	markers := markerText(meta)
	title := ""
	tmdb := meta.ProviderMetadata.TMDB
	if tmdb != nil && tmdb.LocalizedTitles != nil {
		title = strings.TrimSpace(tmdb.LocalizedTitles["de"])
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
	year := meta.Release.Year
	if year == 0 && tmdb != nil {
		year = tmdb.Year
	}
	if year == 0 {
		parts = append(parts, "0000")
	} else {
		parts = append(parts, strconv.Itoa(year))
	}
	if meta.SeasonStr != "" || meta.EpisodeStr != "" {
		parts = append(parts, strings.TrimSpace(meta.SeasonStr+meta.EpisodeStr))
		if incompleteRegex.MatchString(markers) {
			parts = append(parts, "iNCOMPLETE")
		}
	}
	if meta.Edition != "" {
		parts = append(parts, meta.Edition)
	}
	if meta.Is3D != "" {
		parts = append(parts, meta.Is3D)
	}
	if !fullDisc {
		parts = append(parts, resolveLanguage(meta))
	}
	if meta.Repack != "" {
		parts = append(parts, meta.Repack)
	}
	if meta.Release.Resolution != "" {
		parts = append(parts, meta.Release.Resolution)
		if regradedRegex.MatchString(markers) {
			parts = append(parts, "REGRADED")
		}
		if upscaleRegex.MatchString(markers) {
			parts = append(parts, "UPSCALE")
		}
	}
	if strings.Contains(strings.ToUpper(meta.Type), "WEB") && meta.Service != "" {
		parts = append(parts, meta.Service)
	} else if meta.UHD != "" {
		parts = append(parts, meta.UHD)
	}
	parts = append(parts, typeAndSource(meta)...)
	if meta.Audio != "" {
		parts = append(parts, meta.Audio)
	}
	if meta.Channels != "" && !strings.Contains(meta.Audio, meta.Channels) {
		parts = append(parts, meta.Channels)
	}
	if meta.HDR != "" {
		parts = append(parts, meta.HDR)
	}
	if meta.BitDepth != "" && meta.BitDepth != "8" && meta.BitDepth != "0" {
		if strings.Contains(strings.ToLower(meta.BitDepth), "bit") {
			parts = append(parts, meta.BitDepth)
		} else {
			parts = append(parts, meta.BitDepth+"bit")
		}
	}
	codec := meta.VideoEncode
	if codec == "" {
		codec = meta.VideoCodec
	}
	if codec != "" {
		parts = append(parts, codec)
	}
	if internalRegex.MatchString(markers) {
		parts = append(parts, "iNTERNAL")
	}
	group := meta.Tag
	if group == "" || unit3d.IsNoGroupTag(group) {
		group = "NOGRP"
	} else {
		group = strings.TrimPrefix(group, "-")
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ") + "-" + group
}

func typeAndSource(meta api.UploadSubject) []string {
	parts := []string{}
	name := strings.TrimSpace(meta.Type)
	if name == "" && unit3d.IsDiscType(meta.DiscType) {
		name = "DISC"
	}
	if name == "" {
		return nil
	}
	switch strings.ToUpper(name) {
	case "WEBDL":
		name = "WEB-DL"
	case "WEBRIP":
		name = "WEBRip"
	case "ENCODE":
		if meta.Source != "" {
			parts = append(parts, meta.Source)
			name = ""
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
		name = ""
	}
	if name != "" {
		parts = append(parts, name)
	}
	return parts
}

func markerText(meta api.UploadSubject) string {
	value, tag := strings.TrimSpace(meta.ReleaseName), strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
	if value == "" || tag == "" {
		return value
	}
	for _, suffix := range []string{"-" + tag, "." + tag, "_" + tag, " " + tag} {
		if strings.HasSuffix(strings.ToLower(value), strings.ToLower(suffix)) {
			return strings.TrimSpace(value[:len(value)-len(suffix)])
		}
	}
	return value
}

func resolveLanguage(meta api.UploadSubject) string {
	languages := normalizedAudioLanguages(meta.AudioLanguages)
	german := slices.ContainsFunc(languages, isGerman)
	var base string
	switch {
	case german:
		base = "GERMAN"
	case slices.ContainsFunc(meta.SubtitleLanguages, isGerman):
		return "GERMAN SUBBED"
	case len(languages) > 0:
		base = languageName(languages[0])
	default:
		base = "ENGLISH"
	}
	if dubbedRegex.MatchString(markerText(meta)) {
		base += " DUBBED"
	}
	if len(languages) == 2 {
		return base + " DL"
	}
	if len(languages) > 2 {
		return base + " ML"
	}
	return base
}

func normalizedAudioLanguages(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag, ok := unit3d.ParseLanguageTag(value)
		if !ok {
			continue
		}
		base, _ := tag.Base()
		key := base.String()
		if key == "" || key == "und" {
			continue
		}
		if isGerman(key) {
			key = "de"
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}
func isGerman(value string) bool {
	tag, ok := unit3d.ParseLanguageTag(value)
	if !ok {
		return slices.Contains([]string{"german", "ger", "de", "deu", "gsw"}, strings.ToLower(strings.TrimSpace(value)))
	}
	base, _ := tag.Base()
	return base.String() == "de" || base.String() == "gsw"
}
func languageName(value string) string {
	tag, ok := unit3d.ParseLanguageTag(value)
	if !ok {
		return strings.ToUpper(strings.TrimSpace(value))
	}
	name := display.Languages(language.English).Name(tag)
	if name == "" {
		return strings.ToUpper(strings.TrimSpace(value))
	}
	return strings.ToUpper(name)
}
