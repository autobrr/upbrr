// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"strconv"
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// resolveUploadName preserves explicit scene names and applies MTV's generated
// title, season, source, and group normalization to derived names.
func resolveUploadName(meta api.UploadSubject) string {
	if meta.Scene {
		if sceneName := strings.TrimSpace(meta.SceneName); sceneName != "" {
			return sceneName
		}
	}
	name := selectedMTVReleaseName(meta)
	if mtvCategory(meta) != "TV" && !isGeneratedMTVReleaseName(meta, name) {
		return cleanName(name)
	}
	return buildGeneratedMTVName(meta)
}

func selectedMTVReleaseName(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename} {
		if name := strings.TrimSpace(candidate); name != "" {
			return name
		}
	}
	return strings.TrimSpace(pathutil.Base(meta.SourcePath))
}

func isGeneratedMTVReleaseName(meta api.UploadSubject, name string) bool {
	name = strings.TrimSpace(name)
	for _, variant := range []api.ReleaseNameVariant{
		meta.GeneratedReleaseNames.IncludeEpisodeTitle,
		meta.GeneratedReleaseNames.OmitEpisodeTitle,
	} {
		for _, candidate := range []string{variant.Name, variant.NameNoTag, variant.CleanName} {
			if name != "" && name == strings.TrimSpace(candidate) {
				return true
			}
		}
	}
	return false
}

func buildGeneratedMTVName(meta api.UploadSubject) string {
	parts := []string{mtvTitle(meta)}
	if mtvCategory(meta) == "TV" {
		parts = append(parts, mtvTVYear(meta), mtvSeasonEpisode(meta), mtvEpisodeTitle(meta))
	} else if year := mtvMovieYear(meta); year > 0 {
		parts = append(parts, strconv.Itoa(year))
	}
	parts = append(parts,
		meta.Edition,
		meta.Release.Resolution,
		meta.Service,
		mtvSource(meta),
		meta.Audio,
		mtvVideoCodec(meta),
	)
	name := cleanName(strings.Join(nonEmptyMTVElements(parts), " "))
	if group := mtvGroupElement(meta); group != "" {
		name = strings.TrimRight(name, ".-") + "-" + group
	}
	return name
}

func cleanName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " ", ".")
	for strings.Contains(value, "..") {
		value = strings.ReplaceAll(value, "..", ".")
	}
	return strings.Trim(strings.TrimSpace(value), ".")
}

// resolveSearchName derives the stable duplicate-search name from the same
// source-scoped metadata used by MTV upload naming.
func resolveSearchName(meta api.UploadSubject) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return resolveUploadName(meta)
	}
	if meta.Identity.IMDBID != 0 || meta.Identity.TMDBID != 0 ||
		(meta.Identity.TVDBID != 0 && strings.EqualFold(string(meta.Identity.Category), string(api.CanonicalCategoryTV))) {
		return resolveUploadName(meta)
	}
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	query = strings.ReplaceAll(query, ": ", " ")
	query = strings.ReplaceAll(query, "’", "")
	query = strings.ReplaceAll(query, "'", "")
	return strings.Join(strings.Fields(query), " ")
}

func mtvTitle(meta api.UploadSubject) string {
	if mtvCategory(meta) == "TV" && matchingMTVTVDBMetadata(meta) {
		if title := strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish); title != "" {
			return title
		}
	}
	if meta.ProviderMetadata.TMDB != nil && mtvProviderMetadataCurrent(meta) &&
		meta.Identity.TMDBID > 0 && meta.ProviderMetadata.TMDB.TMDBID == meta.Identity.TMDBID {
		if title := strings.TrimSpace(meta.ProviderMetadata.TMDB.Title); title != "" {
			return title
		}
	}
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	return selectedMTVReleaseName(meta)
}

func mtvCategory(meta api.UploadSubject) string {
	for _, value := range []string{string(meta.Identity.Category), meta.Release.Category} {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "TV" || value == "MOVIE" {
			return value
		}
	}
	return ""
}

func matchingMTVTVDBMetadata(meta api.UploadSubject) bool {
	metadata := meta.ProviderMetadata.TVDB
	return metadata != nil && meta.Identity.TVDBID > 0 && metadata.TVDBID == meta.Identity.TVDBID &&
		mtvProviderMetadataCurrent(meta)
}

func mtvProviderMetadataCurrent(meta api.UploadSubject) bool {
	return meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity)
}

func mtvTVYear(meta api.UploadSubject) string {
	if !matchingMTVTVDBMetadata(meta) {
		return ""
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if !evidence.IncludeYear || evidence.SeriesYear <= 0 {
		return ""
	}
	return strconv.Itoa(evidence.SeriesYear)
}

func mtvMovieYear(meta api.UploadSubject) int {
	if meta.ProviderMetadata.TMDB != nil && mtvProviderMetadataCurrent(meta) &&
		meta.Identity.TMDBID > 0 && meta.ProviderMetadata.TMDB.TMDBID == meta.Identity.TMDBID &&
		meta.ProviderMetadata.TMDB.Year > 0 {
		return meta.ProviderMetadata.TMDB.Year
	}
	return meta.Release.Year
}

func mtvSeasonEpisode(meta api.UploadSubject) string {
	if value := strings.TrimSpace(meta.DailyEpisodeDate); value != "" {
		return value
	}
	if value := strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr); value != "" {
		return value
	}
	switch {
	case meta.SeasonInt > 0 && meta.EpisodeInt > 0:
		return "S" + mtvTwoDigits(meta.SeasonInt) + "E" + mtvTwoDigits(meta.EpisodeInt)
	case meta.SeasonInt > 0:
		return "S" + mtvTwoDigits(meta.SeasonInt)
	default:
		return ""
	}
}

func mtvEpisodeTitle(meta api.UploadSubject) string {
	if meta.TVPack || strings.TrimSpace(meta.DailyEpisodeDate) != "" || meta.EpisodeInt <= 0 && strings.TrimSpace(meta.EpisodeStr) == "" {
		return ""
	}
	return strings.TrimSpace(meta.EpisodeTitle)
}

func mtvTwoDigits(value int) string {
	if value >= 0 && value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func mtvSource(meta api.UploadSubject) string {
	source := strings.TrimSpace(meta.Source)
	if source == "" {
		source = strings.TrimSpace(meta.Release.Source)
	}
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "WEBDL":
		if source == "" || strings.EqualFold(source, "WEB") {
			return "WEB-DL"
		}
	case "WEBRIP":
		if source == "" || strings.EqualFold(source, "WEB") {
			return "WEBRip"
		}
	case "HDTV":
		if source == "" {
			return "HDTV"
		}
	}
	return source
}

func mtvVideoCodec(meta api.UploadSubject) string {
	for _, value := range []string{meta.VideoEncode, meta.VideoCodec} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	if len(meta.Release.Codec) > 0 {
		return strings.TrimSpace(meta.Release.Codec[0])
	}
	return ""
}

func mtvGroupElement(meta api.UploadSubject) string {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	switch strings.ToUpper(tag) {
	case "", "NOGRP", "NOGROUP", "UNKNOWN", "UNK":
		if meta.Scene {
			return "SCENE"
		}
		return "NOGRP"
	case "SCENE":
		if meta.Scene {
			return "SCENE"
		}
		return "NOGRP"
	case "P2P":
		return "P2P"
	default:
		return tag
	}
}

func nonEmptyMTVElements(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.Join(strings.Fields(value), " "); value != "" {
			result = append(result, value)
		}
	}
	return result
}
