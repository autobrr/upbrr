// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// requiresMetadataField selects one provider enrichment from the normalized
// demand union without carrying selected tracker identities into release facts.
func requiresMetadataField(set api.MetadataRequirementSet, category api.CanonicalCategory, field api.MetadataRequirementField) bool {
	for _, requirement := range set.Requirements {
		if requirement.Scope != "" && requirement.Scope != api.MetadataRequirementScopeAny &&
			!strings.EqualFold(string(requirement.Scope), string(category)) {
			continue
		}
		if slices.Contains(requirement.AnyOf, field) {
			return true
		}
	}
	return false
}

// requiresProviderMetadataRefresh reports whether a missing applicable demand
// can be supplied by one provider. It preserves AnyOf semantics by skipping a
// row that is already satisfied by retained evidence or a manual correction.
func requiresProviderMetadataRefresh(
	set api.MetadataRequirementSet,
	category api.CanonicalCategory,
	meta preparationstate.State,
	identity api.ExternalIdentity,
	metadata api.SourceScopedMetadata,
	provider api.IdentityProvider,
) bool {
	for _, requirement := range set.Requirements {
		if requirement.Scope != "" && requirement.Scope != api.MetadataRequirementScopeAny &&
			!strings.EqualFold(string(requirement.Scope), string(category)) {
			continue
		}
		if metadataRequirementPresentForCollection(requirement.AnyOf, meta, identity, metadata) {
			continue
		}
		if slices.ContainsFunc(requirement.AnyOf, func(field api.MetadataRequirementField) bool {
			return providerSuppliesMetadataRequirement(provider, field, meta)
		}) {
			return true
		}
	}
	return false
}

func requiresTMDBLocalizedPTBRRefresh(
	set api.MetadataRequirementSet,
	category api.CanonicalCategory,
	meta preparationstate.State,
	identity api.ExternalIdentity,
	metadata api.SourceScopedMetadata,
) bool {
	const field api.MetadataRequirementField = "tmdb_localized_pt_br"
	if !requiresMetadataField(set, category, field) {
		return false
	}
	for _, requirement := range set.Requirements {
		if requirement.Scope != "" && requirement.Scope != api.MetadataRequirementScopeAny &&
			!strings.EqualFold(string(requirement.Scope), string(category)) {
			continue
		}
		if slices.Contains(requirement.AnyOf, field) &&
			!metadataRequirementPresentForCollection(requirement.AnyOf, meta, identity, metadata) {
			return true
		}
	}
	return false
}

func metadataRequirementPresentForCollection(
	fields []api.MetadataRequirementField,
	meta preparationstate.State,
	identity api.ExternalIdentity,
	metadata api.SourceScopedMetadata,
) bool {
	return slices.ContainsFunc(fields, func(field api.MetadataRequirementField) bool {
		return metadataRequirementFieldPresentForCollection(field, meta, identity, metadata)
	})
}

func metadataRequirementFieldPresentForCollection(
	field api.MetadataRequirementField,
	meta preparationstate.State,
	identity api.ExternalIdentity,
	metadata api.SourceScopedMetadata,
) bool {
	tmdbCurrent := metadata.TMDB != nil && identity.TMDBID > 0 && metadata.TMDB.TMDBID == identity.TMDBID &&
		tmdbMetadataMatchesCategory(metadata.TMDB, identity.Category)
	imdbCurrent := metadata.IMDB != nil && identity.IMDBID > 0 && metadata.IMDB.IMDBID == identity.IMDBID
	tvdbCurrent := metadata.TVDB != nil && identity.TVDBID > 0 && metadata.TVDB.TVDBID == identity.TVDBID
	tvmazeCurrent := metadata.TVmaze != nil && identity.TVmazeID > 0 && metadata.TVmaze.TVmazeID == identity.TVmazeID

	switch field {
	case "tmdb_id_only":
		return identity.TMDBID > 0
	case "imdb_id_only":
		return identity.IMDBID > 0
	case "tvdb_id_only":
		return identity.TVDBID > 0
	case "tvmaze_id_only":
		return identity.TVmazeID > 0
	case "tmdb":
		return tmdbCurrent
	case "imdb":
		return imdbCurrent
	case "tvdb":
		return tvdbCurrent
	case "tvmaze":
		return tvmazeCurrent && strings.TrimSpace(metadata.TVmaze.Name) != ""
	case "tmdb_title":
		return tmdbCurrent && strings.TrimSpace(metadata.TMDB.Title) != ""
	case "imdb_title":
		return imdbCurrent && strings.TrimSpace(metadata.IMDB.Title) != ""
	case "tvdb_title":
		return tvdbCurrent && (strings.TrimSpace(metadata.TVDB.NameEnglish) != "" || strings.TrimSpace(metadata.TVDB.Name) != "")
	case "tvdb_year":
		return tvdbCurrent && metadata.TVDB.Year > 0
	case "tvdb_disambiguation":
		return tvdbCurrent && strings.TrimSpace(metadata.TVDB.NameDisambiguation.Source) != "" &&
			(metadata.TVDB.NameDisambiguation.Status == api.MetadataEvidenceStatusComplete ||
				metadata.TVDB.NameDisambiguation.Status == api.MetadataEvidenceStatusPartial)
	case "tmdb_origin_countries":
		return tmdbCurrent && hasNonEmptyString(metadata.TMDB.OriginCountry)
	case "poster":
		return (tmdbCurrent && strings.TrimSpace(metadata.TMDB.Poster) != "") ||
			(imdbCurrent && strings.TrimSpace(metadata.IMDB.Cover) != "") ||
			(tvdbCurrent && strings.TrimSpace(metadata.TVDB.Poster) != "") ||
			(tvmazeCurrent && (strings.TrimSpace(metadata.TVmaze.Poster) != "" || strings.TrimSpace(metadata.TVmaze.PosterMedium) != ""))
	case "title":
		if meta.MetadataOverrides.Title != nil {
			return explicitNonEmptyString(meta.MetadataOverrides.Title)
		}
		return strings.TrimSpace(meta.Release.Title) != "" ||
			(tmdbCurrent && strings.TrimSpace(metadata.TMDB.Title) != "") ||
			(imdbCurrent && strings.TrimSpace(metadata.IMDB.Title) != "") ||
			(tvdbCurrent && (strings.TrimSpace(metadata.TVDB.NameEnglish) != "" || strings.TrimSpace(metadata.TVDB.Name) != "")) ||
			(tvmazeCurrent && strings.TrimSpace(metadata.TVmaze.Name) != "")
	case "original_title":
		if meta.MetadataOverrides.OriginalTitle != nil {
			return explicitNonEmptyString(meta.MetadataOverrides.OriginalTitle)
		}
		return (tmdbCurrent && strings.TrimSpace(metadata.TMDB.OriginalTitle) != "") ||
			(tvdbCurrent && strings.TrimSpace(metadata.TVDB.Name) != "") || strings.TrimSpace(meta.Release.Title) != ""
	case "year":
		if meta.ReleaseNameOverrides.ManualYear != nil {
			return *meta.ReleaseNameOverrides.ManualYear > 0
		}
		return meta.Release.Year > 0 ||
			(tmdbCurrent && metadata.TMDB.Year > 0) || (imdbCurrent && metadata.IMDB.Year > 0) || (tvdbCurrent && metadata.TVDB.Year > 0)
	case "genres":
		if meta.MetadataOverrides.Genres != nil {
			return explicitNonEmptyList(meta.MetadataOverrides.Genres)
		}
		return (tmdbCurrent && strings.TrimSpace(metadata.TMDB.Genres) != "") ||
			(imdbCurrent && strings.TrimSpace(metadata.IMDB.Genres) != "") ||
			(tvdbCurrent && strings.TrimSpace(metadata.TVDB.Genres) != "") ||
			(tvmazeCurrent && strings.TrimSpace(metadata.TVmaze.Genres) != "")
	case "original_language":
		if meta.MetadataOverrides.OriginalLanguage != nil {
			return explicitNonEmptyString(meta.MetadataOverrides.OriginalLanguage)
		}
		return (tmdbCurrent && strings.TrimSpace(metadata.TMDB.OriginalLanguage) != "") ||
			(imdbCurrent && strings.TrimSpace(metadata.IMDB.OriginalLanguage) != "") ||
			(tvdbCurrent && strings.TrimSpace(metadata.TVDB.OriginalLanguage) != "") ||
			(tvmazeCurrent && strings.TrimSpace(metadata.TVmaze.Language) != "")
	case "tmdb_localized_pt_br":
		if !tmdbCurrent || metadata.TMDB.Localized == nil {
			return false
		}
		return localizedPTBRComplete(metadata.TMDB.Localized["pt-BR"], strings.EqualFold(string(identity.Category), "TV") && meta.SeasonInt > 0)
	}
	return false
}

func providerSuppliesMetadataRequirement(
	provider api.IdentityProvider,
	field api.MetadataRequirementField,
	meta preparationstate.State,
) bool {
	if metadataRequirementOverrideBlocksProvider(field, meta.MetadataOverrides) {
		return false
	}
	switch provider {
	case api.IdentityProviderTMDB:
		switch field {
		case "tmdb", "tmdb_title", "tmdb_origin_countries", "poster", "title", "original_title", "year", "genres", "original_language":
			return true
		}
	case api.IdentityProviderIMDB:
		switch field {
		case "imdb", "imdb_title", "poster", "title", "year", "genres", "original_language":
			return true
		}
	case api.IdentityProviderTVDB:
		switch field {
		case "tvdb", "tvdb_title", "tvdb_year", "tvdb_disambiguation", "poster", "title", "original_title", "year", "genres", "original_language":
			return true
		}
	case api.IdentityProviderTVmaze:
		switch field {
		case "tvmaze", "poster", "title", "genres", "original_language":
			return true
		}
	case api.IdentityProviderMAL:
		return false
	}
	return false
}

func metadataRequirementOverrideBlocksProvider(field api.MetadataRequirementField, overrides api.MetadataOverrides) bool {
	switch field {
	case "title":
		return overrides.Title != nil
	case "original_title":
		return overrides.OriginalTitle != nil
	case "genres":
		return overrides.Genres != nil
	case "original_language":
		return overrides.OriginalLanguage != nil
	}
	return false
}

func hasNonEmptyString(values []string) bool {
	return slices.ContainsFunc(values, func(value string) bool { return strings.TrimSpace(value) != "" })
}

func explicitNonEmptyString(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}

func explicitNonEmptyList(values *[]string) bool {
	return values != nil && hasNonEmptyString(*values)
}
