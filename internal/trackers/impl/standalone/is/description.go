// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	parts := make([]string, 0, 8)
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "Title: "+strings.TrimSpace(meta.EpisodeTitle), "Overview: "+strings.TrimSpace(meta.EpisodeOverview))
	}
	if media := trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta); media != "" {
		parts = append(parts, media)
	}
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
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
			parts = append(parts, strings.Join(menuLines, "\n"))
		}
	}
	if len(assets.Screenshots) > 0 {
		var shotLines []string
		for _, image := range assets.Screenshots {
			if strings.TrimSpace(image.RawURL) != "" {
				shotLines = append(shotLines, image.RawURL)
			}
		}
		if len(shotLines) > 0 {
			parts = append(parts, "Screenshots:\n"+strings.Join(shotLines, "\n"))
		}
	}
	return finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))
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
