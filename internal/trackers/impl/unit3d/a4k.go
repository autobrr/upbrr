// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	a4kAIRegex        = rhdTokenRegex(`ai`)
	a4kUpscaleRegex   = rhdTokenRegex(`upscaled?`, `upscl`)
	a4k35mmRegex      = rhdTokenRegex(`35mm`)
	a4kOpenMatteRegex = regexp.MustCompile(`(?i)(^|[^[:alnum:]])open[ ._-]matte([^[:alnum:]]|$)`)
	a4kNoDNRRegex     = regexp.MustCompile(`(?i)(^|[^[:alnum:]])no[ ._-]?dnr([^[:alnum:]]|$)`)
	a4kVersionRegex   = regexp.MustCompile(`(?i)(^|[^[:alnum:]])(v\d+(?:\.\d+)?)([^[:alnum:]]|$)`)
)

func siteA4KProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveTypeID:       resolveUnit3DA4KTypeID,
		resolveResolutionID: resolveUnit3DA4KResolutionID,
	}
}

// resolveUnit3DA4KTypeID classifies A4K's FanRes and AI Remaster/Upscale
// types from filename hints before falling back to the standard
// DISC/REMUX/WEBDL/ENCODE inference. A4K only accepts 2160p movie/TV
// uploads, so no other type values apply.
func resolveUnit3DA4KTypeID(meta api.PreparedMetadata) string {
	if a4kUpscaleRegex.MatchString(meta.ReleaseName) || a4kAIRegex.MatchString(meta.ReleaseName) {
		return "8" // AI Remastered
	}
	if a4k35mmRegex.MatchString(meta.ReleaseName) || a4kNoDNRRegex.MatchString(meta.ReleaseName) || a4kVersionRegex.MatchString(meta.ReleaseName) {
		return "7" // Fanres
	}

	mapping := map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"ENCODE": "3",
	}
	return mapping[inferUnit3DType(meta)]
}

// resolveUnit3DA4KResolutionID reports A4K's 2160p resolution ID. A4K only
// accepts 2160p uploads, so anything else falls back to the site-wide
// "Other" resolution ID.
func resolveUnit3DA4KResolutionID(meta api.PreparedMetadata) string {
	if resolveResolution(meta) == "2160p" {
		return "2"
	}
	return "10"
}

// buildA4KName builds A4K's custom FanRes/AI Remaster/AI Upscale release
// names, which follow a fixed template rather than the standard scene
// release name. Other A4K types keep the standard release name unchanged.
func buildA4KName(name string, meta api.PreparedMetadata) string {
	switch resolveUnit3DA4KTypeID(meta) {
	case "7":
		return buildA4KFanResName(meta)
	case "8":
		return buildA4KAIName(meta)
	default:
		return name
	}
}

func buildA4KFanResName(meta api.PreparedMetadata) string {
	title, year := a4kTitleAndYear(meta)

	parts := make([]string, 0, 12)
	if title != "" {
		parts = append(parts, title)
	}
	if year != "" {
		parts = append(parts, year)
	}
	parts = append(parts, "FANRES")

	if a4kOpenMatteRegex.MatchString(meta.ReleaseName) {
		parts = append(parts, "Open Matte")
	}
	if a4kNoDNRRegex.MatchString(meta.ReleaseName) {
		parts = append(parts, "NoDNR")
	}

	parts = append(parts, "2160p", "UHD", "35mm")
	parts = append(parts, a4kAudioParts(meta)...)

	if resolveDPAudioLabel(meta.AudioLanguages) == "Dual-Audio" {
		parts = append(parts, "Dual-Audio")
	}
	if meta.HDR != "" {
		parts = append(parts, meta.HDR)
	}
	if videoCodec := a4kVideoCodec(meta); videoCodec != "" {
		parts = append(parts, videoCodec)
	}
	if match := a4kVersionRegex.FindStringSubmatch(meta.ReleaseName); match != nil {
		parts = append(parts, match[2])
	}

	return strings.TrimSpace(strings.Join(strings.Fields(strings.Join(parts, " ")), " "))
}

func buildA4KAIName(meta api.PreparedMetadata) string {
	title, year := a4kTitleAndYear(meta)

	label := "AI Remaster"
	if a4kUpscaleRegex.MatchString(meta.ReleaseName) {
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
	parts = append(parts, a4kAudioParts(meta)...)
	if meta.HDR != "" {
		parts = append(parts, meta.HDR)
	}

	name := strings.TrimSpace(strings.Join(strings.Fields(strings.Join(parts, " ")), " "))

	group := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-"))
	if group == "" || isNoGroupTag(group) {
		group = "NOGRP"
	}

	if videoCodec := a4kVideoCodec(meta); videoCodec != "" {
		name += " " + videoCodec
	}
	return name + "-" + group
}

// a4kTitleAndYear resolves the release title and year from parsed release
// metadata, falling back to TMDB when unset.
func a4kTitleAndYear(meta api.PreparedMetadata) (string, string) {
	title := strings.TrimSpace(meta.Release.Title)
	tmdb := meta.ExternalMetadata.TMDB
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

// a4kAudioParts returns the audio codec segment followed by the channel
// layout, skipping the channel layout when it is already part of the codec
// string.
func a4kAudioParts(meta api.PreparedMetadata) []string {
	var parts []string
	if meta.Audio != "" {
		parts = append(parts, meta.Audio)
	}
	if meta.Channels != "" && !strings.Contains(meta.Audio, meta.Channels) {
		parts = append(parts, meta.Channels)
	}
	return parts
}

func a4kVideoCodec(meta api.PreparedMetadata) string {
	if meta.VideoEncode != "" {
		return meta.VideoEncode
	}
	return meta.VideoCodec
}
