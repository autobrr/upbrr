// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var sourceHDRBlockPattern = regexp.MustCompile(`(?is)\[spoiler=source_hdr\].*?\[/spoiler\]`)

func exactHDRDescriptionBlock(tracker string, exact *api.ExactMediaAssets) (string, error) {
	if exact == nil || len(exact.HDRPlots) == 0 {
		return "", nil
	}
	selectedHost := strings.TrimSpace(exact.HDRUploadHosts[strings.ToUpper(strings.TrimSpace(tracker))])
	if selectedHost == "" {
		return "", fmt.Errorf("HDR image host is unavailable for %s", tracker)
	}
	parts := make([]string, 0, len(exact.HDRPlots)*2)
	for _, plot := range exact.HDRPlots {
		upload, ok := exactUploadedVariant(tracker, plot.Image.Path, exact.HDRUploads, selectedHost)
		imageURL := strings.TrimSpace(upload.RawURL)
		if imageURL == "" {
			imageURL = strings.TrimSpace(upload.ImgURL)
		}
		if !ok || imageURL == "" || upload.Purpose != api.ScreenshotPurposeHDRAnalysis {
			return "", fmt.Errorf("hosted HDR image is unavailable for %s", tracker)
		}
		label := strings.ReplaceAll(strings.ReplaceAll(plot.Label, "[", "&#91;"), "]", "&#93;")
		parts = append(parts, "[b]"+label+"[/b]", "[img]"+imageURL+"[/img]")
	}
	return "[spoiler=source_hdr]\n" + strings.Join(parts, "\n") + "\n[/spoiler]", nil
}
