// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"errors"
	"strconv"
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func releaseNamePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.WithMovieYearProvider(trackers.WithEpisodeTitleMode(
		trackers.NewReleaseNamePolicy("standalone/hdb/v2", resolveReleaseNames),
		api.EpisodeTitleModeOmit,
	), api.IdentityProviderIMDB)
}

// resolveReleaseNames returns HDB's upload/search projections and rejects stale
// provider metadata instead of mixing it with the current prepared generation.
func resolveReleaseNames(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
	meta := input.Subject
	if exact := exactHDBReleaseName(meta); exact != "" {
		return trackers.ResolvedReleaseNames{Upload: exact}, nil
	}
	originalTitle := authoritativeHDBOriginalTitle(meta)
	if originalTitle == "" {
		return trackers.ResolvedReleaseNames{}, errors.New("current matching IMDb original title is required to generate the HDB release name")
	}
	name := buildGeneratedHDBName(meta, originalTitle)
	if name == "" {
		return trackers.ResolvedReleaseNames{}, errors.New("structured HDB release name is empty")
	}
	return trackers.ResolvedReleaseNames{Upload: name}, nil
}

func exactHDBReleaseName(meta api.UploadSubject) string {
	sceneName := strings.TrimSpace(meta.SceneName)
	if sceneName != "" && !meta.SceneRenamed {
		return sceneName
	}
	name := selectedHDBReleaseName(meta)
	if name == "" || isGeneratedHDBReleaseName(meta, name) {
		return ""
	}
	if meta.SceneRenamed && sceneName != "" && strings.EqualFold(name, sceneName) {
		return ""
	}
	return name
}

func selectedHDBReleaseName(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename} {
		if name := strings.TrimSpace(candidate); name != "" {
			return name
		}
	}
	return strings.TrimSpace(pathutil.Base(meta.SourcePath))
}

func isGeneratedHDBReleaseName(meta api.UploadSubject, name string) bool {
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

func authoritativeHDBOriginalTitle(meta api.UploadSubject) string {
	metadata := meta.ProviderMetadata.IMDB
	if metadata == nil || meta.Identity.IMDBID <= 0 || metadata.IMDBID != meta.Identity.IMDBID ||
		!hdbProviderMetadataCurrent(meta) {
		return ""
	}
	title := strings.TrimSpace(metadata.AKA)
	if len(title) > len("AKA ") && strings.EqualFold(title[:len("AKA ")], "AKA ") {
		title = strings.TrimSpace(title[len("AKA "):])
	}
	return title
}

func hdbProviderMetadataCurrent(meta api.UploadSubject) bool {
	return hdbSourceMatches(meta.Identity.SourcePath, meta.SourcePath) &&
		hdbSourceMatches(meta.ProviderMetadata.SourcePath, meta.SourcePath) &&
		hdbGenerationMatches(meta.ProviderMetadata.Generation, meta.Identity.Generation)
}

func hdbSourceMatches(scopedPath, currentPath string) bool {
	scopedPath = strings.TrimSpace(scopedPath)
	return scopedPath == "" || strings.EqualFold(scopedPath, strings.TrimSpace(currentPath))
}

func hdbGenerationMatches(scoped, current api.PreparedGeneration) bool {
	if scoped == 0 && current == 0 {
		return true
	}
	return scoped > 0 && scoped == current
}

func buildGeneratedHDBName(meta api.UploadSubject, originalTitle string) string {
	parts := []string{normalizedHDBElement(originalTitle)}
	if hdbCategory(meta) == "TV" {
		parts = append(parts, hdbSeasonEpisode(meta))
	} else {
		parts = append(parts, hdbIMDbYear(meta))
	}
	for _, optional := range []string{meta.Edition, meta.Repack} {
		if !prohibitedHDBElement(optional) {
			parts = append(parts, normalizedHDBElement(optional))
		}
	}
	parts = append(parts,
		normalizedHDBElement(meta.Release.Resolution),
		hdbSourceElement(meta),
		hdbVideoCodecElement(meta),
		hdbAudioElement(meta),
	)
	name := strings.Join(nonEmptyHDBElements(parts), " ")
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag != "" && !prohibitedHDBElement(tag) {
		name = strings.TrimRight(name, " -") + "-" + tag
	}
	return strings.TrimSpace(name)
}

func hdbCategory(meta api.UploadSubject) string {
	for _, value := range []string{string(meta.Identity.Category), meta.Release.Category} {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "TV" || value == "MOVIE" {
			return value
		}
	}
	return ""
}

func hdbIMDbYear(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		return strconv.Itoa(meta.ProviderMetadata.IMDB.Year)
	}
	if meta.Release.Year > 0 {
		return strconv.Itoa(meta.Release.Year)
	}
	return ""
}

func hdbSeasonEpisode(meta api.UploadSubject) string {
	if value := normalizedHDBElement(meta.DailyEpisodeDate); value != "" {
		return value
	}
	if value := normalizedHDBElement(meta.SeasonStr + meta.EpisodeStr); value != "" {
		return value
	}
	switch {
	case meta.SeasonInt > 0 && meta.EpisodeInt > 0:
		return "S" + hdbTwoDigits(meta.SeasonInt) + "E" + hdbTwoDigits(meta.EpisodeInt)
	case meta.EpisodeInt > 0:
		return "E" + hdbTwoDigits(meta.EpisodeInt)
	case meta.SeasonInt > 0:
		return "S" + hdbTwoDigits(meta.SeasonInt)
	default:
		return ""
	}
}

func hdbTwoDigits(value int) string {
	if value >= 0 && value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func hdbSourceElement(meta api.UploadSubject) string {
	source := normalizedHDBElement(meta.Source)
	if source == "" {
		source = normalizedHDBElement(meta.Release.Source)
	}
	typeValue := strings.ToUpper(strings.TrimSpace(meta.Type))
	if source == "" || strings.EqualFold(source, "WEB") {
		switch typeValue {
		case "WEBDL":
			source = "WEB-DL"
		case "WEBRIP":
			source = "WEBRip"
		case "HDTV":
			source = "HDTV"
		}
	}
	region := normalizedHDBElement(meta.Region)
	if source != "" && region != "" {
		return source + " " + region
	}
	return source
}

func hdbVideoCodecElement(meta api.UploadSubject) string {
	for _, value := range []string{meta.VideoEncode, meta.VideoCodec} {
		if value = normalizedHDBElement(value); value != "" {
			return value
		}
	}
	if len(meta.Release.Codec) > 0 {
		return normalizedHDBElement(meta.Release.Codec[0])
	}
	return ""
}

func hdbAudioElement(meta api.UploadSubject) string {
	audio := normalizedHDBElement(meta.Audio)
	channels := normalizedHDBElement(meta.Channels)
	if audio == "" {
		return channels
	}
	if channels == "" || strings.Contains(strings.ToUpper(audio), strings.ToUpper(channels)) {
		return audio
	}
	return audio + " " + channels
}

func normalizedHDBElement(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func prohibitedHDBElement(value string) bool {
	for _, token := range strings.FieldsFunc(strings.ToUpper(strings.TrimSpace(value)), func(r rune) bool {
		return r == ' ' || r == '_' || r == '/'
	}) {
		switch token {
		case "REQ", "RESEED", "COMPLETE", "SEASON", "SERIES", "LIMITED", "SUBBED", "NFOFIX", "DVD5", "DVD9", "DL", "MULTI-LANG":
			return true
		}
	}
	return false
}

func nonEmptyHDBElements(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = normalizedHDBElement(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
