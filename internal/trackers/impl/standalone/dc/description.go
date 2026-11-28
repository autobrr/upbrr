// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/description"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	finalDescription := composeDescription(req, assets, resolveMedia(req, req.Meta))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "DC", req.Runtime.DBPath, finalDescription, req.Logger)
	}

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

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		appendPart("custom_header", header)
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information
	if media != "" {
		appendPart("mediainfo", strings.TrimSpace(media))
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		appendPart("kept", strings.TrimSpace(assets.Description))
	}

	if menus := screenshotBlock(assets.MenuImages); menus != "" {
		appendPart("disc_menu", menus)
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", strings.ReplaceAll(audioAnalysis, "[img]", "[img=350]"))
	}
	if discReports != "" {
		appendPart("screenshots", discReports)
	}
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		appendPart("screenshots", shots)
	}

	// Tonemapped Header
	if tonemapHeader := descriptionunit3d.TonemappingNotice(req.Runtime.Description.TonemappedHeader, assets.Screenshots); tonemapHeader != "" {
		appendPart("screenshots", tonemapHeader)
	}

	// Signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	appendPart("", fmt.Sprintf("[center][url=%s]%s[/url][/center]", link, text))

	// Join and finalize
	return finalizeDescription(description.FormatSections(parts, req.Runtime.Description))
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		webURL := strings.TrimSpace(image.WebURL)
		rawURL := strings.TrimSpace(image.RawURL)
		imgURL := strings.TrimSpace(image.ImgURL)
		if imgURL == "" {
			imgURL = rawURL
		}
		if webURL == "" {
			webURL = rawURL
		}
		if webURL == "" || imgURL == "" {
			continue
		}
		parts = append(parts, "[url="+webURL+"][img=350]"+imgURL+"[/img][/url]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[center]" + strings.Join(parts, " ") + "[/center]"
}

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)
	return trackers.DescriptionResult{Group: "dc", Description: description}, nil
}

func descriptionDefaultsLayout() []trackers.DescriptionLayoutItem {
	return []trackers.DescriptionLayoutItem{
		{
			ID:         "custom_header",
			Label:      "Introduction",
			CanCompact: true,
			Fields:     []string{"CustomDescriptionHeader"},
		},
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
			Fields:     []string{"TonemappedHeader"},
		},
		{Label: "Signature footer"},
	}
}
