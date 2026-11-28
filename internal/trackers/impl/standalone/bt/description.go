// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

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
	text := composeDescription(req, assets)

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "BT", req.Runtime.DBPath, text, req.Logger)
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

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		appendPart("custom_header", header)
	}

	// Logo
	if logo := resolveLogo(meta); logo != "" {
		appendPart("logo", "[center][img]"+logo+"[/img][/center]")
	}

	// TV Episode details
	epTitle := meta.EpisodeTitle
	epOverview := meta.EpisodeOverview
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if ptBR.EpisodeTitle != "" {
		epTitle = ptBR.EpisodeTitle
	}
	if ptBR.EpisodeOverview != "" {
		epOverview = ptBR.EpisodeOverview
	}
	if episode := strings.TrimSpace(epOverview); episode != "" {
		if title := strings.TrimSpace(epTitle); title != "" {
			appendPart("episode_overview", "[center]"+title+"[/center]")
		}
		appendPart("episode_overview", "[center]"+episode+"[/center]")
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
	appendPart("", fmt.Sprintf("[center][url=%s]Upload realizado via %s[/url][/center]", link, "upbrr"))

	// Join and finalize
	return finalizeDescription(description.FormatSections(parts, req.Runtime.Description))
}

func resolveDirectors(meta api.UploadSubject) string {
	var directors []string
	seen := make(map[string]struct{})

	addDirector := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			directors = append(directors, name)
		}
	}

	if meta.ProviderMetadata.TMDB != nil {
		for _, name := range meta.ProviderMetadata.TMDB.Directors {
			addDirector(name)
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		for _, person := range meta.ProviderMetadata.IMDB.Directors {
			addDirector(person.Name)
		}
	}

	if len(directors) > 0 {
		limit := min(len(directors), 5)
		return strings.Join(directors[:limit], ", ")
	}

	return "N/A"
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		if meta.ProviderMetadata.TMDB.Localized != nil {
			if localized, ok := meta.ProviderMetadata.TMDB.Localized["pt-BR"]; ok && strings.TrimSpace(localized.Poster) != "" {
				return strings.TrimSpace(localized.Poster)
			}
		}
		if strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster) != "" {
			return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
		}
	}
	if meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	}
	return ""
}

func resolveScreens(assets trackers.DescriptionAssets) []string {
	var screens []string
	seen := make(map[string]struct{})

	for _, image := range assets.MenuImages {
		u := strings.TrimSpace(image.RawURL)
		if u == "" {
			u = strings.TrimSpace(image.ImgURL)
		}
		if u != "" && !isSeen(seen, u) {
			screens = append(screens, u)
			seen[u] = struct{}{}
		}
	}
	for _, image := range assets.Screenshots {
		u := strings.TrimSpace(image.RawURL)
		if u == "" {
			u = strings.TrimSpace(image.ImgURL)
		}
		if u != "" && !isSeen(seen, u) {
			screens = append(screens, u)
			seen[u] = struct{}{}
		}
	}
	return screens
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
