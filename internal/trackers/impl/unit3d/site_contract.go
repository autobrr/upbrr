// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"golang.org/x/text/language"

	"github.com/autobrr/upbrr/pkg/api"
)

// InferType returns the common normalized Unit3D release type.
func InferType(meta api.UploadSubject) string { return inferUnit3DType(meta) }

// Resolution returns the common normalized release resolution.
func Resolution(meta api.UploadSubject) string { return resolveResolution(meta) }

// RuleResolution returns the common normalized release resolution from the
// narrower rule-evaluation read model.
func RuleResolution(meta api.RuleSubject) string {
	return resolveResolutionValues(meta.Release, meta.ReleaseName)
}

// Category returns the common normalized Unit3D category.
func Category(meta api.UploadSubject) string { return resolveUnit3DCategory(meta) }

// DefaultCategoryID returns the common Unit3D category mapping.
func DefaultCategoryID(meta api.UploadSubject) string { return resolveUnit3DCategoryID(meta) }

// IsDiscType reports whether value is a supported disc marker.
func IsDiscType(value string) bool { return isDiscType(value) }

// IsSDResolution reports whether value is a standard-definition resolution.
func IsSDResolution(value string) bool { return isSDResolution(value) }

// Keywords returns common TMDB keyword metadata.
func Keywords(meta api.UploadSubject) string { return resolveKeywords(meta) }

// TMDBGenres returns common TMDB genre metadata.
func TMDBGenres(meta api.UploadSubject) string { return resolveTMDBGenres(meta) }

// IMDBGenres returns common IMDb genre metadata.
func IMDBGenres(meta api.UploadSubject) string { return resolveIMDBGenres(meta) }

// HasEnglishLanguage reports whether languages contains an English marker.
func HasEnglishLanguage(languages []string) bool { return hasEnglishLanguage(languages) }

// HasAdultToken reports whether value contains a common adult-content marker.
func HasAdultToken(value string) bool { return hasAdultToken(value) }

// ParseLanguageTag resolves common language names and codes.
func ParseLanguageTag(value string) (language.Tag, bool) { return parseLanguageTag(value) }

// IsNoGroupTag reports whether tag is a no-group placeholder.
func IsNoGroupTag(tag string) bool { return isNoGroupTag(tag) }
