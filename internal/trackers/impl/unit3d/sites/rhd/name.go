// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	parts := make([]string, 0)
	fullDisc := strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") || unit3d.IsDiscType(meta.DiscType)
	providerTitle := ""
	tmdb := meta.ProviderMetadata.TMDB
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		tmdb = nil
	}
	if tmdb != nil && tmdb.LocalizedTitles != nil {
		providerTitle = strings.TrimSpace(tmdb.LocalizedTitles["de"])
	}
	if providerTitle == "" {
		providerTitle = strings.TrimSpace(meta.Release.Title)
	}
	if providerTitle == "" && tmdb != nil {
		providerTitle = strings.TrimSpace(tmdb.Title)
	}
	if providerTitle == "" && tmdb != nil {
		providerTitle = strings.TrimSpace(tmdb.OriginalTitle)
	}
	title := providerTitle
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		title = trackers.PreferredTitle(meta, providerTitle)
	}
	if title != "" {
		parts = append(parts, title)
	}
	providerYear := meta.Release.Year
	if providerYear == 0 && tmdb != nil {
		providerYear = tmdb.Year
	}
	year := providerYear
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		year = trackers.PreferredYear(meta, providerYear)
	}
	if year > 0 {
		parts = append(parts, strconv.Itoa(year))
	}
	if meta.DailyEpisodeDate != "" {
		parts = append(parts, meta.DailyEpisodeDate)
	} else if meta.SeasonStr != "" || meta.EpisodeStr != "" {
		parts = append(parts, strings.TrimSpace(meta.SeasonStr+meta.EpisodeStr))
		if rhdHasOther(meta, "Incomplete") {
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
		if rhdHasOther(meta, "Regraded") {
			parts = append(parts, "REGRADED")
		}
		if rhdHasOther(meta, "upscaled", "UPSCL", "UPSUHD") {
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
	if rhdHasOther(meta, "Internal") {
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
		source := strings.TrimSpace(meta.Release.Source)
		size := strings.TrimSpace(meta.Release.Size)
		if strings.EqualFold(meta.DiscType, "DVD") &&
			(strings.EqualFold(size, "DVD5") || strings.EqualFold(size, "DVD9")) &&
			(strings.EqualFold(source, "DVD") || strings.EqualFold(source, "PAL DVD") || strings.EqualFold(source, "NTSC DVD")) {
			source = strings.TrimSpace(source[:len(source)-len("DVD")])
		}
		if source != "" {
			parts = append(parts, source)
		}
		if size != "" {
			parts = append(parts, size)
		}
		name = ""
	}
	if name != "" {
		parts = append(parts, name)
	}
	return parts
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
	if rhdHasAudio(meta, "ac3d", "line") || rhdHasOther(meta, "LD", "MD", "MIC") ||
		slices.ContainsFunc(meta.Release.Language, func(value string) bool {
			return strings.EqualFold(strings.TrimSpace(value), "Dubbed") || strings.EqualFold(strings.TrimSpace(value), "Synced")
		}) {
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

func rhdHasAudio(meta api.UploadSubject, candidates ...string) bool {
	for _, value := range meta.Release.Audio {
		for _, candidate := range candidates {
			if strings.EqualFold(strings.TrimSpace(value), candidate) {
				return true
			}
		}
	}
	return false
}

func rhdHasOther(meta api.UploadSubject, candidates ...string) bool {
	for _, value := range meta.Release.Other {
		for _, candidate := range candidates {
			if strings.EqualFold(strings.TrimSpace(value), candidate) {
				return true
			}
		}
	}
	return false
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
