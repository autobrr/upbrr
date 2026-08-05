// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	meta := req.Meta
	var parts []string

	// Base description
	base := strings.TrimSpace(antDefaultSignaturePattern.ReplaceAllString(assets.Description, ""))
	report := bbcode.CleanPTPDescription(base, meta.DiscType)
	userDesc := strings.TrimSpace(report.Description)
	if userDesc == "" && base != "" && len(report.Images) == 0 {
		userDesc = base
	}

	if userDesc != "" {
		// Custom Header
		if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
			parts = append(parts, header)
		}

		// Logo
		logoURL, _ := unit3d.ResolveLogo(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig())
		if logoURL != "" {
			if strings.HasSuffix(logoURL, ".svg") {
				logoURL = strings.ReplaceAll(logoURL, ".svg", ".png")
			}
			parts = append(parts, "[align=center][img]"+logoURL+"[/img][/align]")
		}

		// User Description
		parts = append(parts, userDesc)
	}

	// Disc menus
	if len(assets.MenuImages) > 0 {
		if header := strings.TrimSpace(req.Runtime.Description.DiscMenuHeader); header != "" {
			parts = append(parts, header)
		}
		var shotParts []string
		for _, img := range assets.MenuImages {
			url := metautil.FirstNonEmptyTrimmed(img.RawURL, img.ImgURL, img.WebURL)
			if url != "" {
				shotParts = append(shotParts, "[img]"+url+"[/img]")
			}
		}
		if len(shotParts) > 0 {
			parts = append(parts, "[align=center]"+strings.Join(shotParts, " ")+"[/align]")
		}
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(
		req.Runtime.Description.TonemappedHeader,
	); tonemapHeader != "" &&
		unit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// Join and finalize
	description := strings.Join(parts, "\n\n")

	finalized := finalizeDescription(description)

	// Character replacements
	replacer := strings.NewReplacer("•", "-", "’", "'", "–", "-")
	finalized = replacer.Replace(finalized)

	finalized = strings.TrimSpace(antEmptyURLPattern.ReplaceAllString(finalized, ""))

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		unit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "ANT", req.Runtime.DBPath, finalized, req.Logger)
	}

	return finalized
}

func resolveScreenshotPayload(images []api.ScreenshotImage, allow bool) string {
	if !allow || len(images) == 0 {
		return ""
	}
	urls := make([]string, 0, 4)
	for _, image := range images {
		rawURL := strings.TrimSpace(image.RawURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(image.ImgURL)
		}
		if rawURL == "" {
			continue
		}
		urls = append(urls, rawURL)
		if len(urls) == 4 {
			break
		}
	}
	return strings.Join(urls, "\n")
}
