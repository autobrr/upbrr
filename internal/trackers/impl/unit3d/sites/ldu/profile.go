// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ldu

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns LDU's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "LDU",
		BaseURL: "https://theldu.to",
		Site: unit3d.SiteProfile{
			BuildName:         buildName,
			ResolveCategoryID: categoryID,
		},
	}
}

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
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

func categoryID(meta api.UploadSubject) string {
	category := unit3d.Category(meta)
	genres := strings.ToLower(
		strings.TrimSpace(
			strings.Join([]string{strings.TrimSpace(meta.Release.Genre), unit3d.Keywords(meta), unit3d.TMDBGenres(meta), unit3d.IMDBGenres(meta)}, ","),
		),
	)
	hasEnglishAudio := unit3d.HasEnglishLanguage(meta.AudioLanguages)
	hasEnglishSubs := unit3d.HasEnglishLanguage(meta.SubtitleLanguages)
	containsDubbed := strings.Contains(strings.ToLower(strings.TrimSpace(meta.Audio)), "dubbed")
	edition := strings.ToLower(strings.TrimSpace(meta.Edition))

	if strings.EqualFold(category, "MOVIE") {
		switch {
		case meta.Anime || meta.Identity.MALID != 0:
			return "8"
		case strings.Contains(edition, "fanedit") || strings.Contains(edition, "fanres"):
			return "12"
		case strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D"):
			return "21"
		case unit3d.HasAdultToken(genres) && !hasEnglishAudio && !hasEnglishSubs:
			return "45"
		case unit3d.HasAdultToken(genres):
			return "6"
		case strings.Contains(genres, "documentary"):
			return "17"
		case strings.Contains(genres, "musical"):
			return "25"
		case !hasEnglishAudio && !hasEnglishSubs:
			return "22"
		case containsDubbed:
			return "27"
		default:
			return "1"
		}
	}
	if strings.EqualFold(category, "TV") {
		switch {
		case meta.Anime || meta.Identity.MALID != 0:
			return "9"
		case strings.Contains(genres, "documentary"):
			return "40"
		case !hasEnglishAudio && !hasEnglishSubs:
			return "29"
		case meta.TVPack:
			return "2"
		case containsDubbed:
			return "31"
		default:
			return "41"
		}
	}
	return unit3d.DefaultCategoryID(meta)
}
