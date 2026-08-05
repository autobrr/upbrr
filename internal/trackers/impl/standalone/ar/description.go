// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, dbPath string, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}

	var parts []string
	title := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ReleaseName), strings.TrimSpace(meta.Release.Title), pathutil.Base(meta.SourcePath))
	parts = append(parts, fmt.Sprintf("[color=green][size=6]%s[/size][/color]", title))

	if links := buildDatabaseLinks(meta); links != "" {
		parts = append(parts, "[color=red][size=4]Links[/size][/color]\n"+links)
	}

	mediaLabel := "MEDIAINFO"
	var mediaText string
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		mediaLabel = "BDINFO"
		mediaText, _ = readBDSummary(meta, dbPath)
	case "DVD":
		mediaText = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
	default:
		mediaText = readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath))
	}
	if strings.TrimSpace(mediaText) != "" {
		parts = append(parts, fmt.Sprintf("[color=red][size=4]%s[/size][/color]\n[hide][code]%s[/code][/hide]", mediaLabel, strings.TrimSpace(mediaText)))
	}

	if overview := resolveOverview(meta); overview != "" {
		parts = append(parts, "[color=red][size=4]PLOT[/size][/color]\n"+overview)
	}
	if genres := resolveGenres(meta); genres != "" {
		parts = append(parts, "[color=red][size=4]Genres[/size][/color]\n"+genres)
	}
	if screenshots := buildScreenshotSection(assets.Screenshots); screenshots != "" {
		parts = append(parts, "[color=red][size=4]Screenshots[/size][/color]\n"+screenshots)
	}
	if youtube := resolveYouTube(meta); youtube != "" {
		parts = append(parts, "[color=red][size=4]Youtube[/size][/color]\n"+youtube)
	}
	if notes := cleanNotes(assets.Description); notes != "" {
		parts = append(parts, "[color=red][size=4]Notes[/size][/color]\n"+notes)
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildScreenshotSection(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.RawURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		parts = append(parts, "[url="+image.RawURL+"][img]"+image.ImgURL+"[/img][/url]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[align=center]" + strings.Join(parts, "") + "[/align]"
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
	}
	if meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	}
	if meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster)
	}
	if meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster)
	}
	return ""
}

func resolveOverview(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Overview) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Overview)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}
