// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

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
	finalDescription := composeDescription(req, assets)

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "GPW", req.Runtime.DBPath, finalDescription, req.Logger)
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
	}, sample.Assets), nil
}

func composeDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
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
	// custom header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		appendPart("custom_header", header)
	}

	// logo
	logoURL, logoSize := descriptionunit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig())
	if logoURL != "" {
		if strings.HasSuffix(logoURL, ".svg") {
			logoURL = strings.ReplaceAll(logoURL, ".svg", ".png")
		}
		appendPart("logo", fmt.Sprintf("[center][img=%d]%s[/img][/center]", logoSize, logoURL))
	}

	// description
	if base := strings.TrimSpace(assets.Description); base != "" {
		appendPart("kept", base)
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
	appendPart("", fmt.Sprintf("[align=right][url=%s][size=1]%s[/size][/url][/align]", link, text))

	// finalize description
	return finalizeDescription(strings.TrimSpace(description.FormatSections(parts, req.Runtime.Description)))
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		rawURL := strings.TrimSpace(image.RawURL)
		if rawURL == "" {
			continue
		}
		imgURL := strings.TrimSpace(image.ImgURL)
		if imgURL != "" && imgURL != rawURL {
			parts = append(parts, "[url="+rawURL+"][img]"+imgURL+"[/img][/url]")
			continue
		}
		parts = append(parts, "[img]"+rawURL+"[/img]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[center]" + strings.Join(parts, " ") + "[/center]"
}

func resolveOverview(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot)
	}
	return ""
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	}
	return ""
}

func resolveDirectorName(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Directors) > 0 {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Directors[0])
	}
	if meta.ProviderMetadata.IMDB != nil && len(meta.ProviderMetadata.IMDB.Directors) > 0 {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Directors[0].Name)
	}
	return ""
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
			ID:         "logo",
			Label:      "Title logo",
			CanCompact: true,
			Fields:     []string{"AddLogo", "LogoSize", "LogoLanguage"},
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
			Fields:     []string{"ScreenshotHeader", "TonemappedHeader"},
		},
		{Label: "Signature footer", Fields: []string{"CustomSignature"}},
	}
}
