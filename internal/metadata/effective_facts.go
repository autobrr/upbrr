// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func applyMetadataNamingOverrides(request api.ReleaseNameRequest, overrides api.MetadataOverrides) api.ReleaseNameRequest {
	if overrides.Title != nil {
		request.Title = strings.TrimSpace(*overrides.Title)
	}
	if overrides.AlternateTitle != nil {
		request.AltTitle = strings.TrimSpace(*overrides.AlternateTitle)
	}
	return request
}

func effectiveMetadata(meta preparationstate.State, request api.ReleaseNameRequest) api.EffectiveMetadata {
	metadata := api.EffectiveMetadata{
		Title:                      request.Title,
		AlternateTitle:             request.AltTitle,
		OriginalTitle:              automaticOriginalTitle(meta),
		Year:                       request.Year,
		Genres:                     splitGenres(resolvedGenre(meta)),
		OriginalLanguage:           automaticOriginalLanguage(meta),
		Distributor:                meta.Distributor,
		TitleProvenance:            api.FactProvenanceAutomatic,
		AlternateTitleProvenance:   api.FactProvenanceAutomatic,
		OriginalTitleProvenance:    api.FactProvenanceAutomatic,
		YearProvenance:             api.FactProvenanceAutomatic,
		GenresProvenance:           api.FactProvenanceAutomatic,
		OriginalLanguageProvenance: api.FactProvenanceAutomatic,
		DistributorProvenance:      api.FactProvenanceAutomatic,
	}
	if value := meta.ReleaseNameOverrides.ManualYear; value != nil {
		metadata.Year = *value
		if *value == 0 {
			metadata.YearProvenance = api.FactProvenanceManualEmpty
		} else {
			metadata.YearProvenance = api.FactProvenanceManual
		}
	}
	if value := meta.MetadataOverrides.Title; value != nil {
		metadata.Title = strings.TrimSpace(*value)
		metadata.TitleProvenance = factProvenanceForString(metadata.Title)
	}
	if value := meta.MetadataOverrides.AlternateTitle; value != nil {
		metadata.AlternateTitle = strings.TrimSpace(*value)
		metadata.AlternateTitleProvenance = factProvenanceForString(metadata.AlternateTitle)
	}
	if value := meta.MetadataOverrides.OriginalTitle; value != nil {
		metadata.OriginalTitle = strings.TrimSpace(*value)
		metadata.OriginalTitleProvenance = factProvenanceForString(metadata.OriginalTitle)
	}
	if value := meta.MetadataOverrides.Genres; value != nil {
		metadata.Genres = normalizeGenres(*value)
		metadata.GenresProvenance = factProvenanceForList(metadata.Genres)
	}
	if value := meta.MetadataOverrides.OriginalLanguage; value != nil {
		metadata.OriginalLanguage = strings.TrimSpace(*value)
		if normalized := languageutil.NormalizeLanguageList([]string{metadata.OriginalLanguage}); len(normalized) == 1 {
			metadata.OriginalLanguage = normalized[0]
		}
		metadata.OriginalLanguageProvenance = factProvenanceForString(metadata.OriginalLanguage)
	}
	if value := meta.MetadataOverrides.Distributor; value != nil {
		metadata.Distributor = normalizeDistributor(*value)
		metadata.DistributorProvenance = factProvenanceForString(metadata.Distributor)
	}
	return metadata
}

func automaticOriginalTitle(meta preparationstate.State) string {
	if matchingTMDBMetadataForNaming(meta) && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle)
	}
	if matchingTVDBMetadataForNaming(meta) && strings.TrimSpace(meta.ProviderMetadata.TVDB.Name) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Name)
	}
	return strings.TrimSpace(meta.Release.Title)
}

func automaticOriginalLanguage(meta preparationstate.State) string {
	value := originalAudioLanguage(preparationstate.State{ProviderMetadata: meta.ProviderMetadata})
	if normalized := languageutil.NormalizeLanguageList([]string{value}); len(normalized) == 1 {
		return normalized[0]
	}
	return strings.TrimSpace(value)
}

func splitGenres(value string) []string { return normalizeGenres(strings.Split(value, ",")) }

func normalizeGenres(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func factProvenanceForString(value string) api.FactProvenance {
	if strings.TrimSpace(value) == "" {
		return api.FactProvenanceManualEmpty
	}
	return api.FactProvenanceManual
}
