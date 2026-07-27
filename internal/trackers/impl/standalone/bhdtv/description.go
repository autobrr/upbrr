// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveInlineDescription(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "Disc so Check Mediainfo dump "
	}
	text, err := resolveMediaDump(meta)
	if err != nil {
		return ""
	}
	return text
}

func buildDescription(assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	base := strings.ReplaceAll(strings.TrimSpace(assets.Description), "[img=250]", "[img=250x250]")
	parts := make([]string, 0, 1+len(assets.Screenshots))
	if base != "" {
		parts = append(parts, base)
	}
	for _, image := range assets.Screenshots {
		webURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.WebURL, image.RawURL))
		imgURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL, image.WebURL))
		if webURL == "" || imgURL == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[url=%s][img]%s[/img][/url]", webURL, imgURL))
	}
	return strings.Join(parts, " ")
}
