// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(defaults config.DescriptionSettingsConfig, meta api.UploadSubject, assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	return composeDescription(defaults, meta, assets, buildDiscSection(meta))
}

func composeDescription(defaults config.DescriptionSettingsConfig, meta api.UploadSubject, assets trackers.DescriptionAssets, discSection string) string {
	base, discReports := description.SplitLeadingDiscReports(strings.TrimSpace(assets.Description))
	base, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(base)
	audioAnalysis = description.RelabelAnalysis(audioAnalysis, defaults.AudioPlotsLabel, defaults.HDRPlotsLabel)

	cleaned := CleanDescription(base, BBCodeOptions{
		Framestor: hasGroup(meta.Tag, "framestor"),
		Flux:      hasGroup(meta.Tag, "flux"),
	})

	descriptionBody := strings.TrimSpace(cleaned.Description)
	maskedDescription, comparisonBlocks := maskBHDComparisonBlocks(descriptionBody)
	maskedDescription = stripUASignature(maskedDescription)
	maskedDescription = strings.ReplaceAll(maskedDescription, "[img]", "[img width=300]")
	descriptionBody = restoreBHDComparisonBlocks(maskedDescription, comparisonBlocks)

	images := assets.Screenshots
	if len(images) == 0 {
		images = screenshotsFromReport(cleaned.Images)
	}
	screenshotLimit := len(images)
	if meta.Options.Screens > 0 {
		screenshotLimit = meta.Options.Screens
	}

	var parts []description.Section
	appendPart := func(id, text string) { parts = description.AppendSection(parts, id, text) }
	if discSection != "" {
		if label := description.MarkupLabel(defaults.VOBReportLabel); label != "" && strings.HasPrefix(discSection, "[spoiler=VOB MediaInfo]") {
			discSection = "[spoiler=" + label + "]" + strings.TrimPrefix(discSection, "[spoiler=VOB MediaInfo]")
		}
		appendPart("mediainfo", discSection)
	}
	if descriptionBody != "" {
		appendPart("kept", descriptionBody)
	}
	if menus := buildDiscScreenshotSection(meta, assets.MenuImages, len(assets.MenuImages)); menus != "" {
		appendPart("disc_menu", "[b]"+description.Heading(defaults.Labels, "bhd.menu", "Disc Menus")+"[/b]\n"+menus)
	}
	if audioAnalysis != "" {
		appendPart("audio_analysis", strings.ReplaceAll(audioAnalysis, "[img]", "[img width=350]"))
	}
	if discReports != "" {
		appendPart("screenshots", CleanDescription(discReports, BBCodeOptions{}).Description)
	}
	if screenshots := buildScreenshotSection(images, screenshotLimit); screenshots != "" {
		appendPart("screenshots", screenshots)
	}
	appendPart("", `[align=right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/align]`)
	return strings.TrimSpace(description.FormatSections(parts, defaults))
}

func buildDiscSection(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "DVD":
		media := metautil.FirstNonEmptyTrimmed(trackers.ReadDVDVOBMediaInfo(meta), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
		if media == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=VOB MediaInfo][code]%s[/code][/spoiler]", media)
	case "BDMV":
		// The primary report goes in the dedicated technical attachment.
		// Retain other reports by identity, even if their text is identical.
		var reports []string
		for _, disc := range meta.Disc.Items {
			for _, report := range disc.Reports {
				if disc.ID == meta.Disc.PrimaryDiscID && report.Playlist.ID == meta.Disc.PrimaryReportID {
					continue
				}
				if summary := strings.TrimSpace(report.Summary); summary != "" {
					heading := safeDiscLabel(disc.Name)
					if file := safeDiscLabel(report.Playlist.File); file != "" {
						heading = strings.TrimSpace(heading + " — " + file)
					}
					reports = append(reports, strings.TrimSpace(heading+"\n"+summary))
				}
			}
		}
		if len(reports) == 0 {
			return ""
		}
		return fmt.Sprintf("[spoiler=BDINFO][code]%s[/code][/spoiler]", strings.Join(reports, "\n\n"))
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
		imgURL := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(image.ImgURL), strings.TrimSpace(image.RawURL))
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

func buildDiscScreenshotSection(meta api.UploadSubject, images []api.ScreenshotImage, limit int) string {
	if len(meta.Disc.Items) < 2 {
		return buildScreenshotSection(images, limit)
	}
	if limit > 0 && limit < len(images) {
		images = images[:limit]
	}
	parts := make([]string, 0, len(meta.Disc.Items)+1)
	used := make(map[int]struct{}, len(images))
	for _, disc := range meta.Disc.Items {
		group := make([]api.ScreenshotImage, 0)
		for index, image := range images {
			if image.DiscID != disc.ID {
				continue
			}
			group = append(group, image)
			used[index] = struct{}{}
		}
		if section := buildScreenshotSection(group, len(group)); section != "" {
			parts = append(parts, "[b]"+safeDiscLabel(disc.Name)+"[/b]\n"+section)
		}
	}
	unscoped := make([]api.ScreenshotImage, 0, len(images)-len(used))
	for index, image := range images {
		if _, ok := used[index]; !ok {
			unscoped = append(unscoped, image)
		}
	}
	if section := buildScreenshotSection(unscoped, len(unscoped)); section != "" {
		parts = append(parts, section)
	}
	return strings.Join(parts, "\n\n")
}

func safeDiscLabel(value string) string {
	value = strings.ReplaceAll(value, "[", "(")
	value = strings.ReplaceAll(value, "]", ")")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
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
	trimmed := trackers.StripDescriptionSignatures(value)
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

func hasGroup(tag string, name string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(tag, "-")))
	return trimmed == strings.ToLower(strings.TrimSpace(name))
}

func descriptionDefaultsLayout() []trackers.DescriptionLayoutItem {
	return []trackers.DescriptionLayoutItem{
		{
			ID:         "mediainfo",
			Label:      "VOB MediaInfo or BDInfo",
			CanCompact: true,
			Fields:     []string{"VOBReportLabel"},
		},
		{
			ID:         "kept",
			Label:      "Imported or custom text",
			CanCompact: true,
		},
		{
			ID:         "disc_menu",
			Label:      "Disc menus",
			CanCompact: true,
			Labels: []trackers.DescriptionLabel{{
				Key:     "bhd.menu",
				Label:   "Disc menu heading",
				Default: "Disc Menus",
			}},
		},
		{
			ID:         "audio_analysis",
			Label:      "Audio and HDR analysis",
			CanCompact: true,
			Fields:     []string{"AudioPlotsLabel", "HDRPlotsLabel"},
		},
		{
			ID:         "screenshots",
			Label:      "Screenshots and disc reports",
			CanCompact: true,
		},
		{Label: "Signature footer"},
	}
}

func renderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	_ config.TrackerConfig,
) (string, error) {
	return buildDescription(defaults, sample.Subject, sample.Assets), nil
}
