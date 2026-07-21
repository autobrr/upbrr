// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveDisplay validates and projects one exact prepared generation into its
// canonical provider presentation.
func (m *Module) ResolveDisplay(ctx context.Context, ref api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
	owned, err := m.resolveEnvelope(ctx, ref)
	if err != nil {
		return api.PreparedReleaseDisplay{}, err
	}
	return projectPreparedReleaseDisplay(owned.result.Release)
}

func projectPreparedReleaseDisplay(release api.PreparedRelease) (api.PreparedReleaseDisplay, error) {
	if reason := providerIdentityMismatch(release); reason != "" {
		return api.PreparedReleaseDisplay{}, &IncompatiblePreparationError{SourcePath: release.Source.SourcePath, Reason: reason}
	}
	detached, err := release.Clone()
	if err != nil {
		return api.PreparedReleaseDisplay{}, fmt.Errorf("prepared release: clone display source: %w", err)
	}
	release = detached
	display := api.PreparedReleaseDisplay{ReleaseName: release.Naming.ReleaseName}
	identity := release.Identity
	metadata := release.ProviderMetadata
	if value := metadata.TMDB; value != nil {
		summary := api.ProviderDisplaySummary{
			Title:            firstNonEmpty(value.Title, release.Naming.ReleaseName),
			OriginalTitle:    value.OriginalTitle,
			Year:             value.Year,
			Overview:         value.Overview,
			PosterURL:        value.Poster,
			BackdropURL:      value.Backdrop,
			Category:         firstNonEmpty(value.Category, displayIdentityCategory(identity), value.TMDBType),
			Date:             firstNonEmpty(value.ReleaseDate, value.FirstAirDate),
			EndDate:          value.LastAirDate,
			OriginalLanguage: value.OriginalLanguage,
			MediaType:        value.TMDBType,
			RuntimeMinutes:   value.Runtime,
			Genres:           value.Genres,
			Keywords:         value.Keywords,
			TrailerURL:       value.YouTube,
			Country:          strings.Join(value.OriginCountry, ", "),
		}
		display.Providers = append(display.Providers, providerDisplay(
			api.IdentityProviderTMDB,
			identity.TMDBID,
			identity.Provenance.TMDB,
			tmdbDisplayURL(identity.TMDBID, summary.Category),
			summary,
			api.ProviderDisplayDetails{TMDB: value},
		))
	}
	if value := metadata.IMDB; value != nil {
		summary := api.ProviderDisplaySummary{
			Title:            firstNonEmpty(value.Title, release.Naming.ReleaseName),
			Year:             value.Year,
			Overview:         value.Plot,
			PosterURL:        value.Cover,
			Category:         firstNonEmpty(displayIdentityCategory(identity), value.Type),
			OriginalLanguage: value.OriginalLanguage,
			MediaType:        value.Type,
			RuntimeMinutes:   value.RuntimeMinutes,
			Genres:           value.Genres,
			Rating:           value.Rating,
			RatingCount:      value.RatingCount,
			Country:          value.Country,
		}
		display.Providers = append(display.Providers, providerDisplay(
			api.IdentityProviderIMDB,
			identity.IMDBID,
			identity.Provenance.IMDB,
			fmt.Sprintf("https://www.imdb.com/title/tt%07d", identity.IMDBID),
			summary,
			api.ProviderDisplayDetails{IMDB: value},
		))
	}
	if value := metadata.TVDB; value != nil {
		summary := api.ProviderDisplaySummary{
			Title:            firstNonEmpty(value.Name, release.Naming.ReleaseName),
			Year:             value.Year,
			Overview:         value.Overview,
			PosterURL:        value.Poster,
			Category:         firstNonEmpty(displayIdentityCategory(identity), value.Type),
			Date:             value.FirstAired,
			OriginalLanguage: value.OriginalLanguage,
			MediaType:        value.Type,
			Genres:           value.Genres,
			Country:          value.OriginalCountry,
		}
		display.Providers = append(display.Providers, providerDisplay(
			api.IdentityProviderTVDB,
			identity.TVDBID,
			identity.Provenance.TVDB,
			fmt.Sprintf("https://www.thetvdb.com/?id=%d&tab=series", identity.TVDBID),
			summary,
			api.ProviderDisplayDetails{TVDB: value},
		))
	}
	if value := metadata.TVmaze; value != nil {
		runtimeMinutes := value.Runtime
		if runtimeMinutes == 0 {
			runtimeMinutes = value.AverageRuntime
		}
		summary := api.ProviderDisplaySummary{
			Title:            firstNonEmpty(value.Name, release.Naming.ReleaseName),
			Overview:         value.Summary,
			PosterURL:        value.Poster,
			BackdropURL:      value.Backdrop,
			Category:         firstNonEmpty(displayIdentityCategory(identity), value.Type),
			Date:             value.Premiered,
			EndDate:          value.Ended,
			OriginalLanguage: value.Language,
			MediaType:        value.Type,
			RuntimeMinutes:   runtimeMinutes,
			Genres:           value.Genres,
			Rating:           value.Rating,
			RatingCount:      value.Weight,
			Country:          value.Country,
		}
		display.Providers = append(display.Providers, providerDisplay(
			api.IdentityProviderTVmaze,
			identity.TVmazeID,
			identity.Provenance.TVmaze,
			fmt.Sprintf("https://www.tvmaze.com/shows/%d", identity.TVmazeID),
			summary,
			api.ProviderDisplayDetails{TVmaze: value},
		))
	}
	if value := metadata.AniList; value != nil {
		summary := api.ProviderDisplaySummary{
			Title:            firstNonEmpty(value.TitleEnglish, value.TitleRomaji, release.Naming.ReleaseName),
			OriginalTitle:    value.TitleRomaji,
			Year:             value.SeasonYear,
			Overview:         value.Description,
			PosterURL:        firstNonEmpty(value.CoverExtraLarge, value.CoverLarge, value.CoverMedium),
			BackdropURL:      value.BannerImage,
			Category:         firstNonEmpty(value.Format, displayIdentityCategory(identity)),
			Date:             value.StartDate,
			EndDate:          value.EndDate,
			OriginalLanguage: strings.ToLower(value.CountryOfOrigin),
			MediaType:        value.Format,
			RuntimeMinutes:   value.Duration,
			Genres:           strings.Join(value.Genres, ", "),
			Rating:           float64(value.AverageScore) / 10,
			RatingCount:      value.Popularity,
		}
		display.Providers = append(display.Providers, providerDisplay(
			api.IdentityProviderMAL,
			identity.MALID,
			identity.Provenance.MAL,
			fmt.Sprintf("https://myanimelist.net/anime/%d", identity.MALID),
			summary,
			api.ProviderDisplayDetails{AniList: value},
		))
	}
	return display, nil
}

func providerDisplay(
	provider api.IdentityProvider,
	id int,
	provenance api.IdentityProvenance,
	url string,
	summary api.ProviderDisplaySummary,
	details api.ProviderDisplayDetails,
) api.ProviderDisplay {
	displayID := strconv.Itoa(id)
	if provider == api.IdentityProviderIMDB {
		displayID = fmt.Sprintf("tt%07d", id)
	}
	return api.ProviderDisplay{
		Provider:         provider,
		ID:               id,
		DisplayID:        displayID,
		URL:              url,
		Provenance:       provenance,
		SummaryAvailable: providerSummaryAvailable(summary),
		Summary:          summary,
		Details:          details,
	}
}

func providerSummaryAvailable(summary api.ProviderDisplaySummary) bool {
	return strings.TrimSpace(summary.Title) != "" ||
		strings.TrimSpace(summary.OriginalTitle) != "" ||
		summary.Year != 0 ||
		strings.TrimSpace(summary.Overview) != "" ||
		strings.TrimSpace(summary.PosterURL) != "" ||
		strings.TrimSpace(summary.BackdropURL) != "" ||
		strings.TrimSpace(summary.Category) != "" ||
		strings.TrimSpace(summary.Date) != "" ||
		strings.TrimSpace(summary.EndDate) != "" ||
		strings.TrimSpace(summary.OriginalLanguage) != "" ||
		strings.TrimSpace(summary.MediaType) != "" ||
		summary.RuntimeMinutes != 0 ||
		strings.TrimSpace(summary.Genres) != "" ||
		strings.TrimSpace(summary.Keywords) != "" ||
		strings.TrimSpace(summary.TrailerURL) != "" ||
		summary.Rating != 0 ||
		summary.RatingCount != 0 ||
		strings.TrimSpace(summary.Country) != ""
}

func displayIdentityCategory(identity api.ExternalIdentity) string {
	if identity.Category == api.CanonicalCategoryUnknown {
		return ""
	}
	return string(identity.Category)
}

func tmdbDisplayURL(id int, category string) string {
	kind := "movie"
	if strings.EqualFold(strings.TrimSpace(category), "tv") {
		kind = "tv"
	}
	return fmt.Sprintf("https://www.themoviedb.org/%s/%d", kind, id)
}
