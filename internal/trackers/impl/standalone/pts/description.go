// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	parts := make([]string, 0, 4)
	if info := commonhttp.ReadOptionalFile(strings.TrimSpace(meta.MediaInfoTextPath)); strings.TrimSpace(info) != "" {
		parts = append(parts, info)
	}
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, sanitizeDescription(base))
	}
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	parts = append(parts, "[right][url=https://github.com/autobrr/upbrr][size=1]upbrr[/size][/url][/right]")
	return finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))
}

func sanitizeDescription(input string) string {
	return finalizeDescription(input)
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	lines := []string{"[center][b]Screenshots[/b]"}
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
