// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	parts := []string{
		"[quote=Info]",
		"Name: " + strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)),
		"",
		"Overview: " + strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.EpisodeOverview, tmdbOverview(meta))),
		"",
		metautil.FirstNonEmptyTrimmed(meta.Release.Resolution, meta.Release.Source) + " / " + strings.TrimSpace(meta.Type),
		"",
		"Category: " + categoryName(meta),
	}
	if tmdb := meta.Identity.TMDBID; tmdb > 0 {
		parts = append(parts, fmt.Sprintf("TMDB: https://www.themoviedb.org/%s/%d", strings.ToLower(categoryName(meta)), tmdb))
	}
	if imdb := imdbURL(meta); imdb != "" {
		parts = append(parts, "IMDb: "+imdb)
	}
	parts = append(parts, "[/quote]")
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, base)
	}
	for _, image := range assets.Screenshots {
		raw := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL))
		if raw != "" {
			parts = append(parts, "[img]"+raw+"[/img]")
		}
	}
	parts = append(parts, `[size=2][url=https://www.torrenthr.org/forums.php?action=viewtopic&topicid=8977]upbrr[/url][/size]`)
	return finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n")))
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return providerid.IMDb(meta.Identity.IMDBID).URL() + "/"
}

func genresText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
	}
	return strings.TrimSpace(meta.Release.Genre)
}

func keywordsText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
}

func youtubeURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
}

func tmdbOverview(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
}
