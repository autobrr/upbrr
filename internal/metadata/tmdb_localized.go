// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

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

		if posterPath, ok := mainData["poster_path"].(string); ok && posterPath != "" {
			result.Poster = "https://image.tmdb.org/t/p/original" + posterPath
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
				for i := len(results) - 1; i >= 0; i-- {
					if video, ok := results[i].(map[string]any); ok {
						if site, ok := video["site"].(string); ok && site == "YouTube" {
							if key, ok := video["key"].(string); ok && key != "" {
								result.TrailerURL = "http://www.youtube.com/watch?v=" + key
								break
							}
						}
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
