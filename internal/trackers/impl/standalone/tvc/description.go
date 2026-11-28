// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, cfg config.TrackerConfig, defaults config.DescriptionSettingsConfig, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	baseDescription, discReports := description.SplitLeadingDiscReports(assets.Description)
	baseDescription, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(baseDescription)
	audioAnalysis = description.RelabelAnalysis(audioAnalysis, defaults.AudioPlotsLabel, defaults.HDRPlotsLabel)
	assets.Description = baseDescription
	var parts []description.Section
	appendPart := func(id, text string) { parts = description.AppendSection(parts, id, text) }
	if defaults.AddLogo {
		if logo := strings.TrimSpace(meta.ProviderMetadata.TMDB.Logo); logo != "" {
			appendPart("logo", fmt.Sprintf("[center][img=%d]%s[/img][/center]", maxInt(cfg.ImageCount, 300), logo))
		}
	}
	if title := strings.TrimSpace(meta.EpisodeTitle); title != "" {
		appendPart("episode_overview", "[center][b]"+description.Heading(defaults.Labels, "tvc.episode", "Episode Title")+":[/b] "+title+"[/center]")
	}
	if overview := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.EpisodeOverview, meta.ProviderMetadata.TMDB.Overview)); overview != "" {
		appendPart("episode_overview", "[center]"+overview+"[/center]")
	}
	if links := externalLinks(meta); links != "" {
		appendPart("links", "[center]"+links+"[/center]")
	}
	if base := strings.TrimSpace(assets.Description); base != "" {
		appendPart("kept", "[center][b]"+description.Heading(defaults.Labels, "tvc.notes", "Notes / Extra Info")+"[/b]\n"+base+"[/center]")
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", strings.ReplaceAll(audioAnalysis, "[img]", "[img=350]"))
	}
	if discReports != "" {
		appendPart("screenshots", discReports)
	}
	if shots := screenshotBlock(assets.Screenshots, maxInt(cfg.ImageCount, 2), defaults.ScreenshotLabel); shots != "" {
		appendPart("screenshots", "[center]"+shots+"[/center]")
	}
	return finalizeDescription(strings.TrimSpace(description.FormatSections(parts, defaults)))
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

func screenshotBlock(images []api.ScreenshotImage, count int, label string) string {
	if len(images) < count {
		return ""
	}
	label = description.MarkupLabel(label)
	if label == "" {
		label = "Screenshots"
	}
	parts := []string{"[b]" + label + "[/b]"}
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
	provider := ""
	if meta.ProviderMetadata.TMDB != nil {
		provider = meta.ProviderMetadata.TMDB.Genres
	}
	return trackers.PreferredGenreText(meta, provider)
}

func keywordsText(meta api.UploadSubject) string {
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
}

func descriptionDefaultsLayout() []trackers.DescriptionLayoutItem {
	return []trackers.DescriptionLayoutItem{
		{
			ID:         "logo",
			Label:      "Title logo",
			CanCompact: true,
			Fields:     []string{"AddLogo"},
		},
		{
			ID:         "episode_overview",
			Label:      "Episode overview",
			CanCompact: true,
			Labels: []trackers.DescriptionLabel{{
				Key:     "tvc.episode",
				Label:   "Episode title heading",
				Default: "Episode Title",
			}},
		},
		{
			ID:         "links",
			Label:      "Provider links",
			CanCompact: true,
		},
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: true,
			Labels: []trackers.DescriptionLabel{{
				Key:     "tvc.notes",
				Label:   "Notes heading",
				Default: "Notes / Extra Info",
			}},
		},
		{
			ID:         "audio_analysis",
			Label:      "Audio and HDR analysis",
			CanCompact: true,
			Fields:     []string{"AudioPlotsLabel", "HDRPlotsLabel"},
		},
		{
			ID:         "screenshots",
			Label:      "Screenshots",
			CanCompact: true,
			Fields:     []string{"ScreenshotLabel"},
		},
	}
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	site config.TrackerConfig,
) (string, error) {
	return buildDescription(sample.Subject, site, defaults, sample.Assets), nil
}
