// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	hdbImgSizedPattern = regexp.MustCompile(`(?i)\[img=\d+\]`)
	hdbSizePattern     = regexp.MustCompile(`(?i)\[/size\]|\[size=\d+\]`)
	hdbSpoilerPattern  = regexp.MustCompile(`(?i)\[spoiler([^\]]*)\]`)
	hdbCompPattern     = regexp.MustCompile(`(?i)\[comparison=([^\]]+)\]([\s\S]*?)\[/comparison\]`)
	hdbImgURLPattern   = regexp.MustCompile(`(?i)(https?://[^\s\]]+\.(?:png|jpg|jpeg|webp))`)
	hdbURLImgPattern   = regexp.MustCompile(`(?is)\[url=(https?://[^\]]+)\]\s*\[img(?:[^\]]*)?\](https?://[^\[]+?)\[/img\]\s*\[/url\]`)
	hdbImgBlockPattern = regexp.MustCompile(`(?is)\[img(?:[^\]]*)?\](https?://[^\[]+?)\[/img\]`)
	hdbUARegex         = regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`)
	hdbEmptyWrapperTag = regexp.MustCompile(`(?is)\[(?:center|align=center)\]\s*\[/(?:center|align)\]`)
)

func BuildDescription(ctx context.Context, meta api.PreparedMetadata, appConfig config.Config, keptDescription string, screenshots []api.ScreenshotImage) (string, error) {
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	parts := make([]string, 0, 8)

	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && strings.TrimSpace(meta.ServiceLongName) != "" && strings.TrimSpace(keptDescription) == "" {
		parts = append(parts, fmt.Sprintf("[center][quote]This release is sourced from %s[/quote][/center]", strings.TrimSpace(meta.ServiceLongName)))
	}

	discSection, err := buildDiscSection(meta, appConfig.MainSettings.DBPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(discSection) != "" {
		parts = append(parts, discSection)
	}

	if transformed := transformBaseDescription(keptDescription, len(screenshots) > 0); strings.TrimSpace(transformed) != "" {
		parts = append(parts, transformed)
	}

	if section := buildScreenshotSection(screenshots, meta.Options.Screens); section != "" {
		parts = append(parts, section)
	}

	if signature := strings.TrimSpace(appConfig.Description.CustomSignature); signature != "" {
		parts = append(parts, signature)
	}

	return normalize(strings.Join(parts, "\n\n")), nil
}

func buildDiscSection(meta api.PreparedMetadata, dbPath string) (string, error) {
	if strings.TrimSpace(meta.DVDVOBMediaInfoText) != "" {
		return "[quote=VOB MediaInfo]" + strings.TrimSpace(meta.DVDVOBMediaInfoText) + "[/quote]", nil
	}

	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "", nil
	}

	if strings.TrimSpace(dbPath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("description: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("description: %w", err)
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta))
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("description: HDB read MediaInfo file: %w", err)
	}
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return "", nil
	}
	return "[quote]" + trimmed + "[/quote]", nil
}

func transformBaseDescription(value string, stripScreens bool) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	transformed := trimmed
	transformed = hdbUARegex.ReplaceAllString(transformed, "")
	if stripScreens {
		transformed = hdbURLImgPattern.ReplaceAllString(transformed, "")
		transformed = hdbImgBlockPattern.ReplaceAllString(transformed, "")
		transformed = hdbEmptyWrapperTag.ReplaceAllString(transformed, "")
	}
	transformed = strings.ReplaceAll(transformed, "[code]", "[font=monospace]")
	transformed = strings.ReplaceAll(transformed, "[/code]", "[/font]")
	for _, tag := range []string{"user", "left", "right", "sup", "sub", "alert", "note", "hr", "ul", "ol"} {
		transformed = strings.ReplaceAll(transformed, "["+tag+"]", "")
		transformed = strings.ReplaceAll(transformed, "[/"+tag+"]", "")
	}
	transformed = strings.ReplaceAll(transformed, "[align=left]", "")
	transformed = strings.ReplaceAll(transformed, "[align=right]", "")
	transformed = strings.ReplaceAll(transformed, "[/align]", "")
	transformed = strings.ReplaceAll(transformed, "[h1]", "[u][b]")
	transformed = strings.ReplaceAll(transformed, "[/h1]", "[/b][/u]")
	transformed = strings.ReplaceAll(transformed, "[h2]", "[u][b]")
	transformed = strings.ReplaceAll(transformed, "[/h2]", "[/b][/u]")
	transformed = strings.ReplaceAll(transformed, "[h3]", "[u][b]")
	transformed = strings.ReplaceAll(transformed, "[/h3]", "[/b][/u]")
	transformed = strings.ReplaceAll(transformed, "[*]", "* ")
	transformed = hdbSpoilerPattern.ReplaceAllString(transformed, "[hide$1]")
	transformed = strings.ReplaceAll(transformed, "[/spoiler]", "[/hide]")
	transformed = convertComparisonToCentered(transformed, 1000)
	transformed = hdbImgSizedPattern.ReplaceAllString(transformed, "[img]")
	transformed = hdbSizePattern.ReplaceAllString(transformed, "")
	return normalize(transformed)
}

func convertComparisonToCentered(value string, maxWidth int) string {
	return hdbCompPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := hdbCompPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		sourcePart := strings.TrimSpace(parts[1])
		imagePart := strings.TrimSpace(parts[2])
		sources := splitCSV(sourcePart)
		if len(sources) == 0 {
			return match
		}
		images := hdbImgURLPattern.FindAllString(imagePart, -1)
		if len(images) == 0 {
			return match
		}
		screensPerLine := len(sources)
		if screensPerLine <= 0 {
			return match
		}
		imgSize := maxWidth / screensPerLine
		if imgSize > 350 {
			imgSize = 350
		}
		rows := make([]string, 0)
		line := make([]string, 0, screensPerLine)
		for _, img := range images {
			u := strings.TrimSpace(img)
			if u == "" {
				continue
			}
			line = append(line, "[url="+u+"][img="+strconv.Itoa(imgSize)+"]"+u+"[/img][/url]")
			if len(line) == screensPerLine {
				rows = append(rows, strings.Join(line, ""))
				line = line[:0]
			}
		}
		return "[center]" + strings.Join(sources, " | ") + "\n" + strings.Join(rows, "\n") + "[/center]"
	})
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func buildScreenshotSection(images []api.ScreenshotImage, maxCount int) string {
	if len(images) == 0 {
		return ""
	}
	limit := len(images)
	if maxCount > 0 && maxCount < limit {
		limit = maxCount
	}
	parts := make([]string, 0, limit+2)
	parts = append(parts, "[center]")
	for _, image := range images[:limit] {
		imgURL := strings.TrimSpace(image.ImgURL)
		if imgURL == "" {
			imgURL = strings.TrimSpace(image.RawURL)
		}
		webURL := strings.TrimSpace(image.WebURL)
		if imgURL == "" {
			continue
		}
		if webURL != "" {
			parts = append(parts, "[url="+webURL+"][img]"+imgURL+"[/img][/url]")
			continue
		}
		parts = append(parts, "[img]"+imgURL+"[/img]")
	}
	parts = append(parts, "[/center]")
	return strings.Join(parts, "")
}

func normalize(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	re := regexp.MustCompile(`\n{3,}`)
	return strings.TrimSpace(re.ReplaceAllString(trimmed, "\n\n"))
}
