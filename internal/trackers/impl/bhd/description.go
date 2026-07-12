// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.PreparedMetadata, cfg config.Config, assets trackers.DescriptionAssets) string {
	base := strings.TrimSpace(assets.Description)
	if assets.Final {
		return base
	}

	cleaned := bbcode.CleanBHDDescription(base, bbcode.BHDOptions{
		Framestor: hasGroup(meta.Tag, "framestor"),
		Flux:      hasGroup(meta.Tag, "flux"),
	})

	descriptionBody := strings.TrimSpace(cleaned.Description)
	descriptionBody = stripUASignature(descriptionBody)
	descriptionBody = strings.ReplaceAll(descriptionBody, "[img]", "[img width=300]")

	images := assets.Screenshots
	if len(images) == 0 {
		images = screenshotsFromReport(cleaned.Images)
	}

	parts := make([]string, 0, 5)
	if discSection := buildDiscSection(meta, cfg.MainSettings.DBPath); discSection != "" {
		parts = append(parts, discSection)
	}
	if descriptionBody != "" {
		parts = append(parts, descriptionBody)
	}
	if screenshots := buildScreenshotSection(images, maxInt(1, meta.Options.Screens)); screenshots != "" {
		parts = append(parts, screenshots)
	}
	parts = append(parts, `[align=right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/align]`)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildDiscSection(meta api.PreparedMetadata, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "DVD":
		media := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
		if media == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=VOB MediaInfo][code]%s[/code][/spoiler]", media)
	case "BDMV":
		text := readBDInfoNoErr(dbPath, meta)
		if text == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=BDINFO][code]%s[/code][/spoiler]", text)
	default:
		return ""
	}
}

func buildScreenshotSection(images []api.ScreenshotImage, limit int) string {
	if len(images) == 0 || limit <= 0 {
		return ""
	}

	var section strings.Builder
	section.WriteString("[align=center]")
	count := 0
	for _, image := range images {
		if count >= limit {
			break
		}
		imgURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.RawURL), strings.TrimSpace(image.ImgURL))
		webURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.WebURL), strings.TrimSpace(image.RawURL), imgURL)
		if imgURL == "" || webURL == "" {
			continue
		}
		if count > 0 {
			if count%2 == 0 {
				section.WriteString("\n\n")
			} else {
				section.WriteByte(' ')
			}
		}
		line := fmt.Sprintf("[url=%s][img width=350]%s[/img][/url]", webURL, imgURL)
		section.WriteString(line)
		count++
	}
	if count == 0 {
		return ""
	}
	section.WriteString("[/align]")
	return section.String()
}

func screenshotsFromReport(images []bbcode.Image) []api.ScreenshotImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]api.ScreenshotImage, 0, len(images))
	for idx, image := range images {
		imgURL := strings.TrimSpace(image.ImgURL)
		rawURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.RawURL), imgURL)
		webURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.WebURL), rawURL, imgURL)
		if imgURL == "" && rawURL == "" {
			continue
		}
		out = append(out, api.ScreenshotImage{
			Index:  idx,
			Host:   strings.TrimSpace(image.Host),
			ImgURL: metautil.FirstNonEmptyTrimmed(imgURL, rawURL),
			RawURL: rawURL,
			WebURL: webURL,
		})
	}
	return out
}

func stripUASignature(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	signatures := []string{
		`[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`,
		`[align=right][url=https://github.com/autobrr/upbrr]upbrr[/url][/align]`,
		`[align=right][url=https://github.com/autobrr/upbrr]Created by upbrr[/url][/align]`,
		`[align=right]Created by upbrr[/align]`,
		`[align=right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/align]`,
		`[right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/right]`,
		`[right][url=https://github.com/autobrr/upbrr]upbrr[/url][/right]`,
		`[right][url=https://github.com/autobrr/upbrr]Created by upbrr[/url][/right]`,
		`[right]Created by upbrr[/right]`,
		`[right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/right]`,
	}
	for _, signature := range signatures {
		trimmed = strings.TrimSpace(strings.ReplaceAll(trimmed, signature, ""))
	}
	return trimmed
}

func readTextFileNoErr(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return ""
	}
	return string(payload)
}

func readBDInfoNoErr(dbPath string, meta api.PreparedMetadata) string {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return ""
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return ""
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return ""
	}
	return readTextFileNoErr(paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta)))
}

func hasGroup(tag string, name string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(tag, "-")))
	return trimmed == strings.ToLower(strings.TrimSpace(name))
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
