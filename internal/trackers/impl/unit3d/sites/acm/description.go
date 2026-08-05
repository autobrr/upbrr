// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var acmSceneNFOPattern = regexp.MustCompile(`(?is)\[center\]\[spoiler=Scene NFO:\].*?\[/center\]`)

func buildACMDescription(
	ctx context.Context,
	meta api.UploadSubject,
	appConfig config.Config,
	trackerConfig config.TrackerConfig,
	logger api.Logger,
	keptDescription string,
	menuImages []api.ScreenshotImage,
	screenshots []api.ScreenshotImage,
) (string, error) {
	base := acmSceneNFOPattern.ReplaceAllString(strings.TrimSpace(keptDescription), "")
	base = strings.ReplaceAll(base, "\r\n", "\n")
	base = strings.ReplaceAll(base, "[pre]", "[code]")
	base = strings.ReplaceAll(base, "[/pre]", "[/code]")
	base = strings.ReplaceAll(base, "[hide", "[spoiler")
	base = strings.ReplaceAll(base, "[/hide]", "[/spoiler]")
	base = convertACMComparisonToCollapse(base, 1000)
	base = strings.ReplaceAll(base, "[img]", "[img=300]")
	base = descriptionunit3d.AppendDVDVOBMediaInfoBlock(base, api.NewDescriptionSubject(meta))

	cfg := appConfig
	if cfg.Description.ThumbnailSize <= 0 {
		cfg.Description.ThumbnailSize = 350
	}
	if len(screenshots) > 0 {
		cfg.Description.ScreensPerRow = strconv.Itoa(len(screenshots))
	} else if strings.TrimSpace(cfg.Description.ScreensPerRow) == "" {
		cfg.Description.ScreensPerRow = "1"
	}

	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && strings.TrimSpace(meta.ServiceLongName) != "" {
		header := fmt.Sprintf(
			"[center][b][color=#ff00ff][size=18]This release is sourced from %s and is not transcoded, just remuxed from the direct %s stream[/size][/color][/b][/center]",
			strings.TrimSpace(meta.ServiceLongName),
			strings.TrimSpace(meta.ServiceLongName),
		)
		base = strings.TrimSpace(strings.Join([]string{header, base}, "\n"))
	}

	value, err := descriptionunit3d.BuildDescription(
		ctx,
		api.NewDescriptionSubject(meta),
		cfg,
		trackerConfig,
		logger,
		base,
		menuImages,
		screenshots,
	)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return value, nil
}

// convertACMComparisonToCollapse rewrites UNIT3D comparison blocks into ACM
// spoiler markup while leaving malformed blocks unchanged when no image URLs
// can be paired with the source labels.
func convertACMComparisonToCollapse(value string, maxWidth int) string {
	re := regexp.MustCompile(`(?is)\[comparison=[\s\S]*?\[/comparison\]`)
	return re.ReplaceAllStringFunc(value, func(block string) string {
		parts := strings.SplitN(block, "]", 2)
		if len(parts) < 2 {
			return block
		}
		sources := strings.Split(strings.ReplaceAll(strings.TrimPrefix(parts[0], "[comparison="), " ", ""), ",")
		if len(sources) == 0 {
			return block
		}
		imgSize := min(maxWidth/len(sources), 350)
		body := strings.TrimSuffix(parts[1], "[/comparison]")
		fields := strings.Fields(strings.ReplaceAll(body, ",", " "))
		images := make([]string, 0, len(fields))
		for _, field := range fields {
			trimmed := strings.TrimSpace(field)
			parsed, err := url.Parse(trimmed)
			if err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
				images = append(images, trimmed)
			}
		}
		if len(images) == 0 {
			return block
		}
		lines := make([]string, 0, len(images)/len(sources)+1)
		row := make([]string, 0, len(sources))
		for _, image := range images {
			row = append(row, fmt.Sprintf("[url=%s][img=%d]%s[/img][/url]", image, imgSize, image))
			if len(row) == len(sources) {
				lines = append(lines, strings.Join(row, ""))
				row = row[:0]
			}
		}
		if len(row) > 0 {
			lines = append(lines, strings.Join(row, ""))
		}
		return fmt.Sprintf(
			"[spoiler=%s][center]%s[/center]\n%s[/spoiler]",
			strings.Join(sources, " vs "),
			strings.Join(sources, " | "),
			strings.Join(lines, "\n"),
		)
	})
}
