// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

const tmdbOriginalImageBaseURL = "https://image.tmdb.org/t/p/original"

// parseTMDBLocalizedData extracts the pt-BR TMDB fields used by localized
// tracker uploads, keeping season or episode overview text even when TMDB does
// not return a localized scoped title.
func parseTMDBLocalizedData(mainData, seasonData, episodeData map[string]any) api.TMDBLocalizedData {
	var result api.TMDBLocalizedData

	if mainData != nil {
		if title, ok := mainData["title"].(string); ok && title != "" {
			result.Title = title
		} else if name, ok := mainData["name"].(string); ok && name != "" {
			result.Title = name
		}

		if overview, ok := mainData["overview"].(string); ok {
			result.Overview = overview
		}

		if posterPath, ok := mainData["poster_path"].(string); ok {
			result.Poster = localizedPosterURL(posterPath)
		}

		if genres, ok := mainData["genres"].([]any); ok {
			var genreNames []string
			for _, g := range genres {
				if genreMap, ok := g.(map[string]any); ok {
					if name, ok := genreMap["name"].(string); ok && name != "" {
						genreNames = append(genreNames, name)
					}
				}
			}
			result.Genres = strings.Join(genreNames, ", ")
		}

		if videos, ok := mainData["videos"].(map[string]any); ok {
			if results, ok := videos["results"].([]any); ok {
				for _, item := range results {
					video, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if trailerURL := localizedTrailerURL(video); trailerURL != "" {
						result.TrailerURL = trailerURL
						break
					}
				}
			}
		}

		if contentRating := localizedTVContentRating(mainData); contentRating != "" {
			result.ContentRating = contentRating
		} else if releaseRating := localizedMovieReleaseRating(mainData); releaseRating != "" {
			result.ContentRating = releaseRating
		}
	}

	if episodeData != nil {
		if name, ok := episodeData["name"].(string); ok {
			result.EpisodeTitle = name
		}
		if overview, ok := episodeData["overview"].(string); ok {
			result.EpisodeOverview = overview
		}
	} else if seasonData != nil {
		if name, ok := seasonData["name"].(string); ok {
			result.EpisodeTitle = name // Used as fallback for season packs
		}
		if overview, ok := seasonData["overview"].(string); ok {
			result.EpisodeOverview = overview
		}
	}

	return result
}

// localizedTVContentRating extracts a Brazilian TV rating from TMDB
// content_ratings data, falling back to the first US rating when needed.
func localizedTVContentRating(mainData map[string]any) string {
	contentRatings, ok := mainData["content_ratings"].(map[string]any)
	if !ok {
		return ""
	}
	results, ok := contentRatings["results"].([]any)
	if !ok {
		return ""
	}
	var brRating string
	var usRating string
	for _, r := range results {
		ratingObj, ok := r.(map[string]any)
		if !ok {
			continue
		}
		iso, _ := ratingObj["iso_3166_1"].(string)
		rating, _ := ratingObj["rating"].(string)
		if iso == "BR" {
			if formatted := formatBRContentRating(rating); formatted != "" {
				brRating = formatted
				break
			}
		}
		if iso == "US" && usRating == "" {
			usRating = rating
		}
	}
	return metautil.FirstNonEmptyTrimmed(brRating, usRating)
}

// localizedMovieReleaseRating extracts a Brazilian movie certification from
// TMDB release_dates data, falling back to the first US certification.
func localizedMovieReleaseRating(mainData map[string]any) string {
	releaseDates, ok := mainData["release_dates"].(map[string]any)
	if !ok {
		return ""
	}
	results, ok := releaseDates["results"].([]any)
	if !ok {
		return ""
	}
	var brRating string
	var usRating string
	for _, r := range results {
		country, ok := r.(map[string]any)
		if !ok {
			continue
		}
		iso, _ := country["iso_3166_1"].(string)
		dates, _ := country["release_dates"].([]any)
		certification := firstCertification(dates)
		if iso == "BR" {
			if formatted := formatBRContentRating(certification); formatted != "" {
				brRating = formatted
				break
			}
		}
		if iso == "US" && usRating == "" {
			usRating = certification
		}
	}
	return metautil.FirstNonEmptyTrimmed(brRating, usRating)
}

func firstCertification(releaseDates []any) string {
	for _, item := range releaseDates {
		releaseDate, ok := item.(map[string]any)
		if !ok {
			continue
		}
		certification, _ := releaseDate["certification"].(string)
		if strings.TrimSpace(certification) != "" {
			return certification
		}
	}
	return ""
}

func formatBRContentRating(rating string) string {
	switch strings.TrimSpace(rating) {
	case "L":
		return "Livre"
	case "10", "12", "14", "16", "18":
		return strings.TrimSpace(rating) + " anos"
	default:
		return ""
	}
}

// localizedPosterURL returns a usable poster URL from a TMDB poster_path value.
// Relative paths are resolved against the TMDB original image base; absolute
// http and https URLs are preserved for callers that pass already-expanded data.
func localizedPosterURL(posterPath string) string {
	posterPath = strings.TrimSpace(posterPath)
	if posterPath == "" {
		return ""
	}

	parsed, err := url.Parse(posterPath)
	if err != nil {
		return ""
	}
	if parsed.IsAbs() {
		if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
			return posterPath
		}
		return ""
	}
	if strings.Contains(posterPath, "://") || strings.HasPrefix(posterPath, "//") || strings.Contains(posterPath, "\\") {
		return ""
	}

	return tmdbOriginalImageBaseURL + "/" + strings.TrimPrefix(posterPath, "/")
}

// localizedTrailerURL returns the YouTube watch URL for official TMDB trailer entries only.
func localizedTrailerURL(video map[string]any) string {
	site, ok := video["site"].(string)
	if !ok || site != "YouTube" {
		return ""
	}
	videoType, ok := video["type"].(string)
	if !ok || videoType != "Trailer" {
		return ""
	}
	key, ok := video["key"].(string)
	if !ok || key == "" {
		return ""
	}

	return "https://www.youtube.com/watch?v=" + key
}
