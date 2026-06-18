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

// parseTMDBLocalizedData extracts the pt-BR TMDB fields used by localized tracker uploads.
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

		if contentRatings, ok := mainData["content_ratings"].(map[string]any); ok {
			if results, ok := contentRatings["results"].([]any); ok {
				validBRRatings := map[string]struct{}{"L": {}, "10": {}, "12": {}, "14": {}, "16": {}, "18": {}}
				var brRating string
				var usRating string
				for _, r := range results {
					if ratingObj, ok := r.(map[string]any); ok {
						iso := ""
						if i, ok := ratingObj["iso_3166_1"].(string); ok {
							iso = i
						}
						rating := ""
						if val, ok := ratingObj["rating"].(string); ok {
							rating = val
						}
						if iso == "BR" {
							if _, valid := validBRRatings[rating]; valid {
								if rating == "L" {
									brRating = "Livre"
								} else {
									brRating = rating + " anos"
								}
								break
							}
						}
						if iso == "US" && usRating == "" {
							usRating = rating
						}
					}
				}
				result.ContentRating = metautil.FirstNonEmptyTrimmed(brRating, usRating)
			}
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
