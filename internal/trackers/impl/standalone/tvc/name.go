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
	var name string
	switch {
	case !isTV(meta):
		name = fmt.Sprintf(
			"%s (%d) [%s %s %s]",
			metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName),
			maxInt(meta.Release.Year, meta.ProviderMetadata.TMDB.Year),
			meta.Release.Resolution,
			typeName,
			videoSuffix(meta.VideoCodec),
		)
	case meta.TVPack:
		name = fmt.Sprintf(
			"%s - Series %d (%d) [%s %s %s]",
			metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName),
			maxInt(meta.SeasonInt, 1),
			maxInt(meta.Release.Year, meta.ProviderMetadata.TMDB.Year),
			meta.Release.Resolution,
			typeName,
			videoSuffix(meta.VideoCodec),
		)
	default:
		name = fmt.Sprintf(
			"%s S%02dE%02d [%s %s %s]",
			metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName),
			maxInt(meta.SeasonInt, 1),
			maxInt(meta.EpisodeInt, 1),
			meta.Release.Resolution,
			typeName,
			videoSuffix(meta.VideoCodec),
		)
	}
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
