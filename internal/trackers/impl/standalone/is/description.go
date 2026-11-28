// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	finalDescription := composeDescription(req, assets, trackers.ReadMediaInfoForDescription(req.Runtime.DBPath, req.Meta))

	return finalDescription
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	_ config.TrackerConfig,
) (string, error) {
	return composeDescription(trackers.PreparationInput{
		Meta:    sample.Subject,
		Runtime: trackers.PreparationRuntime{Description: defaults},
	}, sample.Assets, sample.Report), nil
}

func composeDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets, media string) string {
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	baseDescription, discReports := description.SplitLeadingDiscReports(assets.Description)
	baseDescription, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(baseDescription)
	audioAnalysis = description.RelabelAnalysis(audioAnalysis, req.Runtime.Description.AudioPlotsLabel, req.Runtime.Description.HDRPlotsLabel)
	assets.Description = baseDescription
	meta := req.Meta
	var parts []description.Section
	appendPart := func(id, text string) {
		parts = description.AppendSection(parts, id, text)
	}
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		appendPart("episode_overview", "Title: "+strings.TrimSpace(meta.EpisodeTitle))
		appendPart("episode_overview", "Overview: "+strings.TrimSpace(meta.EpisodeOverview))
	}
	if media != "" {
		appendPart("mediainfo", media)
	}
	if strings.TrimSpace(assets.Description) != "" {
		appendPart("kept", strings.TrimSpace(assets.Description))
	}
	if len(assets.MenuImages) > 0 {
		var menuLines []string
		if header := strings.TrimSpace(req.Runtime.Description.DiscMenuHeader); header != "" {
			menuLines = append(menuLines, header)
		}
		for _, image := range assets.MenuImages {
			if strings.TrimSpace(image.RawURL) != "" {
				menuLines = append(menuLines, image.RawURL)
			}
		}
		if len(menuLines) > 0 {
			appendPart("disc_menu", strings.Join(menuLines, "\n"))
		}
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", audioAnalysis)
	}
	if discReports != "" {
		appendPart("screenshots", discReports)
	}
	if len(assets.Screenshots) > 0 {
		var shotLines []string
		for _, image := range assets.Screenshots {
			if strings.TrimSpace(image.RawURL) != "" {
				shotLines = append(shotLines, image.RawURL)
			}
		}
		if len(shotLines) > 0 {
			label := description.MarkupLabel(req.Runtime.Description.ScreenshotLabel)
			if label == "" {
				label = "Screenshots"
			}
			appendPart("screenshots", label+":\n"+strings.Join(shotLines, "\n"))
		}
	}
	return finalizeDescription(strings.TrimSpace(description.FormatSections(parts, req.Runtime.Description)))
}

func resolvePoster(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
	case meta.ProviderMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	case meta.ProviderMetadata.TVmaze != nil:
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster)
	default:
		return ""
	}
}

func resolveOverview(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
	case meta.ProviderMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot)
	case meta.ProviderMetadata.TVmaze != nil:
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}

func descriptionDefaultsLayout() []trackers.DescriptionLayoutItem {
	return []trackers.DescriptionLayoutItem{
		{
			ID:         "episode_overview",
			Label:      "Episode overview",
			CanCompact: true,
		},
		{
			ID:         "mediainfo",
			Label:      "MediaInfo or BDInfo",
			CanCompact: true,
		},
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: true,
		},
		{
			ID:         "disc_menu",
			Label:      "Disc menus",
			CanCompact: true,
			Fields:     []string{"DiscMenuHeader"},
		},
		{
			ID:         "audio_analysis",
			Label:      "Audio and HDR analysis",
			CanCompact: true,
			Fields:     []string{"AudioPlotsLabel", "HDRPlotsLabel"},
		},
		{
			ID:         "screenshots",
			Label:      "Screenshots and disc reports",
			CanCompact: true,
			Fields:     []string{"ScreenshotLabel"},
		},
	}
}
