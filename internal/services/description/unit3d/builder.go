// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

const uaSignatureText = "Created by upbrr"
const uaSignatureLink = "https://github.com/autobrr/upbrr"
const dvdVOBMediaInfoHeader = "[spoiler=VOB MediaInfo][code]"
const dvdVOBMediaInfoFooter = "[/code][/spoiler]"

var collapseNewlines = regexp.MustCompile(`\n{3,}`)
var bbcodeImageTag = regexp.MustCompile(`(?is)\[img(?:=[^\]]*)?\](.*?)\[/img\]`)
var comparisonTag = regexp.MustCompile(`(?is)\[comparison=[\s\S]*?\[/comparison\]`)
var comparisonImageURL = regexp.MustCompile(`(?i)(https?://.*\.(?:png|jpg))`)
var unit3DAlignBlockTag = regexp.MustCompile(`(?is)\[align=(center|left|right)\](.*?)\[/align\]`)
var unit3DWrapperBlockTag = regexp.MustCompile(`(?is)\[(center|align=(?:center|left|right))\](.*?)\[/(center|align)\]`)
var unit3DWidthImageTag = regexp.MustCompile(`(?i)\[img\s+width=(\d+)\]`)
var unit3DUASignatureTag = regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`)
var unit3DNFOBlockTag = regexp.MustCompile(`(?is)\[(?:center|align=center)\]\s*\[spoiler=(?:Scene|FraMeSToR) NFO:\]\[code\].*?\[/code\]\[/spoiler\]\s*\[/(?:center|align)\]`)

func BuildDescription(ctx context.Context, meta api.PreparedMetadata, appConfig config.Config, _ config.TrackerConfig, logger api.Logger, keptDescription string, menuImages []api.ScreenshotImage, screenshots []api.ScreenshotImage) (string, error) {
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if logger == nil {
		logger = api.NopLogger{}
	}

	if len(screenshots) > 0 {
		meta.DescriptionTemplate = stripUnit3DScreenshotBlocks(meta.DescriptionTemplate)
		keptDescription = stripUnit3DScreenshotBlocks(keptDescription)
	}

	meta.DescriptionTemplate = stripUnit3DNFOBlocks(meta.DescriptionTemplate)
	keptDescription = stripUnit3DNFOBlocks(keptDescription)

	parts := make([]string, 0, 10)
	seenParts := make(map[string]struct{}, 4)
	appendUniquePart := func(value string, logLabel string) {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			return
		}
		key := strings.ToLower(normalized)
		if _, ok := seenParts[key]; ok {
			logger.Debugf("trackers: unit3d desc skipped duplicate part=%s", logLabel)
			return
		}
		seenParts[key] = struct{}{}
		parts = append(parts, normalized)
	}
	if template := stripUnit3DSignature(strings.TrimSpace(meta.DescriptionTemplate)); template != "" {
		appendUniquePart(template, "template")
		logger.Tracef("trackers: unit3d desc part=template len=%d", len(template))
	}
	if kept := stripUnit3DSignature(strings.TrimSpace(keptDescription)); kept != "" {
		appendUniquePart(kept, "kept")
		logger.Tracef("trackers: unit3d desc part=kept len=%d imgs=%d", len(kept), countBBCodeImages(kept))
	}
	if header := strings.TrimSpace(appConfig.Description.CustomDescriptionHeader); header != "" {
		appendUniquePart(header, "custom_header")
		logger.Tracef("trackers: unit3d desc part=custom_header len=%d", len(header))
	}

	logoURL, logoSize := ResolveLogo(meta, appConfig)
	if logoURL != "" {
		appendUniquePart(fmt.Sprintf("[center][img=%d]%s[/img][/center]", logoSize, logoURL), "logo")
		logger.Tracef("trackers: unit3d desc part=logo size=%d", logoSize)
	}

	if episodeOverview := EpisodeOverviewBlock(meta, appConfig); episodeOverview != "" {
		appendUniquePart(episodeOverview, "episode_overview")
		logger.Tracef("trackers: unit3d desc part=episode_overview len=%d", len(episodeOverview))
	}

	if bluray := BlurayBlock(meta, appConfig); bluray != "" {
		appendUniquePart(bluray, "bluray")
		logger.Tracef("trackers: unit3d desc part=bluray")
	}

	if vobMediaInfo := DVDVOBMediaInfoBlock(meta); vobMediaInfo != "" {
		appendUniquePart(vobMediaInfo, "dvd_vob_mediainfo")
		logger.Tracef("trackers: unit3d desc part=dvd_vob_mediainfo")
	}

	if tonemapHeader := strings.TrimSpace(appConfig.Description.TonemappedHeader); tonemapHeader != "" && ShouldIncludeTonemappedHeader(meta, appConfig, screenshots) {
		appendUniquePart(tonemapHeader, "tonemap_header")
		logger.Tracef("trackers: unit3d desc part=tonemap_header len=%d", len(tonemapHeader))
	}

	if len(menuImages) > 0 {
		menuHeader := strings.TrimSpace(appConfig.Description.DiscMenuHeader)
		if menuHeader != "" {
			appendUniquePart(menuHeader, "menu_header")
		}
		menuSection := buildScreenshotSection(menuImages, appConfig.Description.ThumbnailSize, parseScreensPerRow(appConfig.Description.ScreensPerRow))
		if menuSection != "" {
			appendUniquePart(menuSection, "menu_images")
			logger.Tracef("trackers: unit3d desc part=menu_images count=%d", len(menuImages))
		}
	}

	logger.Tracef("trackers: unit3d desc part=mediainfo skipped (sent via API)")

	filteredScreenshots := filterScreenshotDuplicates(screenshots, keptDescription, menuImages)
	logger.Tracef("trackers: unit3d desc screenshots total=%d filtered=%d", len(screenshots), len(filteredScreenshots))
	screenshotHeader := strings.TrimSpace(appConfig.Description.ScreenshotHeader)
	screenshotSection := buildScreenshotSection(filteredScreenshots, appConfig.Description.ThumbnailSize, parseScreensPerRow(appConfig.Description.ScreensPerRow))
	if screenshotSection != "" && screenshotHeader != "" {
		appendUniquePart(screenshotHeader, "screenshot_header")
		logger.Tracef("trackers: unit3d desc part=screenshot_header len=%d", len(screenshotHeader))
	}
	if screenshotSection != "" {
		appendUniquePart(screenshotSection, "screenshots")
		logger.Tracef("trackers: unit3d desc part=screenshots count=%d", countBBCodeImages(screenshotSection))
	}
	if customSignature := strings.TrimSpace(appConfig.Description.CustomSignature); customSignature != "" {
		appendUniquePart(customSignature, "custom_signature")
		logger.Tracef("trackers: unit3d desc part=custom_signature len=%d", len(customSignature))
	} else {
		link, text := UppbrrSignatureLink()
		appendUniquePart(fmt.Sprintf("[right][url=%s][size=4]%s[/size][/url][/right]", link, text), "signature")
		logger.Tracef("trackers: unit3d desc part=signature")
	}

	description := normalizeDescription(strings.Join(parts, "\n\n"))
	description = finalizeUnit3DDescription(description)
	if strings.TrimSpace(description) == "" {
		return "", nil
	}

	if meta.Options.Debug {
		SaveDescriptionDebug(meta, "unit3d", appConfig.MainSettings.DBPath, description, logger)
	}

	return description, nil
}

func buildScreenshotSection(images []api.ScreenshotImage, thumbnailSize int, screensPerRow int) string {
	if thumbnailSize <= 0 {
		thumbnailSize = 350
	}
	if screensPerRow <= 0 {
		screensPerRow = 2
	}
	rows := make([]string, 0)
	rowParts := make([]string, 0, screensPerRow)
	for _, image := range images {
		imgTag := buildScreenshotTag(image, thumbnailSize)
		if imgTag == "" {
			continue
		}
		rowParts = append(rowParts, imgTag)
		if len(rowParts) == screensPerRow {
			rows = append(rows, strings.Join(rowParts, " "))
			rowParts = rowParts[:0]
		}
	}
	if len(rowParts) > 0 {
		rows = append(rows, strings.Join(rowParts, " "))
	}
	if len(rows) == 0 {
		return ""
	}
	return "[center]\n" + strings.Join(rows, "\n") + "\n[/center]"
}

func buildScreenshotTag(image api.ScreenshotImage, thumbnailSize int) string {
	webURL := strings.TrimSpace(image.WebURL)
	rawURL := strings.TrimSpace(image.RawURL)
	if webURL != "" && rawURL != "" {
		return fmt.Sprintf("[url=%s][img=%d]%s[/img][/url]", webURL, thumbnailSize, rawURL)
	}
	url := pickScreenshotURL(image)
	if url == "" {
		return ""
	}
	return fmt.Sprintf("[img=%d]%s[/img]", thumbnailSize, url)
}

func parseScreensPerRow(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 2
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed <= 0 {
		return 2
	}
	return parsed
}

func filterScreenshotDuplicates(images []api.ScreenshotImage, keptDescription string, menuImages []api.ScreenshotImage) []api.ScreenshotImage {
	if len(images) == 0 {
		return images
	}
	seen := extractBBCodeImageURLs(keptDescription)
	if seen == nil {
		seen = make(map[string]struct{})
	}
	for _, img := range menuImages {
		u := pickScreenshotURL(img)
		if u != "" {
			seen[u] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return images
	}
	filtered := make([]api.ScreenshotImage, 0, len(images))
	for _, image := range images {
		url := pickScreenshotURL(image)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		filtered = append(filtered, image)
	}
	return filtered
}

func extractBBCodeImageURLs(value string) map[string]struct{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	matches := bbcodeImageTag.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		return nil
	}
	results := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		url := strings.TrimSpace(match[1])
		if url == "" {
			continue
		}
		results[url] = struct{}{}
	}
	return results
}

func countBBCodeImages(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	return len(bbcodeImageTag.FindAllStringSubmatch(trimmed, -1))
}

func pickScreenshotURL(image api.ScreenshotImage) string {
	url := strings.TrimSpace(image.ImgURL)
	if url == "" {
		url = strings.TrimSpace(image.RawURL)
	}
	if url == "" {
		url = strings.TrimSpace(image.WebURL)
	}
	return url
}

func stripUnit3DSignature(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.TrimSpace(unit3DUASignatureTag.ReplaceAllString(trimmed, ""))
}

func stripUnit3DScreenshotBlocks(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	cleaned := unit3DWrapperBlockTag.ReplaceAllStringFunc(trimmed, func(match string) string {
		parts := unit3DWrapperBlockTag.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		if !isUnit3DScreenshotBlock(parts[2]) {
			return match
		}
		return ""
	})
	return normalizeDescription(cleaned)
}

func stripUnit3DNFOBlocks(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return normalizeDescription(unit3DNFOBlockTag.ReplaceAllString(trimmed, ""))
}

func isUnit3DScreenshotBlock(value string) bool {
	images := extractUnit3DBlockImages(value)
	if len(images) == 0 || isPosterLikeTopBlock(images) {
		return false
	}
	withoutLinkedImages := unit3dURLImgPattern.ReplaceAllString(value, "")
	withoutImages := unit3dImgPattern.ReplaceAllString(withoutLinkedImages, "")
	return strings.TrimSpace(withoutImages) == ""
}

func extractUnit3DBlockImages(value string) []Image {
	report := extractUnit3DImages(value)
	images := make([]Image, 0, len(report))
	for _, image := range report {
		images = append(images, Image{
			ImgURL: image.ImgURL,
			RawURL: image.RawURL,
			WebURL: image.WebURL,
			Host:   image.Host,
		})
	}
	return images
}

func ShouldIncludeTonemappedHeader(meta api.PreparedMetadata, appConfig config.Config, screenshots []api.ScreenshotImage) bool {
	if !appConfig.ScreenshotHandling.ToneMap {
		return false
	}
	if len(screenshots) == 0 {
		return false
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	return strings.Contains(hdr, "HDR") || strings.Contains(hdr, "DV")
}

func ResolveLogo(meta api.PreparedMetadata, appConfig config.Config) (string, int) {
	if !appConfig.Description.AddLogo {
		return "", 0
	}
	logoURL := ""
	if meta.ExternalMetadata.TMDB != nil {
		logoURL = strings.TrimSpace(meta.ExternalMetadata.TMDB.Logo)
		if logoURL == "" {
			logoURL = strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo)
		}
	}
	if logoURL == "" {
		return "", 0
	}
	size := appConfig.Description.LogoSize
	if size <= 0 {
		size = 300
	}
	return logoURL, size
}

func EpisodeOverviewBlock(meta api.PreparedMetadata, appConfig config.Config) string {
	if !appConfig.Description.EpisodeOverview {
		return ""
	}
	overview := strings.TrimSpace(meta.EpisodeOverview)
	if overview == "" {
		return ""
	}
	return "[center]" + escapeBBCode(overview) + "[/center]"
}

func BlurayBlock(meta api.PreparedMetadata, appConfig config.Config) string {
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return ""
	}
	if meta.ExternalMetadata.Bluray == nil {
		return ""
	}
	candidate := meta.ExternalMetadata.Bluray.SelectedCandidate()
	if candidate == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if appConfig.Description.AddBlurayLink {
		if releaseURL := sanitizeBlurayReleaseURL(candidate.URL); releaseURL != "" {
			parts = append(parts, "[center]"+releaseURL+"[/center]")
		}
	}
	if appConfig.Description.UseBlurayImages && len(candidate.CoverImages) > 0 {
		size := appConfig.Description.BlurayImageSize
		if size <= 0 {
			size = 250
		}
		imageTags := make([]string, 0, len(candidate.CoverImages))
		for _, image := range candidate.CoverImages {
			imageURL := sanitizeDescriptionURL(image.URL)
			if imageURL == "" {
				continue
			}
			imageTags = append(imageTags, fmt.Sprintf("[img=%d]%s[/img]", size, imageURL))
		}
		if len(imageTags) > 0 {
			parts = append(parts, "[center]"+strings.Join(imageTags, " ")+"[/center]")
		}
	}
	return strings.Join(parts, "\n")
}

func sanitizeBlurayReleaseURL(rawURL string) string {
	return sanitizeDescriptionURL(rawURL)
}

func sanitizeDescriptionURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return escapeBBCode(parsed.String())
}

func escapeBBCode(value string) string {
	return strings.NewReplacer("[", "%5B", "]", "%5D").Replace(value)
}

func UppbrrSignatureLink() (string, string) {
	return uaSignatureLink, uaSignatureText
}

func DVDVOBMediaInfoBlock(meta api.PreparedMetadata) string {
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return ""
	}
	vobText := strings.TrimSpace(meta.DVDVOBMediaInfoText)
	if vobText == "" {
		return ""
	}
	return dvdVOBMediaInfoHeader + vobText + dvdVOBMediaInfoFooter
}

func AppendDVDVOBMediaInfoBlock(description string, meta api.PreparedMetadata) string {
	trimmedDescription := strings.TrimSpace(description)
	block := DVDVOBMediaInfoBlock(meta)
	if block == "" {
		return trimmedDescription
	}
	if trimmedDescription == "" {
		return block
	}
	if strings.Contains(trimmedDescription, block) {
		return trimmedDescription
	}
	if strings.Contains(trimmedDescription, dvdVOBMediaInfoHeader) && strings.Contains(trimmedDescription, strings.TrimSpace(meta.DVDVOBMediaInfoText)) {
		return trimmedDescription
	}
	return normalizeDescription(trimmedDescription + "\n\n" + block)
}

func normalizeDescription(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	cleaned := collapseNewlines.ReplaceAllString(trimmed, "\n\n")
	return strings.TrimSpace(cleaned)
}

func finalizeUnit3DDescription(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "[hide", "[spoiler")
	value = strings.ReplaceAll(value, "[/hide]", "[/spoiler]")
	value = unit3DAlignBlockTag.ReplaceAllStringFunc(value, func(match string) string {
		parts := unit3DAlignBlockTag.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		tag := strings.ToLower(strings.TrimSpace(parts[1]))
		if tag == "left" {
			tag = "center"
		}
		return "[" + tag + "]" + parts[2] + "[/" + tag + "]"
	})
	value = unit3DWidthImageTag.ReplaceAllString(value, "[img=$1]")
	for _, tag := range []string{"[user]", "[/user]", "[hr]", "[/hr]", "[ul]", "[/ul]", "[ol]", "[/ol]"} {
		value = strings.ReplaceAll(value, tag, "")
	}
	value = collapseNewlines.ReplaceAllString(value, "\n\n")
	value = convertUnit3DComparisonsToCollapse(value, 1000)
	return normalizeDescription(value)
}

func convertUnit3DComparisonsToCollapse(value string, maxWidth int) string {
	comparisons := comparisonTag.FindAllString(value, -1)
	for _, comp := range comparisons {
		parts := strings.SplitN(comp, "]", 2)
		if len(parts) < 2 {
			continue
		}
		sourcePart := strings.TrimSpace(strings.TrimPrefix(parts[0], "[comparison="))
		sourcePart = strings.ReplaceAll(sourcePart, " ", "")
		sources := strings.Split(sourcePart, ",")
		if len(sources) == 0 {
			continue
		}
		imagePart := strings.ReplaceAll(parts[1], "[/comparison]", "")
		imagePart = strings.ReplaceAll(imagePart, ",", "\n")
		imagePart = strings.ReplaceAll(imagePart, " ", "\n")
		images := comparisonImageURL.FindAllString(imagePart, -1)
		if len(images) == 0 {
			continue
		}
		imgSize := maxWidth / len(sources)
		if imgSize > 350 {
			imgSize = 350
		}
		line := make([]string, 0, len(sources))
		output := make([]string, 0, len(images)/len(sources)+1)
		for _, img := range images {
			img = strings.TrimSpace(img)
			if img == "" {
				continue
			}
			line = append(line, "[url="+img+"][img="+strconv.Itoa(imgSize)+"]"+img+"[/img][/url]")
			if len(line) == len(sources) {
				output = append(output, strings.Join(line, ""))
				line = line[:0]
			}
		}
		if len(line) > 0 {
			output = append(output, strings.Join(line, ""))
		}
		replacement := "[spoiler=" + strings.Join(sources, " vs ") + "][center]" + strings.Join(sources, " | ") + "[/center]\n" + strings.Join(output, "\n") + "[/spoiler]"
		value = strings.Replace(value, comp, replacement, 1)
	}
	return value
}

func SaveDescriptionDebug(meta api.PreparedMetadata, tracker string, dbPath string, description string, logger api.Logger) {
	if strings.TrimSpace(dbPath) == "" {
		return
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return
	}
	name := "[" + tracker + "]DESCRIPTION.txt"
	path := filepath.Join(tmpDir, name)
	if err := os.WriteFile(path, []byte(description), 0o600); err != nil {
		if logger != nil {
			logger.Warnf("trackers: %s description debug save: %v", tracker, err)
		}
		return
	}
	if logger != nil {
		logger.Debugf("trackers: %s description saved %s", tracker, path)
	}
}
