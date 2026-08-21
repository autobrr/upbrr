// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveName(meta api.UploadSubject) string {
	typeName := strings.ReplaceAll(meta.Type, "WEBDL", "WEB-DL")
	title := metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)
	name := title
	year := meta.Release.Year
	if meta.ProviderMetadata.TMDB != nil {
		year = maxInt(year, meta.ProviderMetadata.TMDB.Year)
	}
	switch {
	case !isTV(meta):
		if year > 0 {
			name += fmt.Sprintf(" (%d)", year)
		}
	case meta.TVPack:
		if meta.SeasonInt > 0 {
			name += fmt.Sprintf(" - Series %d", meta.SeasonInt)
		}
		if year > 0 {
			name += fmt.Sprintf(" (%d)", year)
		}
	default:
		switch {
		case strings.TrimSpace(meta.DailyEpisodeDate) != "":
			name += " " + strings.TrimSpace(meta.DailyEpisodeDate)
		case meta.SeasonInt > 0 && meta.EpisodeInt > 0:
			name += fmt.Sprintf(" S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
		case meta.SeasonInt > 0:
			name += fmt.Sprintf(" S%02d", meta.SeasonInt)
		case meta.EpisodeInt > 0:
			name += fmt.Sprintf(" E%02d", meta.EpisodeInt)
		}
	}
	name += fmt.Sprintf(" [%s %s %s]", meta.Release.Resolution, typeName, videoSuffix(meta.VideoCodec))
	if strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") {
		name = strings.Replace(name, "]", " HEVC]", 1)
	}
	return appendCountryCode(meta, name)
}

func appendCountryCode(meta api.UploadSubject, name string) string {
	mapping := map[string]string{
		"AT": "AUT",
		"AU": "AUS",
		"BE": "BEL",
		"CA": "CAN",
		"CH": "CHE",
		"CZ": "CZE",
		"DE": "GER",
		"DK": "DNK",
		"EE": "EST",
		"ES": "SPA",
		"FI": "FIN",
		"FR": "FRA",
		"IE": "IRL",
		"IS": "ISL",
		"IT": "ITA",
		"NL": "NLD",
		"NO": "NOR",
		"NZ": "NZL",
		"PL": "POL",
		"PT": "POR",
		"RU": "RUS",
		"SE": "SWE",
	}
	if meta.ProviderMetadata.TMDB == nil {
		return name
	}
	for _, code := range meta.ProviderMetadata.TMDB.OriginCountry {
		if mapped := mapping[strings.ToUpper(strings.TrimSpace(code))]; mapped != "" {
			return name + " [" + mapped + "]"
		}
	}
	return name
}

func videoSuffix(codec string) string {
	value := strings.ToUpper(strings.TrimSpace(codec))
	if len(value) <= 3 {
		return value
	}
	return value[len(value)-3:]
}
