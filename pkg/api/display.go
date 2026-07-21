// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// PreparedReleaseDisplay is the canonical presentation projection for one
// exact prepared generation.
type PreparedReleaseDisplay struct {
	ReleaseName string
	Providers   []ProviderDisplay
}

// ProviderDisplay is one provider-local display projection. Details contains
// exactly one payload matching Provider.
type ProviderDisplay struct {
	Provider         IdentityProvider
	ID               int
	DisplayID        string
	URL              string
	Provenance       IdentityProvenance
	SummaryAvailable bool
	Summary          ProviderDisplaySummary
	Details          ProviderDisplayDetails
}

// ProviderDisplaySummary is the normalized provider presentation shared by
// CLI, WebUI preview, and history.
type ProviderDisplaySummary struct {
	Title            string
	OriginalTitle    string
	Year             int
	Overview         string
	PosterURL        string
	BackdropURL      string
	Category         string
	Date             string
	EndDate          string
	OriginalLanguage string
	MediaType        string
	RuntimeMinutes   int
	Genres           string
	Keywords         string
	TrailerURL       string
	Rating           float64
	RatingCount      int
	Country          string
}

// ProviderDisplayDetails is a closed typed provider payload union. Projection
// validation guarantees exactly one non-nil field matching Provider.
type ProviderDisplayDetails struct {
	TMDB    *TMDBMetadata    `json:",omitempty"`
	IMDB    *IMDBMetadata    `json:",omitempty"`
	TVDB    *TVDBMetadata    `json:",omitempty"`
	TVmaze  *TVmazeMetadata  `json:",omitempty"`
	AniList *AniListMetadata `json:",omitempty"`
}
