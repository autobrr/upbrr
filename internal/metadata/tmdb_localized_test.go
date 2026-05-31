// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"
)

func TestParseTMDBLocalizedData(t *testing.T) {
	t.Run("empty inputs", func(t *testing.T) {
		res := parseTMDBLocalizedData(nil, nil, nil)
		if res.Title != "" || res.Overview != "" || res.TrailerURL != "" {
			t.Errorf("expected empty result, got %+v", res)
		}
	})

	t.Run("main data fields", func(t *testing.T) {
		main := map[string]any{
			"title":    "Main Title",
			"overview": "Main Overview",
			"genres": []any{
				map[string]any{"id": 1, "name": "Action"},
				map[string]any{"id": 2, "name": "Comedy"},
			},
			"videos": map[string]any{
				"results": []any{
					map[string]any{"site": "YouTube", "type": "Teaser", "key": "teaser"},
					map[string]any{"site": "YouTube", "type": "Trailer", "key": "trailer"},
				},
			},
			"content_ratings": map[string]any{
				"results": []any{
					map[string]any{"iso_3166_1": "US", "rating": "TV-MA"},
					map[string]any{"iso_3166_1": "BR", "rating": "16"},
				},
			},
			"poster_path": "/poster.jpg",
		}

		res := parseTMDBLocalizedData(main, nil, nil)
		if res.Title != "Main Title" {
			t.Errorf("Title: expected 'Main Title', got '%s'", res.Title)
		}
		if res.Overview != "Main Overview" {
			t.Errorf("Overview: expected 'Main Overview', got '%s'", res.Overview)
		}
		if res.Genres != "Action, Comedy" {
			t.Errorf("Genres: expected 'Action, Comedy', got '%s'", res.Genres)
		}
		if res.TrailerURL != "https://www.youtube.com/watch?v=trailer" && res.TrailerURL != "https://www.youtube.com/watch?v=teaser" {
			t.Errorf("TrailerURL: unexpected '%s'", res.TrailerURL)
		}
		if res.ContentRating != "16 anos" {
			t.Errorf("ContentRating: expected '16 anos', got '%s'", res.ContentRating)
		}
		if res.Poster != "https://image.tmdb.org/t/p/original/poster.jpg" {
			t.Errorf("Poster: expected 'https://image.tmdb.org/t/p/original/poster.jpg', got '%s'", res.Poster)
		}
	})

	t.Run("season and episode data", func(t *testing.T) {
		season := map[string]any{
			"name":        "Season 1",
			"overview":    "Season 1 Overview",
			"poster_path": "/season_poster.jpg",
		}
		episode := map[string]any{
			"name":     "Episode 1",
			"overview": "Episode 1 Overview",
		}

		res := parseTMDBLocalizedData(nil, season, episode)
		if res.Title != "" {
			t.Errorf("Title: expected '', got '%s'", res.Title)
		}
		if res.Overview != "" {
			t.Errorf("Overview: expected '', got '%s'", res.Overview)
		}
		if res.EpisodeTitle != "Episode 1" {
			t.Errorf("EpisodeTitle: expected 'Episode 1', got '%s'", res.EpisodeTitle)
		}
		if res.EpisodeOverview != "Episode 1 Overview" {
			t.Errorf("EpisodeOverview: expected 'Episode 1 Overview', got '%s'", res.EpisodeOverview)
		}
		if res.Poster != "" {
			t.Errorf("Poster: expected '', got '%s'", res.Poster)
		}
	})

	t.Run("us rating fallback", func(t *testing.T) {
		main := map[string]any{
			"content_ratings": map[string]any{
				"results": []any{
					map[string]any{"iso_3166_1": "US", "rating": "TV-MA"},
				},
			},
		}
		res := parseTMDBLocalizedData(main, nil, nil)
		if res.ContentRating != "TV-MA" {
			t.Errorf("ContentRating fallback: expected 'TV-MA', got '%s'", res.ContentRating)
		}
	})
}
