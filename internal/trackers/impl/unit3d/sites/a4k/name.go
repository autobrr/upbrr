// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package a4k

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	upscaleTokenRegex = regexp.MustCompile(`(?i)(^|[^[:alnum:]])upscaled?([^[:alnum:]]|$)`)
	openMatteRegex    = regexp.MustCompile(`(?i)(^|[^[:alnum:]])open[ ._-]matte([^[:alnum:]]|$)`)
	noDNRRegex        = regexp.MustCompile(`(?i)(^|[^[:alnum:]])no[ ._-]?dnr([^[:alnum:]]|$)`)
	versionRegex      = regexp.MustCompile(`(?i)(^|[^[:alnum:]])(v\d+(?:\.\d+)?)([^[:alnum:]]|$)`)
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := markerName(meta)
	if name == "" {
		return ""
	}

	switch typeID(meta) {
	case "7":
		return buildFanResName(meta)
	case "8":
		return buildAIName(meta)
	default:
		return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	}
}

func buildFanResName(meta api.UploadSubject) string {
	title, year := titleAndYear(meta)
	parts := make([]string, 0, 12)
	if title != "" {
		parts = append(parts, title)
	}
	if year != "" {
		parts = append(parts, year)
	}
	parts = append(parts, "FANRES")
	name := markerName(meta)
	if openMatteRegex.MatchString(name) {
		parts = append(parts, "Open Matte")
	}
	if noDNRRegex.MatchString(name) {
		parts = append(parts, "NoDNR")
	}
	parts = append(parts, "2160p", "UHD", "35mm")
	parts = append(parts, audioParts(meta)...)
	if len(meta.AudioLanguages) > 1 {
		parts = append(parts, "Dual-Audio")
	}
	if meta.HDR != "" {
		parts = append(parts, meta.HDR)
	}
	if codec := videoCodec(meta); codec != "" {
		parts = append(parts, codec)
	}
	if match := versionRegex.FindStringSubmatch(name); match != nil {
		parts = append(parts, match[2])
	}
	return cleanName(strings.Join(parts, " "))
}

func buildAIName(meta api.UploadSubject) string {
	title, year := titleAndYear(meta)
	label := "AI Remaster"
	if upscaleTokenRegex.MatchString(markerName(meta)) {
		label = "AI Upscale"
	}
	parts := make([]string, 0, 12)
	if title != "" {
		parts = append(parts, title)
	}
	if year != "" {
		parts = append(parts, year)
	}
	parts = append(parts, "2160p", label)
	if meta.Source != "" {
		parts = append(parts, meta.Source)
	}
	parts = append(parts, audioParts(meta)...)
	if meta.HDR != "" {
		parts = append(parts, meta.HDR)
	}
	if codec := videoCodec(meta); codec != "" {
		parts = append(parts, codec)
	}
	group := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-"))
	if group == "" || unit3d.IsNoGroupTag(group) {
		group = "NOGRP"
	}
	return cleanName(strings.Join(parts, " ")) + "-" + group
}

func titleAndYear(meta api.UploadSubject) (string, string) {
	title := strings.TrimSpace(meta.Release.Title)
	tmdb := meta.ProviderMetadata.TMDB
	if title == "" && tmdb != nil {
		title = strings.TrimSpace(tmdb.Title)
	}
	if title == "" && tmdb != nil {
		title = strings.TrimSpace(tmdb.OriginalTitle)
	}
	year := meta.Release.Year
	if year == 0 && tmdb != nil {
		year = tmdb.Year
	}
	if year == 0 {
		return title, ""
	}
	return title, strconv.Itoa(year)
}

func audioParts(meta api.UploadSubject) []string {
	parts := make([]string, 0, 2)
	if meta.Audio != "" {
		parts = append(parts, meta.Audio)
	}
	if meta.Channels != "" && !strings.Contains(meta.Audio, meta.Channels) {
		parts = append(parts, meta.Channels)
	}
	return parts
}

func videoCodec(meta api.UploadSubject) string {
	if meta.VideoEncode != "" {
		return meta.VideoEncode
	}
	return meta.VideoCodec
}

func cleanName(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}
