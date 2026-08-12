// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ldu

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return clean(name)
	}
	nonEnglishOriginal := !isEnglish(originalLanguage(meta))
	audio, nonEnglishAudio := firstAudio(meta.AudioLanguages)
	subtitle := firstSubtitle(meta.SubtitleLanguages)
	if categoryID(meta) == "18" && subtitle != "" {
		return clean(name + " [Subs " + subtitle + "]")
	}
	if !nonEnglishOriginal && !nonEnglishAudio {
		return clean(name)
	}
	parts := make([]string, 0, 2)
	if audio != "" {
		parts = append(parts, "["+audio+"]")
	}
	if subtitle != "" {
		parts = append(parts, "[Subs "+subtitle+"]")
	}
	if len(parts) == 0 {
		return clean(name)
	}
	return clean(name + " " + strings.Join(parts, " "))
}

func clean(value string) string { return strings.TrimSpace(strings.Join(strings.Fields(value), " ")) }

func firstAudio(values []string) (string, bool) {
	for _, value := range values {
		if code, english, ok := languageCode(value); ok {
			return code, !english
		}
	}
	return "", false
}

func firstSubtitle(values []string) string {
	for _, value := range values {
		if code, _, ok := languageCode(value); ok {
			return code
		}
	}
	return ""
}

func languageCode(value string) (string, bool, bool) {
	tag, ok := unit3d.ParseLanguageTag(value)
	if !ok {
		return "", false, false
	}
	base, _ := tag.Base()
	if base.String() == "und" {
		return "", false, false
	}
	code := base.ISO3()
	if code == "" {
		return "", false, false
	}
	return strings.ToUpper(code), base.String() == "en", true
}

func originalLanguage(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	}
	return ""
}

func isEnglish(value string) bool {
	tag, ok := unit3d.ParseLanguageTag(value)
	if !ok {
		return false
	}
	base, _ := tag.Base()
	return base.String() == "en"
}
