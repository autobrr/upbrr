// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"fmt"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	parts := make([]string, 0, 3)
	// custom header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// logo
	logoURL, logoSize := descriptionunit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig())
	if logoURL != "" {
		if strings.HasSuffix(logoURL, ".svg") {
			logoURL = strings.ReplaceAll(logoURL, ".svg", ".png")
		}
		parts = append(parts, fmt.Sprintf("[center][img=%d]%s[/img][/center]", logoSize, logoURL))
	}

	// description
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, base)
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
	parts = append(parts, fmt.Sprintf("[align=right][url=%s][size=1]%s[/size][/url][/align]", link, text))

	// finalize description
	finalDescription := finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "GPW", req.Runtime.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
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
		parts = append(parts, "[img]"+strings.TrimSpace(image.RawURL)+"[/img]")
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
