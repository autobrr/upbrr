// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"fmt"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	var parts []string

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if logo := resolveLogo(meta); logo != "" {
		parts = append(parts, "[align=center][img]"+logo+"[/img][/align]")
	}

	// TV Episode details
	epTitle := meta.EpisodeTitle
	epOverview := meta.EpisodeOverview
	if meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Localized != nil {
		if ptBR, ok := meta.ProviderMetadata.TMDB.Localized["pt-BR"]; ok {
			if ptBR.EpisodeTitle != "" {
				epTitle = ptBR.EpisodeTitle
			}
			if ptBR.EpisodeOverview != "" {
				epOverview = ptBR.EpisodeOverview
			}
		}
	}
	epTitle = strings.TrimSpace(epTitle)
	epOverview = strings.TrimSpace(epOverview)
	if epOverview != "" {
		if epTitle != "" {
			parts = append(parts, "[align=center]"+epTitle+"[/align]")
		}
		parts = append(parts, "[align=center]"+epOverview+"[/align]")
	}

	// File information
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if discType == "DVD" || discType == "HDDVD" {
		mediainfo := strings.TrimSpace(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath))
		if mediainfo != "" {
			parts = append(parts, "[hide=DVD MediaInfo][pre]"+mediainfo+"[/pre][/hide]")
		}
	}
	if discType == "BDMV" {
		bdinfo, _ := trackers.ReadBDInfo(req.Runtime.DBPath, meta)
		parts = append(parts, "[hide=BDInfo][pre]"+bdinfo+"[/pre][/hide]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(
		req.Runtime.Description.TonemappedHeader,
	); tonemapHeader != "" &&
		descriptionunit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// Signature
	link, _ := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[align=center][url=%s]Upload realizado via %s[/url][/align]", link, "upbrr"))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := finalizeDescription(description)

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "BJS", req.Runtime.DBPath, finalized, req.Logger)
	}

	return finalized
}

// resolveOverview prefers scoped TV synopsis for episode/season-pack uploads,
// then localized title-level overview, then TMDB or IMDB fallback text.
func resolveOverview(meta api.UploadSubject, ptBR api.TMDBLocalizedData) string {
	if shouldUseScopedTVOverview(meta) && ptBR.EpisodeOverview != "" {
		return strings.TrimSpace(ptBR.EpisodeOverview)
	}
	if ptBR.Overview != "" {
		return strings.TrimSpace(ptBR.Overview)
	}
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot)
	}
	return ""
}

func resolveDirectors(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Directors) > 0 {
		return firstTrimmed(meta.ProviderMetadata.TMDB.Directors)
	}
	if meta.ProviderMetadata.IMDB != nil {
		names := make([]string, 0, len(meta.ProviderMetadata.IMDB.Directors))
		for _, p := range meta.ProviderMetadata.IMDB.Directors {
			if strings.TrimSpace(p.Name) != "" {
				names = append(names, strings.TrimSpace(p.Name))
			}
		}
		if len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		if meta.ProviderMetadata.TMDB.Localized != nil {
			if localized, ok := meta.ProviderMetadata.TMDB.Localized["pt-BR"]; ok && strings.TrimSpace(localized.Poster) != "" {
				return strings.TrimSpace(localized.Poster)
			}
		}
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
	}
	return ""
}

func resolveScreens(_ api.UploadSubject) []string {
	return nil
}
