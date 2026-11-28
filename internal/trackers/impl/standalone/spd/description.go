// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

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
	text := composeDescription(req, assets)

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "SPD", req.Runtime.DBPath, text, req.Logger)
	}

	return text
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
	meta := req.Meta
	var parts []description.Section
	appendPart := func(id, text string) {
		parts = description.AppendSection(parts, id, text)
	}

	// Avoid unnecessary descriptions
	if strings.TrimSpace(assets.Description) != "" || strings.TrimSpace(meta.EpisodeOverview) != "" {
		// Custom Header
		if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
			appendPart("custom_header", header)
		}

		// Logo
		if logo, logoSize := descriptionunit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig()); logo != "" {
			appendPart("logo", fmt.Sprintf("[center][img=%d]https://image.tmdb.org/t/p/w300/%s[/img][/center]", logoSize, logo))
		}

		// TV Details
		if strings.TrimSpace(meta.EpisodeOverview) != "" {
			appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
			appendPart("episode_overview", "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
		}

		// User Description
		if strings.TrimSpace(assets.Description) != "" {
			appendPart("kept", strings.TrimSpace(assets.Description))
		}
	}

	// Tonemapped Header
	if tonemapHeader := descriptionunit3d.TonemappingNotice(req.Runtime.Description.TonemappedHeader, assets.Screenshots); tonemapHeader != "" {
		appendPart("tonemap_header", tonemapHeader)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.Runtime.Description.CustomSignature); signature != "" {
		appendPart("", signature)
	}

	// Signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	appendPart("", fmt.Sprintf("[url=%s]%s[/url]", link, text))

	// Join and finalize
	return finalizeDescription(description.FormatSections(parts, req.Runtime.Description))
}

func combineUniqueScreenshots(menu []api.ScreenshotImage, normal []api.ScreenshotImage) []string {
	var out []string
	seen := make(map[string]struct{})

	for _, image := range menu {
		u := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL))
		if u != "" && !isSeen(seen, u) {
			out = append(out, u)
			seen[u] = struct{}{}
		}
	}
	for _, image := range normal {
		u := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL))
		if u != "" && !isSeen(seen, u) {
			out = append(out, u)
			seen[u] = struct{}{}
		}
	}
	return out
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return providerid.IMDb(meta.Identity.IMDBID).URL()
}

func genresText(meta api.UploadSubject) string {
	var provider string
	if meta.ProviderMetadata.TMDB != nil {
		provider = metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
	} else {
		provider = meta.Release.Genre
	}
	if meta.EffectiveMetadata.GenresProvenance.IsManual() {
		return trackers.PreferredGenreText(meta, provider)
	}
	return strings.TrimSpace(provider)
}

func keywordsText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
}

func tmdbBackdrop(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Backdrop)
}

func tmdbPoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
}

func tmdbOverview(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
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
	return trackers.DescriptionResult{Group: "spd", Description: description}, nil
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
			ID:         "episode_overview",
			Label:      "Episode overview",
			CanCompact: true,
		},
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: true,
		},
		{
			ID:         "tonemap_header",
			Label:      "Tonemapping notice",
			CanCompact: true,
			Fields:     []string{"TonemappedHeader"},
		},
		{Label: "Signature footer", Fields: []string{"CustomSignature"}},
	}
}
