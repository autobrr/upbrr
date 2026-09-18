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

var openMatteRegex = regexp.MustCompile(`(?i)(^|[^[:alnum:]])open[ ._-]matte([^[:alnum:]]|$)`)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if name == "" {
		return ""
	}
	switch typeID(meta) {
	case "7":
		return buildFanResName(meta)
	case "8":
		return buildAIName(meta)
	default:
		return cleanName(name)
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
	if openMatteRegex.MatchString(meta.Edition) {
		parts = append(parts, "Open Matte")
	}
	if a4kHasOther(meta, "no-DNR", "No Digital Noise Reduction") {
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
	if version := strings.TrimSpace(meta.Release.Version); version != "" {
		parts = append(parts, version)
	}
	return cleanName(strings.Join(parts, " "))
}

func buildAIName(meta api.UploadSubject) string {
	title, year := titleAndYear(meta)
	label := "AI Remaster"
	if a4kHasOther(meta, "AI.Upscale", "upscaled (ai)", "upscaled") {
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
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		tmdb = nil
	}
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		title = strings.TrimSpace(meta.EffectiveMetadata.Title)
	} else if title == "" && tmdb != nil {
		title = strings.TrimSpace(tmdb.Title)
		if title == "" {
			title = strings.TrimSpace(tmdb.OriginalTitle)
		}
	}
	year := meta.Release.Year
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		year = meta.EffectiveMetadata.Year
	} else if year == 0 && tmdb != nil {
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
