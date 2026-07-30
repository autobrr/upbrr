// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(assets trackers.DescriptionAssets) string {
	return strings.TrimSpace(assets.Description)
}

func screenshots(images []api.ScreenshotImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if raw := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL)); raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return providerid.IMDb(meta.Identity.IMDBID).URL() + "/"
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Poster)
}

func genresText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
	}
	return metautil.FirstNonEmptyTrimmed(meta.Release.Genre)
}

func keywordsText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
}
