// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"context"
	"fmt"
	"strings"

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

	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	if discSection := buildDiscSection(meta, req.Runtime.DBPath); discSection != "" {
		parts = append(parts, discSection)
	}

	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	allShots := make([]api.ScreenshotImage, 0, len(assets.MenuImages)+len(assets.Screenshots))
	allShots = append(allShots, assets.MenuImages...)
	allShots = append(allShots, assets.Screenshots...)
	if shots := buildScreenshotSection(allShots, maxInt(1, meta.Options.Screens)); shots != "" {
		parts = append(parts, shots)
	}

	if tonemapHeader := strings.TrimSpace(req.Runtime.Description.TonemappedHeader); tonemapHeader != "" &&
		unit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	link, text := unit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[center][url=%s]%s[/url][/center]", link, text))

	description := strings.Join(parts, "\n\n")
	finalized := finalizeDescription(description)

	if req.Intent == trackers.PreparationIntentDryRun {
		unit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "FLD", req.Runtime.DBPath, finalized, req.Logger)
	}

	return finalized
}

func buildScreenshotSection(images []api.ScreenshotImage, limit int) string {
	if len(images) == 0 || limit <= 0 {
		return ""
	}

	var section strings.Builder
	section.WriteString("[align=center]")
	count := 0
	for _, image := range images {
		if count >= limit {
			break
		}
		imgURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.RawURL), strings.TrimSpace(image.ImgURL))
		webURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.WebURL), strings.TrimSpace(image.RawURL), imgURL)
		if imgURL == "" || webURL == "" {
			continue
		}
		if count > 0 {
			if count%2 == 0 {
				section.WriteString("\n\n")
			} else {
				section.WriteByte(' ')
			}
		}
		line := fmt.Sprintf("[url=%s][img width=350]%s[/img][/url]", webURL, imgURL)
		section.WriteString(line)
		count++
	}
	section.WriteString("[/align]")
	return section.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
		Intent:        req.Intent,
	}, assets)
	return trackers.DescriptionResult{Group: "fld", Description: description}, nil
}
