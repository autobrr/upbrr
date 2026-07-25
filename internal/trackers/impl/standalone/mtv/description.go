// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

var mtvQuoteTagPattern = regexp.MustCompile(`(?i)\[/?quote\]`)
var mtvCollapseLinesPattern = regexp.MustCompile(`\n{3,}`)

var mtvNFOBlockPattern = regexp.MustCompile(
	`(?is)(?:\[(?:center|align=center)\]\s*)?\[(?:spoiler|hide)=(?:Scene|FraMeSToR) NFO:\](?:\[(?:code|pre)\])?.*?(?:\[/(?:code|pre)\])?\[/(?:spoiler|hide)\](?:\s*\[/(?:center|align)\])?`,
)
var mtvURLImagePattern = regexp.MustCompile(`(?is)\[url=[^\]]+\]\s*\[img(?:[^\]]*)?\][^\[]+\[/img\]\s*\[/url\]`)
var mtvImagePattern = regexp.MustCompile(`(?is)\[img(?:[^\]]*)?\][^\[]+\[/img\]`)

var mtvSignaturePattern = regexp.MustCompile(
	`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`,
)
var mtvEmptyAlignPattern = regexp.MustCompile(`(?is)\[(?:center|right|left|align=(?:center|right|left))\]\s*\[/(?:center|right|left|align)\]`)

// BuildDescription assembles MTV's media details, eligible tone-mapping header,
// screenshots, and sanitized kept notes. Missing media files are ignored, while
// other read failures and context cancellation are returned.
func BuildDescription(
	ctx context.Context,
	meta api.UploadSubject,
	appConfig config.Config,
	keptDescription string,
	screenshots []api.ScreenshotImage,
) (string, error) {
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	parts := make([]string, 0, 8)

	mediaBlock, err := buildMediaInfoBlock(meta, appConfig.MainSettings.DBPath)
	if err != nil {
		return "", err
	}
	if mediaBlock != "" {
		parts = append(parts, mediaBlock)
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && strings.TrimSpace(meta.DVDVOBMediaInfoText) != "" {
		parts = append(parts, "[mediainfo]"+strings.TrimSpace(meta.DVDVOBMediaInfoText)+"[/mediainfo]")
	}

	if shouldIncludeTonemappedHeader(meta, appConfig, screenshots) {
		header := strings.TrimSpace(appConfig.Description.TonemappedHeader)
		if header != "" {
			parts = append(parts, header)
		}
	}

	if section := buildScreenshotSection(screenshots); section != "" {
		parts = append(parts, section)
	}

	base := sanitizeNotes(keptDescription)
	if base != "" {
		parts = append(parts, "[spoiler=Notes]"+base+"[/spoiler]")
	}

	return normalize(strings.Join(parts, "\n\n")), nil
}

func buildScreenshotSection(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		imgURL := strings.TrimSpace(image.ImgURL)
		if imgURL == "" {
			imgURL = strings.TrimSpace(image.RawURL)
		}
		rawURL := strings.TrimSpace(image.RawURL)
		if rawURL == "" {
			rawURL = imgURL
		}
		if imgURL == "" {
			continue
		}
		if rawURL != "" {
			parts = append(parts, "[url="+rawURL+"][img=250]"+imgURL+"[/img][/url]")
			continue
		}
		parts = append(parts, "[img=250]"+imgURL+"[/img]")
	}
	return strings.Join(parts, "")
}

func normalize(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.TrimSpace(mtvCollapseLinesPattern.ReplaceAllString(trimmed, "\n\n"))
}

func sanitizeNotes(value string) string {
	cleaned := mtvQuoteTagPattern.ReplaceAllString(value, "")
	cleaned = mtvNFOBlockPattern.ReplaceAllString(cleaned, "")
	cleaned = mtvURLImagePattern.ReplaceAllString(cleaned, "")
	cleaned = mtvImagePattern.ReplaceAllString(cleaned, "")
	cleaned = mtvSignaturePattern.ReplaceAllString(cleaned, "")
	cleaned = mtvEmptyAlignPattern.ReplaceAllString(cleaned, "")
	return normalize(cleaned)
}

func shouldIncludeTonemappedHeader(meta api.UploadSubject, appConfig config.Config, screenshots []api.ScreenshotImage) bool {
	if !appConfig.ScreenshotHandling.ToneMap {
		return false
	}
	if len(screenshots) == 0 {
		return false
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	return strings.Contains(hdr, "HDR") || strings.Contains(hdr, "DV")
}
