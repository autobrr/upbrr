// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var onlyBBCodePattern = regexp.MustCompile(`\[/?[a-zA-Z0-9]+(?:=[^\]]*)?\]`)

func normalizeNewlines(value string) string {
	value = html.UnescapeString(value)
	return strings.ReplaceAll(value, "\r\n", "\n")
}

// NormalizeNewlines decodes HTML entities and normalizes CRLF line endings.
func NormalizeNewlines(value string) string { return normalizeNewlines(value) }

func isOnlyBBCode(value string) bool {
	text := onlyBBCodePattern.ReplaceAllString(value, "")
	return strings.TrimSpace(text) == ""
}

// IsOnlyTags reports whether value contains no text outside BBCode tags.
func IsOnlyTags(value string) bool { return isOnlyBBCode(value) }

func removeExtraLines(value string) string {
	re := regexp.MustCompile(`\n{3,}`)
	return re.ReplaceAllString(value, "\n\n")
}

// RemoveExtraLines collapses three or more consecutive newlines.
func RemoveExtraLines(value string) string { return removeExtraLines(value) }

func convertPreToCode(value string) string {
	value = strings.ReplaceAll(value, "[pre]", "[code]")
	value = strings.ReplaceAll(value, "[/pre]", "[/code]")
	return value
}

// ConvertPreToCode changes pre tags to code tags.
func ConvertPreToCode(value string) string { return convertPreToCode(value) }

func convertCodeToPre(value string) string {
	value = strings.ReplaceAll(value, "[code]", "[pre]")
	value = strings.ReplaceAll(value, "[/code]", "[/pre]")
	return value
}

// ConvertCodeToPre changes code tags to pre tags.
func ConvertCodeToPre(value string) string { return convertCodeToPre(value) }

func convertHideToSpoiler(value string) string {
	value = strings.ReplaceAll(value, "[hide", "[spoiler")
	value = strings.ReplaceAll(value, "[/hide]", "[/spoiler]")
	return value
}

// ConvertHideToSpoiler changes hide tags to spoiler tags.
func ConvertHideToSpoiler(value string) string { return convertHideToSpoiler(value) }

func convertSpoilerToHide(value string) string {
	value = strings.ReplaceAll(value, "[spoiler", "[hide")
	value = strings.ReplaceAll(value, "[/spoiler]", "[/hide]")
	return value
}

// ConvertSpoilerToHide changes spoiler tags to hide tags.
func ConvertSpoilerToHide(value string) string { return convertSpoilerToHide(value) }

func removeHide(value string) string {
	value = strings.ReplaceAll(value, "[hide]", "")
	value = strings.ReplaceAll(value, "[/hide]", "")
	return value
}

// RemoveHide removes hide tags.
func RemoveHide(value string) string { return removeHide(value) }

func convertNamedSpoilerToNamedHide(value string) string {
	re := regexp.MustCompile(`(?i)\[spoiler=([^]]+)]`)
	value = re.ReplaceAllString(value, "[hide=$1]")
	value = strings.ReplaceAll(value, "[/spoiler]", "[/hide]")
	return value
}

// ConvertNamedSpoilerToNamedHide converts named spoilers to named hides.
func ConvertNamedSpoilerToNamedHide(value string) string {
	return convertNamedSpoilerToNamedHide(value)
}

func removeSpoiler(value string) string {
	re := regexp.MustCompile(`(?i)\[/?spoiler[\s\S]*?\]`)
	return re.ReplaceAllString(value, "")
}

// RemoveSpoiler removes spoiler tags.
func RemoveSpoiler(value string) string { return removeSpoiler(value) }

func removeColor(value string) string {
	re := regexp.MustCompile(`(?i)\[/?color(?:=[^\]]*)?\]`)
	return re.ReplaceAllString(value, "")
}

// RemoveColor removes color tags.
func RemoveColor(value string) string { return removeColor(value) }

func convertNamedSpoilerToNormalSpoiler(value string) string {
	re := regexp.MustCompile(`(?i)(\[spoiler=[^]]+])`)
	return re.ReplaceAllString(value, "[spoiler]")
}

// ConvertNamedSpoilerToNormalSpoiler removes spoiler titles.
func ConvertNamedSpoilerToNormalSpoiler(value string) string {
	return convertNamedSpoilerToNormalSpoiler(value)
}

func convertCodeToQuote(value string) string {
	value = strings.ReplaceAll(value, "[code", "[quote")
	value = strings.ReplaceAll(value, "[/code]", "[/quote]")
	return value
}

// ConvertCodeToQuote changes code tags to quote tags.
func ConvertCodeToQuote(value string) string { return convertCodeToQuote(value) }

func removeImgResize(value string) string {
	re := regexp.MustCompile(`(?i)\[img(?:[^\]]*)\]`)
	return re.ReplaceAllString(value, "[img]")
}

// RemoveImageResize removes image tag attributes.
func RemoveImageResize(value string) string { return removeImgResize(value) }

func convertToAlign(value string) string {
	reStart := regexp.MustCompile(`\[(right|center|left)\]`)
	reEnd := regexp.MustCompile(`\[/(right|center|left)\]`)
	value = reStart.ReplaceAllString(value, "[align=$1]")
	value = reEnd.ReplaceAllString(value, "[/align]")
	return value
}

// ConvertToAlign converts positional tags to align tags.
func ConvertToAlign(value string) string { return convertToAlign(value) }

func removeSup(value string) string {
	value = strings.ReplaceAll(value, "[sup]", "")
	value = strings.ReplaceAll(value, "[/sup]", "")
	return value
}

// RemoveSup removes sup tags.
func RemoveSup(value string) string { return removeSup(value) }

func removeSub(value string) string {
	value = strings.ReplaceAll(value, "[sub]", "")
	value = strings.ReplaceAll(value, "[/sub]", "")
	return value
}

// RemoveSub removes sub tags.
func RemoveSub(value string) string { return removeSub(value) }

func removeList(value string) string {
	value = strings.ReplaceAll(value, "[list]", "")
	value = strings.ReplaceAll(value, "[/list]", "")
	return value
}

// RemoveList removes list container tags.
func RemoveList(value string) string { return removeList(value) }

func convertComparisonToCollapse(value string, maxWidth int) string {
	re := regexp.MustCompile(`(?i)\[comparison=[\s\S]*?\[/comparison\]`)
	comparisons := re.FindAllString(value, -1)
	for _, comp := range comparisons {
		parts := strings.SplitN(comp, "]", 2)
		if len(parts) < 2 {
			continue
		}
		compSources := strings.ReplaceAll(parts[0], "[comparison=", "")
		compSources = strings.ReplaceAll(compSources, " ", "")
		sources := strings.Split(compSources, ",")
		images := strings.ReplaceAll(parts[1], "[/comparison]", "")
		images = strings.ReplaceAll(images, ",", "\n")
		images = strings.ReplaceAll(images, " ", "\n")
		imgRe := regexp.MustCompile(`(?i)(https?://.*\.(?:png|jpg))`)
		compImages := imgRe.FindAllString(images, -1)
		screensPerLine := len(sources)
		if screensPerLine == 0 {
			continue
		}
		imgSize := min(maxWidth/screensPerLine, 350)
		line := make([]string, 0, screensPerLine)
		output := make([]string, 0)
		for _, img := range compImages {
			img = strings.TrimSpace(img)
			if img == "" {
				continue
			}
			bb := "[url=" + img + "][img=" + itoa(imgSize) + "]" + img + "[/img][/url]"
			line = append(line, bb)
			if len(line) == screensPerLine {
				output = append(output, strings.Join(line, ""))
				line = line[:0]
			}
		}
		outputStr := strings.Join(output, "\n")
		newBB := "[spoiler=" + strings.Join(sources, " vs ") + "][center]" + strings.Join(sources, " | ") + "[/center]\n" + outputStr + "[/spoiler]"
		value = strings.ReplaceAll(value, comp, newBB)
	}
	return value
}

// ConvertComparisonToCollapse converts comparison blocks to spoilers.
func ConvertComparisonToCollapse(value string, maxWidth int) string {
	return convertComparisonToCollapse(value, maxWidth)
}

func convertComparisonToCentered(value string, maxWidth int) string {
	re := regexp.MustCompile(`(?i)\[comparison=[\s\S]*?\[/comparison\]`)
	comparisons := re.FindAllString(value, -1)
	for _, comp := range comparisons {
		parts := strings.SplitN(comp, "]", 2)
		if len(parts) < 2 {
			continue
		}
		compSources := strings.TrimSpace(strings.ReplaceAll(parts[0], "[comparison=", ""))
		sources := regexp.MustCompile(`\s*,\s*`).Split(compSources, -1)
		images := strings.ReplaceAll(parts[1], "[/comparison]", "")
		images = strings.ReplaceAll(images, ",", "\n")
		images = strings.ReplaceAll(images, " ", "\n")
		imgRe := regexp.MustCompile(`(?i)(https?://.*\.(?:png|jpg))`)
		compImages := imgRe.FindAllString(images, -1)
		screensPerLine := len(sources)
		if screensPerLine == 0 {
			continue
		}
		imgSize := min(maxWidth/screensPerLine, 350)
		line := make([]string, 0, screensPerLine)
		output := make([]string, 0)
		for _, img := range compImages {
			img = strings.TrimSpace(img)
			if img == "" {
				continue
			}
			bb := "[url=" + img + "][img=" + itoa(imgSize) + "]" + img + "[/img][/url]"
			line = append(line, bb)
			if len(line) == screensPerLine {
				output = append(output, strings.Join(line, ""))
				line = line[:0]
			}
		}
		outputStr := strings.Join(output, "\n")
		newBB := "[center]" + strings.Join(sources, " | ") + "\n" + outputStr + "[/center]"
		value = strings.ReplaceAll(value, comp, newBB)
	}
	return value
}

// ConvertComparisonToCentered renders comparison blocks as centered screenshots.
func ConvertComparisonToCentered(value string, maxWidth int) string {
	return convertComparisonToCentered(value, maxWidth)
}

func convertCollapseToComparison(value string, spoilerHide string, collapses []string) string {
	if len(collapses) == 0 {
		return value
	}
	imgTagRe := regexp.MustCompile(`(?i)\[img[\s\S]*?\[/img\]`)
	imgURLRe := regexp.MustCompile(`(?i)\[img[\s\S]*\]`)
	for _, tag := range collapses {
		images := imgTagRe.FindAllString(tag, -1)
		if len(images) < 6 {
			continue
		}
		compImages := make([]string, 0, len(images))
		for _, image := range images {
			imageURL := imgURLRe.ReplaceAllString(strings.ReplaceAll(image, "[/img]", ""), "")
			compImages = append(compImages, imageURL)
		}
		sources := ""
		if spoilerHide == "spoiler" {
			match := regexp.MustCompile(`(?i)\[spoiler[\s\S]*?\]`).FindString(tag)
			if match == "" {
				continue
			}
			sources = strings.TrimSuffix(strings.TrimPrefix(match, "[spoiler="), "]")
		} else if spoilerHide == "hide" {
			match := regexp.MustCompile(`(?i)\[hide[\s\S]*?\]`).FindString(tag)
			if match == "" {
				continue
			}
			sources = strings.TrimSuffix(strings.TrimPrefix(match, "[hide="), "]")
		}
		if sources == "" {
			continue
		}
		sources = regexp.MustCompile(`(?i)comparison`).ReplaceAllString(sources, "")
		for _, sep := range []string{"vs", ",", "|"} {
			parts := strings.Split(sources, sep)
			sources = strings.Join(parts, "$")
		}
		finalSources := strings.Split(sources, "$")
		for i, source := range finalSources {
			finalSources[i] = strings.TrimSpace(source)
		}
		compImagesStr := strings.Join(compImages, "\n")
		finalSourcesStr := strings.Join(finalSources, ", ")
		spoilToComp := "[comparison=" + finalSourcesStr + "]" + compImagesStr + "[/comparison]"
		value = strings.ReplaceAll(value, tag, spoilToComp)
	}
	return value
}

// ConvertCollapseToComparison converts screenshot comparison collapses to BBCode comparisons.
func ConvertCollapseToComparison(value string, spoilerHide string, collapses []string) string {
	return convertCollapseToComparison(value, spoilerHide, collapses)
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func normalizeImageRawURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	converted := convertImgboxThumbURL(trimmed)
	converted = convertPixhostThumbURL(converted)
	return converted
}

// NormalizeImageRawURL expands known image-host thumbnail URLs to original images.
func NormalizeImageRawURL(value string) string { return normalizeImageRawURL(value) }

func convertImgboxThumbURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if !strings.Contains(host, "imgbox.com") || !strings.Contains(host, "thumbs") {
		return value
	}
	parsed.Host = strings.ReplaceAll(parsed.Host, "thumbs2.imgbox.com", "images2.imgbox.com")
	parsed.Path = strings.ReplaceAll(parsed.Path, "_t.png", "_o.png")
	parsed.Path = strings.ReplaceAll(parsed.Path, "_t.jpg", "_o.jpg")
	parsed.Path = strings.ReplaceAll(parsed.Path, "_t.jpeg", "_o.jpeg")
	return parsed.String()
}

func convertPixhostThumbURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	pathValue := strings.TrimSpace(parsed.Path)
	if !isPixhostHost(host) || !strings.HasPrefix(pathValue, "/thumbs/") {
		return value
	}
	replacePixhostThumbHost(parsed, host)
	parsed.Path = strings.Replace(pathValue, "/thumbs/", "/images/", 1)
	return parsed.String()
}

func replacePixhostThumbHost(parsed *url.URL, host string) {
	hostParts := strings.SplitN(host, ".", 2)
	if len(hostParts) != 2 {
		return
	}
	first := hostParts[0]
	if !strings.HasPrefix(first, "t") || len(first) == 1 {
		return
	}

	port := parsed.Port()
	parsed.Host = "img" + strings.TrimPrefix(first, "t") + "." + hostParts[1]
	if port != "" {
		parsed.Host += ":" + port
	}
}

func isPixhostHost(host string) bool {
	return host == "pixhost.cc" ||
		host == "pixhost.to" ||
		strings.HasSuffix(host, ".pixhost.cc") ||
		strings.HasSuffix(host, ".pixhost.to")
}
