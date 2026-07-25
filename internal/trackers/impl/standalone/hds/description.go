// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"fmt"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"

	"context"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	parts := make([]string, 0, 12)

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if req.Runtime.Description.AddLogo {
		if logo := resolveLogo(meta); logo != "" {
			parts = append(parts, "[center][img]"+logo+"[/img][/center]")
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media := trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta); media != "" {
		parts = append(parts, "[pre]"+media+"[/pre]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// menu
	if len(assets.MenuImages) > 0 {
		// header
		if header := strings.TrimSpace(req.Runtime.Description.DiscMenuHeader); header != "" {
			parts = append(parts, header)
		}
		// images
		if shots := screenshotBlock(assets.MenuImages); shots != "" {
			parts = append(parts, shots)
		}
	}
	// Screenshot Header
	if header := strings.TrimSpace(req.Runtime.Description.ScreenshotHeader); header != "" {
		parts = append(parts, header)
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(
		req.Runtime.Description.TonemappedHeader,
	); tonemapHeader != "" &&
		descriptionunit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// screenshots
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.Runtime.Description.CustomSignature); signature != "" {
		parts = append(parts, signature)
	}

	// upbrr signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[center][url=%s][size=2]%s[/size][/url][/center]", link, text))

	// finalize description
	finalDescription := finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "HDS", req.Runtime.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, image := range images {
		if strings.TrimSpace(image.WebURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		sb.WriteString("[url=" + image.WebURL + "][img]" + image.ImgURL + "[/img][/url]")

		// HDS cannot resize images. If the image host does not provide small thumbnails(<400px), place only one image per line.
		// imgbox provides small thumbnails, so we can place them side-by-side.
		if !strings.Contains(strings.ToLower(image.WebURL), "imgbox") {
			sb.WriteString("\n")
		}
	}
	content := strings.TrimSpace(sb.String())
	if content == "" {
		return ""
	}
	return "[center]\n" + content + "\n[/center]"
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
	return trackers.DescriptionResult{Group: "hds", Description: description}, nil
}
