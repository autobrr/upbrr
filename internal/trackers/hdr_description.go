// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var sourceHDRBlockPattern = regexp.MustCompile(`(?is)\[spoiler=(?:source_hdr|HDR plots)\].*?\[/spoiler\]`)

// exactHDRDescriptionBlock renders only video evidence in the HDR section.
func exactHDRDescriptionBlock(tracker string, exact *api.ExactMediaAssets) (string, error) {
	parts, err := exactHDRDescriptionParts(tracker, exact, false)
	if err != nil || len(parts) == 0 {
		return "", err
	}
	return "[spoiler=HDR plots]\n" + strings.Join(parts, "\n") + "\n[/spoiler]", nil
}

// exactHDRDescriptionParts separates video and audio presentation while retaining
// the exact HDR run's host and upload-purpose authority for both.
func exactHDRDescriptionParts(tracker string, exact *api.ExactMediaAssets, audio bool) ([]string, error) {
	if exact == nil || len(exact.HDRPlots) == 0 && len(exact.HDRStats) == 0 {
		return nil, nil
	}
	selectedHost := strings.TrimSpace(exact.HDRUploadHosts[strings.ToUpper(strings.TrimSpace(tracker))])
	parts := make([]string, 0, len(exact.HDRPlots)*2)
	for _, plot := range exact.HDRPlots {
		if exact.CollectionHost == "slowpics" {
			continue
		}
		if (plot.Kind.DecodedMode() == api.HDRDecodedAudio) != audio {
			continue
		}
		if selectedHost == "" {
			if !exact.ImageHostUploadSkipped {
				return nil, fmt.Errorf("HDR image host is unavailable for %s", tracker)
			}
			continue
		}
		upload, ok := exactUploadedVariant(tracker, plot.Image.Path, exact.HDRUploads, selectedHost)
		imageURL := strings.TrimSpace(upload.RawURL)
		if imageURL == "" {
			imageURL = strings.TrimSpace(upload.ImgURL)
		}
		if !ok || imageURL == "" || upload.Purpose != api.ScreenshotPurposeHDRAnalysis {
			if exact.ImageHostUploadSkipped {
				continue
			}
			return nil, fmt.Errorf("hosted HDR image is unavailable for %s", tracker)
		}
		label := strings.ReplaceAll(strings.ReplaceAll(plot.Label, "[", "&#91;"), "]", "&#93;")
		parts = append(parts, "[b]"+label+"[/b]", "[img]"+imageURL+"[/img]")
	}
	if audio {
		for _, stats := range exact.HDRStats {
			label := strings.ReplaceAll(strings.ReplaceAll(stats.Label, "[", "&#91;"), "]", "&#93;")
			parts = append(parts, "[b]"+label+"[/b]", "[code]"+strings.ReplaceAll(stats.Text, "[", "&#91;")+"[/code]")
		}
	}
	return parts, nil
}
