// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"fmt"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"

	"context"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	var parts []string

	// Avoid unnecessary descriptions
	if strings.TrimSpace(assets.Description) != "" || strings.TrimSpace(meta.EpisodeOverview) != "" {
		// Custom Header
		if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
			parts = append(parts, header)
		}

		// Logo
		if logo, logoSize := descriptionunit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig()); logo != "" {
			parts = append(parts, fmt.Sprintf("[center][img=%d]https://image.tmdb.org/t/p/w300/%s[/img][/center]", logoSize, logo))
		}

		// TV Details
		if strings.TrimSpace(meta.EpisodeOverview) != "" {
			parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
			parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
		}

		// User Description
		if strings.TrimSpace(assets.Description) != "" {
			parts = append(parts, strings.TrimSpace(assets.Description))
		}
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(
		req.Runtime.Description.TonemappedHeader,
	); tonemapHeader != "" &&
		descriptionunit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.Runtime.Description.CustomSignature); signature != "" {
		parts = append(parts, signature)
	}

	// Signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[url=%s]%s[/url]", link, text))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := finalizeDescription(description)

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "SPD", req.Runtime.DBPath, finalized, req.Logger)
	}

	return finalized
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
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.Identity.IMDBID)
}

func genresText(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Genres, meta.Release.Genre)
	}
	return strings.TrimSpace(meta.Release.Genre)
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
