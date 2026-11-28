// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

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
	finalDescription := composeDescription(req, assets, trackers.ReadMediaInfoForDescription(req.Runtime.DBPath, req.Meta))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "HDT", req.Runtime.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	settings config.DescriptionSettingsConfig,
	tracker config.TrackerConfig,
) (string, error) {
	return composeDescription(trackers.PreparationInput{
		Meta:          sample.Subject,
		Runtime:       trackers.PreparationRuntime{Description: settings},
		TrackerConfig: tracker,
	}, sample.Assets, sample.Report), nil
}

func composeDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets, media string) string {
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	baseDescription, discReports := description.SplitLeadingDiscReports(assets.Description)
	baseDescription, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(baseDescription)
	audioAnalysis = description.RelabelAnalysis(audioAnalysis, req.Runtime.Description.AudioPlotsLabel, req.Runtime.Description.HDRPlotsLabel)
	assets.Description = baseDescription
	meta := req.Meta
	parts := make([]description.Section, 0, 12)
	appendPart := func(id, text string) {
		parts = description.AppendSection(parts, id, text)
	}

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		appendPart("custom_header", header)
	}

	// Logo
	if req.Runtime.Description.AddLogo {
		if logo := resolveLogo(meta); logo != "" {
			appendPart("logo", "[center][img]"+logo+"[/img][/center]")
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media != "" {
		appendPart("mediainfo", "[pre]"+media+"[/pre]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		appendPart("kept", strings.TrimSpace(assets.Description))
	}

	// menu
	if len(assets.MenuImages) > 0 {
		// header
		if header := strings.TrimSpace(req.Runtime.Description.DiscMenuHeader); header != "" {
			appendPart("disc_menu", header)
		}
		// images
		if shots := screenshotBlock(assets.MenuImages); shots != "" {
			appendPart("disc_menu", shots)
		}
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", audioAnalysis)
	}
	if discReports != "" {
		appendPart("screenshots", discReports)
	}
	// Screenshot Header
	if header := strings.TrimSpace(req.Runtime.Description.ScreenshotHeader); header != "" {
		appendPart("screenshots", header)
	}

	// Tonemapped Header
	if tonemapHeader := descriptionunit3d.TonemappingNotice(req.Runtime.Description.TonemappedHeader, assets.Screenshots); tonemapHeader != "" {
		appendPart("screenshots", tonemapHeader)
	}

	// screenshots
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		appendPart("screenshots", shots)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.Runtime.Description.CustomSignature); signature != "" {
		appendPart("", signature)
	}

	// upbrr signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	appendPart("", fmt.Sprintf("[right][url=%s][size=1]%s[/size][/url][/right]", link, text))

	// finalize description
	return finalizeDescription(strings.TrimSpace(description.FormatSections(parts, req.Runtime.Description)))
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.RawURL) == "" {
			continue
		}
		parts = append(parts, "<a href='"+strings.TrimSpace(image.RawURL)+"'><img src='"+strings.TrimSpace(image.ImgURL)+"' height=137></a> ")
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
	return trackers.DescriptionResult{Group: "hdt", Description: description}, nil
}
