// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

var mtvQuoteTagPattern = regexp.MustCompile(`(?i)\[/?quote\]`)
var mtvCollapseLinesPattern = regexp.MustCompile(`\n{3,}`)
var mtvNFOBlockPattern = regexp.MustCompile(`(?is)(?:\[(?:center|align=center)\]\s*)?\[(?:spoiler|hide)=(?:Scene|FraMeSToR) NFO:\](?:\[(?:code|pre)\])?.*?(?:\[/(?:code|pre)\])?\[/(?:spoiler|hide)\](?:\s*\[/(?:center|align)\])?`)
var mtvURLImagePattern = regexp.MustCompile(`(?is)\[url=[^\]]+\]\s*\[img(?:[^\]]*)?\][^\[]+\[/img\]\s*\[/url\]`)
var mtvImagePattern = regexp.MustCompile(`(?is)\[img(?:[^\]]*)?\][^\[]+\[/img\]`)
var mtvSignaturePattern = regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`)
var mtvEmptyAlignPattern = regexp.MustCompile(`(?is)\[(?:center|right|left|align=(?:center|right|left))\]\s*\[/(?:center|right|left|align)\]`)

func BuildDescription(ctx context.Context, meta api.PreparedMetadata, appConfig config.Config, keptDescription string, screenshots []api.ScreenshotImage) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
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

func buildMediaInfoBlock(meta api.PreparedMetadata, dbPath string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdInfoPath, err := resolveBDInfoPath(meta, dbPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(bdInfoPath) != "" {
			text, err := os.ReadFile(bdInfoPath)
			if err != nil {
				if !os.IsNotExist(err) {
					return "", fmt.Errorf("description: MTV read BDInfo file: %w", err)
				}
			} else {
				trimmed := strings.TrimSpace(string(text))
				if trimmed != "" {
					return "[mediainfo]" + trimmed + "[/mediainfo]", nil
				}
			}
		}
	}

	if strings.TrimSpace(meta.MediaInfoTextPath) != "" {
		text, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("description: MTV read MediaInfo file: %w", err)
			}
		} else {
			trimmed := strings.TrimSpace(string(text))
			if trimmed != "" {
				return "[mediainfo]" + trimmed + "[/mediainfo]", nil
			}
		}
	}

	return "", nil
}

func resolveBDInfoPath(meta api.PreparedMetadata, dbPath string) (string, error) {
	if strings.TrimSpace(dbPath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", err
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta))
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("description: MTV stat BDInfo summary: %w", err)
	}
	return path, nil
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

func shouldIncludeTonemappedHeader(meta api.PreparedMetadata, appConfig config.Config, screenshots []api.ScreenshotImage) bool {
	if !appConfig.ScreenshotHandling.ToneMap {
		return false
	}
	if len(screenshots) == 0 {
		return false
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	return strings.Contains(hdr, "HDR") || strings.Contains(hdr, "DV")
}
