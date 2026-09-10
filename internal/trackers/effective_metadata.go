// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// PreferredTitle retains explicit manual authority while preserving a tracker's
// existing provider preference and legacy canonical fallback.
func PreferredTitle(meta api.UploadSubject, provider string) string {
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return meta.EffectiveMetadata.Title
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	if title := strings.TrimSpace(meta.EffectiveMetadata.Title); title != "" {
		return title
	}
	return strings.TrimSpace(meta.Release.Title)
}

// PreferredAlternateTitle retains explicit manual authority.
func PreferredAlternateTitle(meta api.UploadSubject, provider string) string {
	if meta.EffectiveMetadata.AlternateTitleProvenance.IsManual() {
		return meta.EffectiveMetadata.AlternateTitle
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	return strings.TrimSpace(meta.EffectiveMetadata.AlternateTitle)
}

// PreferredOriginalTitle retains explicit manual authority.
func PreferredOriginalTitle(meta api.UploadSubject, provider string) string {
	if meta.EffectiveMetadata.OriginalTitleProvenance.IsManual() {
		return meta.EffectiveMetadata.OriginalTitle
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	if title := strings.TrimSpace(meta.EffectiveMetadata.OriginalTitle); title != "" {
		return title
	}
	return strings.TrimSpace(meta.Release.Title)
}

// PreferredYear retains an explicit manual year before provider evidence.
func PreferredYear(meta api.UploadSubject, provider int) int {
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		return meta.EffectiveMetadata.Year
	}
	if provider > 0 {
		return provider
	}
	if meta.EffectiveMetadata.Year > 0 {
		return meta.EffectiveMetadata.Year
	}
	return meta.Release.Year
}

// PreferredGenreText retains explicit manual genres before provider evidence.
func PreferredGenreText(meta api.UploadSubject, provider string) string {
	if meta.EffectiveMetadata.GenresProvenance.IsManual() {
		return strings.Join(meta.EffectiveMetadata.Genres, ", ")
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	if len(meta.EffectiveMetadata.Genres) != 0 {
		return strings.Join(meta.EffectiveMetadata.Genres, ", ")
	}
	return strings.TrimSpace(meta.Release.Genre)
}

// PreferredOriginalLanguage retains an explicit manual language before provider evidence.
func PreferredOriginalLanguage(meta api.UploadSubject, provider string) string {
	if meta.EffectiveMetadata.OriginalLanguageProvenance.IsManual() {
		return meta.EffectiveMetadata.OriginalLanguage
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	return strings.TrimSpace(meta.EffectiveMetadata.OriginalLanguage)
}

// PreferredDistributor retains an explicit manual distributor before provider evidence.
func PreferredDistributor(meta api.UploadSubject, provider string) string {
	if meta.EffectiveMetadata.DistributorProvenance.IsManual() {
		return meta.EffectiveMetadata.Distributor
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	if value := strings.TrimSpace(meta.EffectiveMetadata.Distributor); value != "" {
		return value
	}
	return strings.TrimSpace(meta.Distributor)
}
