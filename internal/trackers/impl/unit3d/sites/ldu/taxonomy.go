// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ldu

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

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
