// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strconv"
	"strings"
	"time"
)

func validationScreenshotFingerprint(exact *ExactMediaAssets, tracker string) WorkflowFingerprint {
	if exact == nil {
		return ""
	}
	selected := make(map[string]int)
	var images []ScreenshotImage
	for _, image := range exact.Screenshots {
		if image.Purpose != ScreenshotPurposeFinal {
			continue
		}
		selected[strings.TrimSpace(image.Path)] = len(images)
		image.Path, image.DiscName = "", ""
		image.Host, image.ImgURL, image.RawURL, image.WebURL = "", "", "", ""
		image.UploadedAt = time.Time{}
		images = append(images, image)
	}
	var links []UploadedImageLink
	for _, link := range exact.ScreenshotUploads {
		scope := strings.TrimSpace(link.UsageScope)
		if strings.HasPrefix(strings.ToLower(scope), "tracker:") {
			owner := strings.TrimSpace(scope[len("tracker:"):])
			if owner == "" {
				scope = "global"
			} else {
				scope = "tracker:" + strings.ToUpper(owner)
			}
		}
		index, present := selected[strings.TrimSpace(link.ImagePath)]
		if !present ||
			(scope != "" && !strings.EqualFold(scope, "global") && scope != "tracker:"+strings.ToUpper(strings.TrimSpace(tracker))) {
			continue
		}
		// Bind the host variant to selection order, not its materialized local path.
		link.ImagePath = strconv.Itoa(index)
		link.SourcePath, link.PreparedMediaFingerprint = "", ""
		link.PreparedGeneration = 0
		link.DiscID, link.Purpose = "", ScreenshotPurposeFinal
		link.Host = strings.ToLower(strings.TrimSpace(link.Host))
		link.UsageScope = strings.ToLower(scope)
		if link.UsageScope == "" {
			link.UsageScope = "global"
		}
		link.UploadedAt = time.Time{}
		links = append(links, link)
	}
	fingerprint, _ := CanonicalWorkflowFingerprint(struct {
		Images []ScreenshotImage
		Links  []UploadedImageLink
	}{images, links})
	return fingerprint
}
