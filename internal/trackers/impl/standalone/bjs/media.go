// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"math"
	"strings"

	metamediainfo "github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveRuntime(meta api.UploadSubject) int {
	for _, candidate := range []string{
		commonhttp.ReadOptionalFile(meta.MediaInfoTextPath),
		strings.TrimSpace(meta.DVDVOBMediaInfoText),
	} {
		if minutes := parseMediaInfoDurationMinutes(candidate); minutes > 0 {
			return minutes
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if meta.Disc.DurationSeconds > 0 {
			return int(math.Round(meta.Disc.DurationSeconds / 60))
		}
	}
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.RuntimeMinutes > 0 {
		return meta.ProviderMetadata.IMDB.RuntimeMinutes
	}
	if meta.ProviderMetadata.TMDB != nil {
		return meta.ProviderMetadata.TMDB.Runtime
	}
	if meta.ProviderMetadata.TVmaze != nil {
		return meta.ProviderMetadata.TVmaze.Runtime
	}
	return 0
}

// parseMediaInfoDurationMinutes returns rounded minutes from the first parseable
// MediaInfo Duration or Duration/String[1-3] line, including ISO-8601-like values.
func parseMediaInfoDurationMinutes(text string) int {
	return metamediainfo.DurationMinutes(text)
}
