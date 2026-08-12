// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, cfg config.TrackerConfig, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	parts := make([]string, 0, 6)
	if logo := strings.TrimSpace(meta.ProviderMetadata.TMDB.Logo); logo != "" {
		parts = append(parts, fmt.Sprintf("[center][img=%d]%s[/img][/center]", maxInt(cfg.ImageCount, 300), logo))
	}
	if title := strings.TrimSpace(meta.EpisodeTitle); title != "" {
		parts = append(parts, "[center][b]Episode Title:[/b] "+title+"[/center]")
	}
	if overview := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.EpisodeOverview, meta.ProviderMetadata.TMDB.Overview)); overview != "" {
		parts = append(parts, "[center]"+overview+"[/center]")
	}
	if links := externalLinks(meta); links != "" {
		parts = append(parts, "[center]"+links+"[/center]")
	}
	if shots := screenshotBlock(assets.Screenshots, maxInt(cfg.ImageCount, 2)); shots != "" {
		parts = append(parts, "[center]"+shots+"[/center]")
	}
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, "[center][b]Notes / Extra Info[/b]\n"+base+"[/center]")
	}
	return finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))
}

func externalLinks(meta api.UploadSubject) string {
	parts := make([]string, 0, 3)
	if category := categoryName(meta); meta.Identity.TMDBID > 0 && category != "" {
		parts = append(parts, fmt.Sprintf("[url=https://www.themoviedb.org/%s/%d]TMDB[/url]", strings.ToLower(category), meta.Identity.TMDBID))
	}
	if meta.Identity.IMDBID > 0 {
		parts = append(parts, fmt.Sprintf("[url=%s]IMDb[/url]", providerid.IMDb(meta.Identity.IMDBID).URL()))
	}
	if isTVCategory(meta) && meta.Identity.TVDBID > 0 {
		parts = append(parts, fmt.Sprintf("[url=https://www.thetvdb.com/?id=%d&tab=series]TVDB[/url]", meta.Identity.TVDBID))
	}
	return strings.Join(parts, " | ")
}

func screenshotBlock(images []api.ScreenshotImage, count int) string {
	if len(images) < count {
		return ""
	}
	parts := []string{"[b]Screenshots[/b]"}
	for _, image := range images[:count] {
		web := metautil.FirstNonEmptyTrimmed(image.WebURL, image.ImgURL, image.RawURL)
		img := metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL)
		if web == "" || img == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[url=%s][img=350]%s[/img][/url]", web, img))
	}
	return strings.Join(parts, " ")
}

func genresText(meta api.UploadSubject) string {
	return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
}

func keywordsText(meta api.UploadSubject) string {
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
}
