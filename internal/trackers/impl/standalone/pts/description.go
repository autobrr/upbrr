// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, assets trackers.DescriptionAssets, defaults config.DescriptionSettingsConfig) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	return composeDescription(assets, defaults, commonhttp.ReadOptionalFile(strings.TrimSpace(meta.MediaInfoTextPath)))
}

func descriptionDefaultsLayout() []trackers.DescriptionLayoutItem {
	return []trackers.DescriptionLayoutItem{
		{
			ID:         "mediainfo",
			Label:      "MediaInfo",
			CanCompact: true,
		},
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: true,
		},
		{
			ID:         "screenshots",
			Label:      "Screenshots and disc reports",
			CanCompact: true,
			Fields:     []string{"ScreenshotLabel"},
		},
		{Label: "upbrr signature footer"},
	}
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	_ config.TrackerConfig,
) (string, error) {
	return composeDescription(sample.Assets, defaults, sample.Report), nil
}

func composeDescription(assets trackers.DescriptionAssets, defaults config.DescriptionSettingsConfig, report string) string {
	baseDescription, discReports := description.SplitLeadingDiscReports(trackers.StripDescriptionSignatures(assets.Description))
	parts := make([]description.Section, 0, 4)
	if strings.TrimSpace(report) != "" {
		parts = description.AppendSection(parts, "mediainfo", report)
	}
	if base := strings.TrimSpace(baseDescription); base != "" {
		parts = description.AppendSection(parts, "kept", sanitizeDescription(base))
	}
	if discReports != "" {
		parts = description.AppendSection(parts, "screenshots", sanitizeDescription(discReports))
	}
	if shots := screenshotBlock(assets.Screenshots, defaults.ScreenshotLabel); shots != "" {
		parts = description.AppendSection(parts, "screenshots", shots)
	}
	parts = description.AppendSection(parts, "", "[right][url=https://github.com/autobrr/upbrr][size=1]upbrr[/size][/url][/right]")
	return finalizeDescription(strings.TrimSpace(description.FormatSections(parts, defaults)))
}

func sanitizeDescription(input string) string {
	return finalizeDescription(input)
}

func screenshotBlock(images []api.ScreenshotImage, label string) string {
	if len(images) == 0 {
		return ""
	}
	label = description.MarkupLabel(label)
	if label == "" {
		label = "Screenshots"
	}
	lines := []string{"[center][b]" + label + "[/b]"}
	for _, image := range images {
		imgURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL))
		webURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.WebURL, imgURL))
		if imgURL == "" || webURL == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[url=%s][img]%s[/img][/url]", webURL, imgURL))
	}
	lines = append(lines, "[/center]")
	return strings.Join(lines, "\n")
}

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return providerid.IMDb(meta.Identity.IMDBID).URL()
}
