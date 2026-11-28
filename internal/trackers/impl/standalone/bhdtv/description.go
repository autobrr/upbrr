// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveInlineDescription(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "Disc so Check Mediainfo dump "
	}
	text, err := resolveMediaDump(meta, "")
	if err != nil {
		return ""
	}
	return text
}

func buildDescription(assets trackers.DescriptionAssets, defaults config.DescriptionSettingsConfig) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	baseDescription, discReports := description.SplitLeadingDiscReports(assets.Description)
	base := strings.ReplaceAll(strings.TrimSpace(baseDescription), "[img=250]", "[img=250x250]")
	gallery := make([]string, 0, 1+len(assets.Screenshots))
	if discReports != "" {
		gallery = append(gallery, discReports)
	}
	for _, image := range assets.Screenshots {
		webURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.WebURL, image.RawURL))
		imgURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL, image.WebURL))
		if webURL == "" || imgURL == "" {
			continue
		}
		gallery = append(gallery, fmt.Sprintf("[url=%s][img]%s[/img][/url]", webURL, imgURL))
	}
	parts := []description.Section{
		{ID: "kept", Text: base},
		{ID: "screenshots", Text: strings.Join(gallery, " ")},
	}
	return description.FormatSectionsSeparated(parts, defaults, " ")
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	_ config.TrackerConfig,
) (string, error) {
	return buildDescription(sample.Assets, defaults), nil
}
