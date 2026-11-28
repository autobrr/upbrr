// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	text := composeDescription(req, assets, readDescriptionReport(req))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "BJS", req.Runtime.DBPath, text, req.Logger)
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
	}, sample.Assets, sample.Report), nil
}

func readDescriptionReport(req trackers.PreparationInput) string {
	switch strings.ToUpper(strings.TrimSpace(req.Meta.DiscType)) {
	case "DVD", "HDDVD":
		return strings.TrimSpace(commonhttp.ReadOptionalFile(req.Meta.MediaInfoTextPath))
	case "BDMV":
		report, _ := trackers.ReadBDInfoForDescription(req.Runtime.DBPath, req.Meta)
		return report
	default:
		return ""
	}
}

func composeDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets, report string) string {
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	meta := req.Meta
	var parts []description.Section
	appendPart := func(id, text string) {
		parts = description.AppendSection(parts, id, text)
	}

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		appendPart("custom_header", header)
	}

	// Logo
	if logo := resolveLogo(meta); logo != "" {
		appendPart("logo", "[align=center][img]"+logo+"[/img][/align]")
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
			appendPart("episode_overview", "[align=center]"+epTitle+"[/align]")
		}
		appendPart("episode_overview", "[align=center]"+epOverview+"[/align]")
	}

	// File information
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if discType == "DVD" || discType == "HDDVD" {
		if report != "" {
			appendPart("mediainfo", "[hide=DVD MediaInfo][pre]"+report+"[/pre][/hide]")
		}
	}
	if discType == "BDMV" {
		appendPart("mediainfo", "[hide=BDInfo][pre]"+report+"[/pre][/hide]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		appendPart("kept", strings.TrimSpace(assets.Description))
	}

	// Tonemapped Header
	if tonemapHeader := descriptionunit3d.TonemappingNotice(req.Runtime.Description.TonemappedHeader, assets.Screenshots); tonemapHeader != "" {
		appendPart("tonemap_header", tonemapHeader)
	}

	// Signature
	link, _ := descriptionunit3d.UppbrrSignatureLink()
	appendPart("", fmt.Sprintf("[align=center][url=%s]Upload realizado via %s[/url][/align]", link, "upbrr"))

	// Join and finalize
	return finalizeDescription(description.FormatSections(parts, req.Runtime.Description))
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
		},
		{
			ID:         "episode_overview",
			Label:      "Episode overview",
			CanCompact: true,
		},
		{
			ID:         "mediainfo",
			Label:      "DVD MediaInfo or BDInfo",
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
		{Label: "Signature footer"},
	}
}
