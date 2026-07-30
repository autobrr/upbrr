// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		if meta.ProviderMetadata.TMDB.Localized != nil {
			if localized, ok := meta.ProviderMetadata.TMDB.Localized["pt-BR"]; ok && strings.TrimSpace(localized.Poster) != "" {
				return strings.TrimSpace(localized.Poster)
			}
		}
		if strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster) != "" {
			return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
		}
	}
	switch {
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster)
	default:
		return ""
	}
}

func resolveOverview(meta api.UploadSubject, answers map[string]string) string {
	if strings.TrimSpace(answers["overview"]) != "" {
		return strings.TrimSpace(answers["overview"])
	}
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if shouldUseScopedTVOverview(meta) && ptBR.EpisodeOverview != "" {
		return strings.TrimSpace(ptBR.EpisodeOverview)
	}
	if ptBR.Overview != "" {
		return strings.TrimSpace(ptBR.Overview)
	}
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Overview) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Overview)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}

// shouldUseScopedTVOverview reports whether ASC should prefer season or
// episode localized overview over title-level synopsis text.
func shouldUseScopedTVOverview(meta api.UploadSubject) bool {
	if meta.SeasonInt <= 0 {
		return false
	}
	if categoryOf(meta) != "TV" {
		return false
	}
	if meta.TVPack {
		return true
	}
	return meta.EpisodeInt > 0
}

func resolveGenres(meta api.UploadSubject, answers map[string]string) string {
	if strings.TrimSpace(answers["genre"]) != "" {
		return strings.TrimSpace(answers["genre"])
	}
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)

	// 1. Use localized if available
	if ptBR.Genres != "" {
		genres := strings.Split(strings.TrimSpace(ptBR.Genres), ",")
		out := make([]string, 0, len(genres))
		for _, genre := range genres {
			g := strings.TrimSpace(genre)
			capitalized := metautil.CapitalizeGenre(g)
			if capitalized != "" {
				out = append(out, capitalized)
			}
		}
		return strings.Join(out, ", ")
	}

	// 2. Use metautil.TranslateGenreToPortugueseStrict to translate
	var genreText string
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.TVDB.Genres)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.TVmaze.Genres)
	default:
		genreText = strings.TrimSpace(meta.Release.Genre)
	}

	if genreText == "" {
		return ""
	}

	genres := strings.Split(genreText, ",")
	out := make([]string, 0, len(genres))
	for _, genre := range genres {
		g := strings.TrimSpace(genre)
		if g == "" {
			continue
		}
		translated := metautil.TranslateGenreToPortugueseStrict(g)
		if translated == "" {
			translated = g
		}
		capitalized := metautil.CapitalizeGenre(translated)
		if capitalized != "" {
			out = append(out, capitalized)
		}
	}
	return strings.Join(out, ", ")
}

func resolveTrailer(meta api.UploadSubject) string {
	value := ""
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if ptBR.TrailerURL != "" {
		value = ptBR.TrailerURL
	}
	if value == "" && meta.ProviderMetadata.TMDB != nil {
		value = strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return "https://www.youtube.com/watch?v=" + value
}

func resolveIMDbIDText(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func resolveOriginalLanguage(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.OriginalLanguage)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Language) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Language)
	default:
		return ""
	}
}

func resolveRuntime(meta api.UploadSubject) string {
	minutes := 0
	switch {
	case meta.ProviderMetadata.TMDB != nil:
		minutes = meta.ProviderMetadata.TMDB.Runtime
	case meta.ProviderMetadata.IMDB != nil:
		minutes = meta.ProviderMetadata.IMDB.RuntimeMinutes
	case meta.ProviderMetadata.TVmaze != nil:
		minutes = meta.ProviderMetadata.TVmaze.Runtime
	}
	if minutes <= 0 {
		return ""
	}
	hours := minutes / 60
	remain := minutes % 60
	if hours == 0 {
		return fmt.Sprintf("%02d minutos", remain)
	}
	return fmt.Sprintf("%d hora%s e %02d minutos", hours, pluralSuffix(hours), remain)
}

func resolveCountries(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.ProductionCountries) > 0 {
		names := make([]string, 0, len(meta.ProviderMetadata.TMDB.ProductionCountries))
		for _, country := range meta.ProviderMetadata.TMDB.ProductionCountries {
			if strings.TrimSpace(country.Name) != "" {
				names = append(names, strings.TrimSpace(country.Name))
			}
		}
		return strings.Join(names, ", ")
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.CountryList)
	}
	return ""
}

func resolveCast(meta api.UploadSubject) []string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Cast) > 0:
		return append([]string{}, meta.ProviderMetadata.TMDB.Cast...)
	case meta.ProviderMetadata.IMDB != nil && len(meta.ProviderMetadata.IMDB.Stars) > 0:
		names := make([]string, 0, len(meta.ProviderMetadata.IMDB.Stars))
		for _, person := range meta.ProviderMetadata.IMDB.Stars {
			if strings.TrimSpace(person.Name) != "" {
				names = append(names, strings.TrimSpace(person.Name))
			}
		}
		return names
	default:
		return nil
	}
}

func resolveReleaseDate(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.ReleaseDate) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.ReleaseDate)
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.FirstAirDate) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.FirstAirDate)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.FirstAired) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.FirstAired)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Premiered) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Premiered)
	default:
		return ""
	}
}

func resolveYear(meta api.UploadSubject) int {
	switch {
	case meta.Release.Year > 0:
		return meta.Release.Year
	case meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0:
		return meta.ProviderMetadata.TMDB.Year
	case meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0:
		return meta.ProviderMetadata.IMDB.Year
	case meta.ProviderMetadata.TVDB != nil && meta.ProviderMetadata.TVDB.Year > 0:
		return meta.ProviderMetadata.TVDB.Year
	default:
		return 0
	}
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}
