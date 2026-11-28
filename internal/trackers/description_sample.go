// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// DescriptionSample is synthetic, in-memory content for the defaults panel.
// It carries no source paths, credentials, network clients or repositories.
type DescriptionSample struct {
	Subject api.UploadSubject
	Assets  DescriptionAssets
	Report  string
}

// DescriptionLabel describes a single editable tracker-owned heading.
type DescriptionLabel struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Default string `json:"default"`
}

// DescriptionLayoutItem identifies a movable section and its existing settings.
type DescriptionLayoutItem struct {
	ID         string             `json:"id"`
	Label      string             `json:"label"`
	Fields     []string           `json:"fields,omitempty"`
	Labels     []DescriptionLabel `json:"labels,omitempty"`
	CanHide    bool               `json:"canHide,omitempty"`
	CanCompact bool               `json:"canCompact,omitempty"`
}

// DescriptionSampleFunc composes synthetic prepared media without release operations.
type DescriptionSampleFunc func(context.Context, DescriptionSample, config.DescriptionSettingsConfig, config.TrackerConfig) (string, error)

// DescriptionSampleRenderer opts in with a compositional renderer that can be
// used without reading release artifacts, authenticating or uploading images.
type DescriptionSampleRenderer interface {
	DescriptionLayout() []DescriptionLayoutItem
	RenderDescriptionSample(context.Context, DescriptionSample, config.DescriptionSettingsConfig, config.TrackerConfig) (string, error)
}

// ComparisonLayoutItem introduces an optional named spoiler without changing
// the comparison rows, their source labels, or their reviewed evidence.
func ComparisonLayoutItem() DescriptionLayoutItem {
	return DescriptionLayoutItem{
		ID:         "comparison",
		Label:      "Source comparisons",
		CanCompact: true,
		Labels: []DescriptionLabel{{
			Key:     "comparison.heading",
			Label:   "Comparison wrapper heading",
			Default: "",
		}},
	}
}

// SharedDescriptionLayout describes the shared Unit3D composer sections.
func SharedDescriptionLayout() []DescriptionLayoutItem {
	return []DescriptionLayoutItem{
		{
			ID:         "template",
			Label:      "Description template",
			CanCompact: true,
		},
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: true,
		},
		ComparisonLayoutItem(),
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
			Fields:     []string{"EpisodeOverview"},
		},
		{
			ID:         "bluray",
			Label:      "Blu-ray release",
			CanCompact: true,
			Fields:     []string{"AddBlurayLink", "UseBlurayImages", "BlurayImageSize"},
		},
		{
			ID:         "dvd_vob_mediainfo",
			Label:      "VOB MediaInfo",
			CanCompact: true,
			Fields:     []string{"VOBReportLabel"},
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
			ID:         "tonemap_header",
			Label:      "Tonemapping notice",
			CanCompact: true,
			Fields:     []string{"TonemappedHeader"},
		},
		{
			ID:         "screenshots",
			Label:      "Screenshots and additional disc reports",
			CanCompact: true,
			Fields:     []string{"ScreenshotHeader", "ThumbnailSize", "ScreensPerRow"},
		},
		{Label: "Signature footer", Fields: []string{"CustomSignature"}},
	}
}

// StandaloneGalleryLayout lists the groups used by tracker-local gallery renderers
// that include inline technical evidence and use the standard heading preferences.
func StandaloneGalleryLayout() []DescriptionLayoutItem {
	return []DescriptionLayoutItem{
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
			Fields:     []string{"AddLogo"},
		},
		{
			ID:         "episode_overview",
			Label:      "Episode overview",
			CanCompact: true,
		},
		{
			ID:         "mediainfo",
			Label:      "MediaInfo or BDInfo",
			CanCompact: true,
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

// TextDescriptionLayout describes trackers that accept a single text block
// with technical evidence and screenshots carried outside the description.
func TextDescriptionLayout() []DescriptionLayoutItem {
	return []DescriptionLayoutItem{{Label: "Imported or custom text"}}
}

// TextAndGalleryLayout covers notes and a gallery, with spacing controls only
// for trackers that separate the sections with blank lines.
func TextAndGalleryLayout(canCompact bool) []DescriptionLayoutItem {
	return []DescriptionLayoutItem{
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: canCompact,
		},
		{
			ID:         "screenshots",
			Label:      "Screenshots",
			CanCompact: canCompact,
		},
	}
}
