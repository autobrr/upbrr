// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var otwSeasonPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:S|Season[ ._-]*)(\d{1,3})`)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	if isOTWNonDiscMultiSeason(meta) {
		return ""
	}
	title, year := currentOTWTMDBTitleYear(meta)
	if title == "" {
		title = strings.TrimSpace(meta.Release.Title)
	}
	if year <= 0 {
		year = meta.Release.Year
	}
	parts := []string{title}
	if year > 0 {
		parts = append(parts, strconv.Itoa(year))
	}
	parts = append(parts, otwSeasonEpisode(meta), otwEpisodeTitle(meta), meta.Repack, meta.Release.Resolution)
	if isOTWCompleteDisc(meta) {
		parts = append(parts, meta.Distributor, meta.Region)
	}
	parts = append(parts,
		meta.Is3D,
		meta.Service,
		otwSource(meta),
		otwType(meta),
	)
	if isOTWCompleteDisc(meta) || strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		parts = append(parts, meta.HDR, otwVideoCodec(meta), otwAudio(meta))
	} else {
		parts = append(parts, otwAudio(meta), meta.HDR, otwVideoCodec(meta))
	}
	name := strings.Join(nonEmptyOTWElements(parts), " ")
	if tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")); tag != "" {
		name = strings.TrimRight(name, " -") + "-" + tag
	}
	return strings.Join(strings.Fields(name), " ")
}

func currentOTWTMDBTitleYear(meta api.UploadSubject) (string, int) {
	metadata := meta.ProviderMetadata.TMDB
	if metadata == nil || meta.Identity.TMDBID <= 0 || metadata.TMDBID != meta.Identity.TMDBID ||
		!otwProviderMetadataCurrent(meta) {
		return "", 0
	}
	return strings.TrimSpace(metadata.Title), metadata.Year
}

func otwProviderMetadataCurrent(meta api.UploadSubject) bool {
	return otwSourceMatches(meta.Identity.SourcePath, meta.SourcePath) &&
		otwSourceMatches(meta.ProviderMetadata.SourcePath, meta.SourcePath) &&
		otwGenerationMatches(meta.ProviderMetadata.Generation, meta.Identity.Generation)
}

func otwSourceMatches(scopedPath, currentPath string) bool {
	scopedPath = strings.TrimSpace(scopedPath)
	return scopedPath == "" || strings.EqualFold(scopedPath, strings.TrimSpace(currentPath))
}

func otwGenerationMatches(scoped, current api.PreparedGeneration) bool {
	if scoped == 0 && current == 0 {
		return true
	}
	return scoped > 0 && scoped == current
}

func otwSeasonEpisode(meta api.UploadSubject) string {
	if value := strings.TrimSpace(meta.DailyEpisodeDate); value != "" {
		return value
	}
	if value := strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr); value != "" {
		return value
	}
	switch {
	case meta.SeasonInt > 0 && meta.EpisodeInt > 0:
		return "S" + otwTwoDigits(meta.SeasonInt) + "E" + otwTwoDigits(meta.EpisodeInt)
	case meta.SeasonInt > 0:
		return "S" + otwTwoDigits(meta.SeasonInt)
	default:
		return ""
	}
}

func otwEpisodeTitle(meta api.UploadSubject) string {
	if meta.TVPack || strings.TrimSpace(meta.DailyEpisodeDate) != "" || meta.EpisodeInt <= 0 && strings.TrimSpace(meta.EpisodeStr) == "" {
		return ""
	}
	return strings.TrimSpace(meta.EpisodeTitle)
}

func otwTwoDigits(value int) string {
	if value >= 0 && value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func isOTWCompleteDisc(meta api.UploadSubject) bool {
	return strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") ||
		unit3d.IsDiscType(meta.DiscType) ||
		unit3d.IsDiscType(meta.Disc.Type)
}

func isOTWNonDiscMultiSeason(meta api.UploadSubject) bool {
	if isOTWCompleteDisc(meta) {
		return false
	}
	seasons := make(map[string]struct{})
	for _, value := range append([]string{
		meta.SeasonStr,
		meta.ReleaseName,
		meta.ReleaseNameNoTag,
	}, meta.FileList...) {
		for _, match := range otwSeasonPattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				seasons[match[1]] = struct{}{}
			}
		}
	}
	return len(seasons) > 1
}

func otwSource(meta api.UploadSubject) string {
	source := strings.TrimSpace(meta.Source)
	if source == "" {
		source = strings.TrimSpace(meta.Release.Source)
	}
	if (strings.EqualFold(meta.Type, "WEBDL") || strings.EqualFold(meta.Type, "WEBRIP")) &&
		strings.EqualFold(source, "WEB") {
		return ""
	}
	return source
}

func otwType(meta api.UploadSubject) string {
	var typeValue string
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "", "DISC", "ENCODE", "HDTV":
		typeValue = ""
	case "WEBDL":
		typeValue = "WEB-DL"
	case "WEBRIP":
		typeValue = "WEBRip"
	case "REMUX":
		typeValue = "REMUX"
	default:
		typeValue = strings.TrimSpace(meta.Type)
	}
	if strings.EqualFold(typeValue, otwSource(meta)) {
		return ""
	}
	return typeValue
}

func otwVideoCodec(meta api.UploadSubject) string {
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

func otwAudio(meta api.UploadSubject) string {
	audio := strings.Join(strings.Fields(meta.Audio), " ")
	channels := strings.Join(strings.Fields(meta.Channels), " ")
	if audio == "" {
		return channels
	}
	if channels == "" || strings.Contains(strings.ToUpper(audio), strings.ToUpper(channels)) {
		return audio
	}
	return audio + " " + channels
}

func nonEmptyOTWElements(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.Join(strings.Fields(value), " "); value != "" {
			result = append(result, value)
		}
	}
	return result
}
