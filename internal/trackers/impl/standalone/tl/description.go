// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"fmt"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	parts := make([]string, 0, 8)

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if req.Runtime.Description.AddLogo {
		if logo, _ := descriptionunit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig()); logo != "" {
			parts = append(parts, `<center><img src="`+logo+`" style="max-width: 300px;"></center>`)
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media := trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta); media != "" {
		parts = append(parts, media)
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
	parts = append(parts, fmt.Sprintf("<div style=\"text-align: right; font-size: 11px;\"><a href=\"%s\">%s</a></div>", link, text))

	// finalize description
	finalDescription := finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "TL", req.Runtime.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.Identity.IMDBID)
}

func tvmazeURL(meta api.UploadSubject) string {
	if meta.Identity.TVmazeID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.tvmaze.com/shows/%d", meta.Identity.TVmazeID)
}

func screenshots(images []api.ScreenshotImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if raw := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL)); raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := []string{"<center>"}
	for idx, image := range images {
		img := metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL)
		web := metautil.FirstNonEmptyTrimmed(image.WebURL, img)
		if img == "" || web == "" {
			continue
		}
		parts = append(parts, `<a href="`+web+`"><img src="`+img+`" style="max-width: 350px;"></a>`)
		if (idx+1)%2 == 0 {
			parts = append(parts, "<br><br>")
		}
	}
	parts = append(parts, "</center>")
	return strings.Join(parts, "  ")
}

func genresText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
	}
	return strings.TrimSpace(meta.Release.Genre)
}
