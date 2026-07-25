// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// isTVDBCategory reports whether AR descriptions may include a TVDB series link.
// Canonical movie identity suppresses TVDB links even when episode facts exist.
func isTVDBCategory(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func resolveTypeID(meta api.UploadSubject) string {
	genres := strings.ToLower(resolveGenres(meta) + " " + resolveKeywords(meta))
	adultKeywords := []string{"xxx", "erotic", "porn", "adult", "orgy"}
	if (strings.EqualFold(meta.Type, "DISC") || strings.EqualFold(meta.Type, "REMUX")) && strings.EqualFold(meta.Source, "Blu-ray") {
		return "14"
	}
	if meta.Anime {
		if isSD(meta.Release.Resolution) {
			return "15"
		}
		return "16"
	}
	if strings.EqualFold(string(meta.Identity.Category), "TV") {
		if meta.TVPack {
			if isSD(meta.Release.Resolution) {
				return "4"
			}
			if isHighTier(meta.Release.Resolution) {
				return "6"
			}
			return "5"
		}
		if isSD(meta.Release.Resolution) {
			return "0"
		}
		if isHighTier(meta.Release.Resolution) {
			return "2"
		}
		return "1"
	}
	if isSD(meta.Release.Resolution) {
		return "7"
	}
	for _, keyword := range adultKeywords {
		if strings.Contains(genres, keyword) {
			return "13"
		}
	}
	if isHighTier(meta.Release.Resolution) {
		return "9"
	}
	return "8"
}

func resolveTags(meta api.UploadSubject) string {
	values := make([]string, 0, 8)
	if meta.Identity.IMDBID > 0 {
		values = append(values, "tt"+strconv.Itoa(meta.Identity.IMDBID))
	}
	for value := range strings.SplitSeq(resolveGenres(meta), ",") {
		for sub := range strings.SplitSeq(value, "&") {
			tag := strings.TrimSpace(collapseDots(sub))
			if tag == "" {
				continue
			}
			values = append(values, tag)
		}
	}
	return strings.Join(uniqueStrings(values), ", ")
}

func resolveGenres(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Genres) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Genres)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Genres) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Genres)
	default:
		return strings.TrimSpace(meta.Release.Genre)
	}
}

func isSD(resolution string) bool {
	value := strings.ToLower(strings.TrimSpace(resolution))
	return strings.Contains(value, "576") || strings.Contains(value, "480")
}

func isHighTier(resolution string) bool {
	value := strings.ToLower(strings.TrimSpace(resolution))
	return strings.Contains(value, "2160") || strings.Contains(value, "4320") || strings.Contains(value, "8640")
}
