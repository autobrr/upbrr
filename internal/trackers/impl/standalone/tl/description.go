// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
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
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "TL", req.Runtime.DBPath, finalDescription, req.Logger)
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
		if logo, _ := descriptionunit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig()); logo != "" {
			appendPart("logo", `<center><img src="`+logo+`" style="max-width: 300px;"></center>`)
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media != "" {
		appendPart("mediainfo", media)
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		appendPart("kept", strings.TrimSpace(assets.Description))
	}

	// menu
	if shots := screenshotBlock(assets.MenuImages); !nativeRehosting(req.TrackerConfig) && shots != "" {
		// header
		if header := strings.TrimSpace(req.Runtime.Description.DiscMenuHeader); header != "" {
			appendPart("disc_menu", header)
		}
		// images
		appendPart("disc_menu", shots)
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", audioAnalysis)
	}
	if discReports != "" {
		appendPart("screenshots", discReports)
	}
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		embed := !nativeRehosting(req.TrackerConfig)
		if embed {
			if header := strings.TrimSpace(req.Runtime.Description.ScreenshotHeader); header != "" {
				appendPart("screenshots", header)
			}
		}
		if notice := descriptionunit3d.TonemappingNotice(req.Runtime.Description.TonemappedHeader, assets.Screenshots); notice != "" {
			appendPart("screenshots", notice)
		}
		if embed {
			appendPart("screenshots", shots)
		}
	}

	// custom user signature
	if signature := strings.TrimSpace(req.Runtime.Description.CustomSignature); signature != "" {
		appendPart("", signature)
	}

	// upbrr signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	appendPart("", fmt.Sprintf("<div style=\"text-align: right; font-size: 11px;\"><a href=\"%s\">%s</a></div>", link, text))

	// finalize description
	return finalizeDescription(strings.TrimSpace(description.FormatSections(parts, req.Runtime.Description)))
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return providerid.IMDb(meta.Identity.IMDBID).URL()
}

func tvmazeURL(meta api.UploadSubject) string {
	if meta.Identity.TVmazeID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.tvmaze.com/shows/%d", meta.Identity.TVmazeID)
}

func nativeRehosting(cfg config.TrackerConfig) bool {
	return !cfg.APIUpload && cfg.ImgRehost
}

func screenshots(groups ...[]api.ScreenshotImage) []string {
	var out []string
	seen := make(map[string]bool)
	for _, images := range groups {
		for _, image := range images {
			if raw := metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL); raw != "" && !seen[raw] {
				out = append(out, raw)
				seen[raw] = true
			}
		}
	}
	return out
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := []string{"<center>"}
	count := 0
	for idx, image := range images {
		img := metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL)
		web := metautil.FirstNonEmptyTrimmed(image.WebURL, img)
		if img == "" || web == "" {
			continue
		}
		parts = append(parts, `<a href="`+web+`"><img src="`+img+`" style="max-width: 350px;"></a>`)
		count++
		if (idx+1)%2 == 0 {
			parts = append(parts, "<br><br>")
		}
	}
	if count == 0 {
		return ""
	}
	parts = append(parts, "</center>")
	return strings.Join(parts, "  ")
}

func genresText(meta api.UploadSubject) string {
	provider := ""
	if meta.ProviderMetadata.TMDB != nil {
		provider = meta.ProviderMetadata.TMDB.Genres
	}
	return trackers.PreferredGenreText(meta, provider)
}
