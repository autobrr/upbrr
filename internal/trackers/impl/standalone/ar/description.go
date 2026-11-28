// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var centerTagReplacer = strings.NewReplacer("[center]", "[align=center]", "[/center]", "[/align]")

func buildDescription(defaults config.DescriptionSettingsConfig, meta api.UploadSubject, dbPath string, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return description.MapOutsideLiteralBlocks(strings.TrimSpace(assets.Description), centerTagReplacer.Replace)
	}

	var mediaText string
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		mediaText, _ = readBDSummary(meta, dbPath)
	case "DVD":
		mediaText = metautil.FirstNonEmptyTrimmed(trackers.ReadDVDVOBMediaInfo(meta), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
	default:
		mediaText = readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath))
	}
	return composeDescription(defaults, meta, assets, mediaText)
}

func composeDescription(defaults config.DescriptionSettingsConfig, meta api.UploadSubject, assets trackers.DescriptionAssets, mediaText string) string {
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	baseDescription, discReports := description.SplitLeadingDiscReports(assets.Description)
	baseDescription, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(baseDescription)
	audioAnalysis = description.RelabelAnalysis(audioAnalysis, defaults.AudioPlotsLabel, defaults.HDRPlotsLabel)

	var parts []description.Section
	appendPart := func(id, text string) { parts = description.AppendSection(parts, id, text) }
	heading := func(key, text string) string {
		return "[color=red][size=4]" + description.Heading(defaults.Labels, "ar."+key, text) + "[/size][/color]\n"
	}
	title := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.ReleaseName), strings.TrimSpace(meta.Release.Title), pathutil.Base(meta.SourcePath))
	appendPart("", fmt.Sprintf("[color=green][size=6]%s[/size][/color]", title))

	if links := buildDatabaseLinks(meta); links != "" {
		appendPart("links", heading("links", "Links")+links)
	}

	if strings.TrimSpace(mediaText) != "" {
		reportKey, reportLabel := "mediainfo", "MEDIAINFO"
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
			reportKey, reportLabel = "bdinfo", "BDINFO"
		}
		appendPart("mediainfo", heading(reportKey, reportLabel)+"[hide][code]"+strings.TrimSpace(mediaText)+"[/code][/hide]")
	}

	if overview := resolveOverview(meta); overview != "" {
		appendPart("plot", heading("plot", "PLOT")+overview)
	}
	if genres := resolveGenres(meta); genres != "" {
		appendPart("genres", heading("genres", "Genres")+genres)
	}
	if youtube := resolveYouTube(meta); youtube != "" {
		appendPart("trailer", heading("trailer", "Youtube")+youtube)
	}
	if notes := cleanNotes(baseDescription); notes != "" {
		appendPart("kept", heading("notes", "Notes")+notes)
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", audioAnalysis)
	}
	if discReports != "" {
		appendPart("screenshots", discReports)
	}
	if screenshots := buildScreenshotSection(assets.Screenshots); screenshots != "" {
		appendPart("screenshots", heading("screenshots", "Screenshots")+screenshots)
	}

	return description.MapOutsideLiteralBlocks(strings.TrimSpace(description.FormatSections(parts, defaults)), func(fragment string) string {
		return centerTagReplacer.Replace(bbcode.ConvertSpoilerToHide(bbcode.ConvertNamedSpoilerToNamedHide(fragment)))
	})
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

func descriptionDefaultsLayout() []trackers.DescriptionLayoutItem {
	makeItem := func(id, label, key, defaultHeading string) trackers.DescriptionLayoutItem {
		return trackers.DescriptionLayoutItem{
			ID:    id,
			Label: label,
			Labels: []trackers.DescriptionLabel{{
				Key:     "ar." + key,
				Label:   label + " heading",
				Default: defaultHeading,
			}},
		}
	}
	return []trackers.DescriptionLayoutItem{
		{Label: "Required release title"},
		makeItem("links", "Provider links", "links", "Links"),
		{
			ID:         "mediainfo",
			Label:      "MediaInfo or BDInfo",
			CanCompact: true,
			Labels: []trackers.DescriptionLabel{
				{
					Key:     "ar.mediainfo",
					Label:   "MediaInfo heading",
					Default: "MEDIAINFO",
				},
				{
					Key:     "ar.bdinfo",
					Label:   "BDInfo heading",
					Default: "BDINFO",
				},
			},
		},
		makeItem("plot", "Plot", "plot", "PLOT"),
		makeItem("genres", "Genres", "genres", "Genres"),
		makeItem("trailer", "Trailer", "trailer", "Youtube"),
		makeItem("kept", "Notes", "notes", "Notes"),
		{
			ID:         "audio_analysis",
			Label:      "Audio and HDR analysis",
			CanCompact: true,
			Fields:     []string{"AudioPlotsLabel", "HDRPlotsLabel"},
		},
		makeItem("screenshots", "Screenshots", "screenshots", "Screenshots"),
	}
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	_ config.TrackerConfig,
) (string, error) {
	return composeDescription(defaults, sample.Subject, sample.Assets, sample.Report), nil
}
