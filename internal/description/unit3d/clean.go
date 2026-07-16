// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"

	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
)

var (
	unit3dURLImgPattern    = regexp.MustCompile(`(?i)\[url=(https?://[^\]]+)\]\[img[^\]]*\](.*?)\[/img\]\[/url\]`)
	unit3dImgPattern       = regexp.MustCompile(`(?i)\[img[^\]]*\](.*?)\[/img\]`)
	unit3dURLToken         = regexp.MustCompile(`(?i)https?://[^\s\[\]]+`)
	unit3dWrapperTag       = regexp.MustCompile(`(?is)\[(?:center|align=[^\]]+)\][\s\S]*?\[/(?:center|align)\]`)
	unit3dParagraphSplit   = regexp.MustCompile(`\n\s*\n+`)
	unit3dTonemapOnlyBlock = regexp.MustCompile(
		`(?is)\[(?:center|align=[^\]]+)\]\s*\[code\]\s*Screenshots have been tonemapped for reference\s*\[/code\]\s*\[/(?:center|align)\]`,
	)
	unit3dEmptySpoilerTag = regexp.MustCompile(`(?is)\[spoiler(?:=[^\]]*)?\]\s*\[/spoiler\]`)
	unit3dWrapperOpenTag  = regexp.MustCompile(`(?is)\[(?:center|align=[^\]]+)\]`)
	unit3dWrapperCloseTag = regexp.MustCompile(`(?is)\[/(?:center|align)\]`)
	unit3dHostAliasCache  sync.Map
	unit3dSiteLinkCache   sync.Map
)

// CleanDescription normalizes a Unit3D description and extracts its image blocks.
func CleanDescription(description string, site string) Report {
	desc, report, ok := normalizeUnit3DDescriptionInput(description, site)
	if !ok {
		return report
	}

	report.Images = selectUnit3DFirstImageSet(desc)
	report.Description = cleanUnit3DDescriptionBody(desc)
	return report
}

// CleanDescriptionBody normalizes description text without extracting standalone images.
func CleanDescriptionBody(description string, site string) Report {
	desc, report, ok := normalizeUnit3DDescriptionInput(description, site)
	if !ok {
		return report
	}

	report.Description = cleanUnit3DDescriptionBody(desc)
	return report
}

// CleanDescriptionImages extracts and normalizes images without cleaning the surrounding body.
func CleanDescriptionImages(description string, site string) Report {
	desc, report, ok := normalizeUnit3DDescriptionInput(description, site)
	if !ok {
		return report
	}

	report.Images = selectUnit3DFirstImageSet(desc)
	return report
}

func normalizeUnit3DDescriptionInput(description string, site string) (string, Report, bool) {
	desc := normalizeNewlines(description)
	report := Report{}
	if strings.TrimSpace(desc) == "" {
		report.Notes = append(report.Notes, Note{Kind: "empty", Message: "blank input"})
		return "", report, false
	}

	desc = stripSiteLinks(desc, site)
	desc = replaceSiteHost(desc, site)
	return desc, report, true
}

func cleanUnit3DDescriptionBody(desc string) string {
	cleaned := unit3dURLImgPattern.ReplaceAllString(desc, "")
	cleaned = unit3dImgPattern.ReplaceAllString(cleaned, "")
	cleaned = unit3dTonemapOnlyBlock.ReplaceAllString(cleaned, "")
	cleaned = removeUnit3DEmptySpoilers(cleaned)
	cleaned = removeUnit3DImageOnlyWrappers(cleaned)
	cleaned = removeUnit3DEmptyWrappers(cleaned)
	cleaned = unit3dParagraphSplit.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}

func removeUnit3DEmptySpoilers(value string) string {
	previous := value
	for {
		next := unit3dEmptySpoilerTag.ReplaceAllString(previous, "")
		if next == previous {
			return next
		}
		previous = next
	}
}

func removeUnit3DImageOnlyWrappers(value string) string {
	return unit3dWrapperTag.ReplaceAllStringFunc(value, func(segment string) string {
		withoutURLImages := unit3dURLImgPattern.ReplaceAllString(segment, "")
		withoutImages := unit3dImgPattern.ReplaceAllString(withoutURLImages, "")
		if strings.TrimSpace(stripUnit3DWrapperTags(withoutImages)) == "" {
			return ""
		}
		return segment
	})
}

func removeUnit3DEmptyWrappers(value string) string {
	previous := value
	for {
		next := unit3dWrapperTag.ReplaceAllStringFunc(previous, func(segment string) string {
			if strings.TrimSpace(stripUnit3DWrapperTags(segment)) == "" {
				return ""
			}
			return segment
		})
		if next == previous {
			return next
		}
		previous = next
	}
}

func stripUnit3DWrapperTags(value string) string {
	cleaned := unit3dWrapperOpenTag.ReplaceAllString(value, "")
	cleaned = unit3dWrapperCloseTag.ReplaceAllString(cleaned, "")
	return cleaned
}

func selectUnit3DFirstImageSet(desc string) []Image {
	segments := unit3dWrapperTag.FindAllString(desc, -1)
	if len(segments) == 0 {
		segments = unit3dParagraphSplit.Split(desc, -1)
	}

	for _, segment := range segments {
		images := extractUnit3DImages(segment)
		if len(images) == 0 {
			continue
		}
		if isPosterLikeTopBlock(images) {
			continue
		}
		return images
	}

	return nil
}

func extractUnit3DImages(value string) []Image {
	images := make([]Image, 0)
	withoutURLBlocks := unit3dURLImgPattern.ReplaceAllStringFunc(value, func(block string) string {
		parts := unit3dURLImgPattern.FindStringSubmatch(block)
		if len(parts) < 3 {
			return block
		}
		webURL := strings.TrimSpace(parts[1])
		imgURL := strings.TrimSpace(parts[2])
		if imgURL != "" {
			rawURL := normalizeRawImageURL(imgURL)
			if linkedRawURL, ok := normalizeLinkedRawImageURL(webURL); ok {
				rawURL = linkedRawURL
			}
			host := imagehost.ExtractHost(rawURL)
			if host == "" {
				host = imagehost.ExtractHost(imgURL)
			}
			images = append(images, Image{
				ImgURL: imgURL,
				RawURL: rawURL,
				WebURL: webURL,
				Host:   host,
			})
		}
		return ""
	})

	withoutURLBlocks = unit3dImgPattern.ReplaceAllStringFunc(withoutURLBlocks, func(block string) string {
		parts := unit3dImgPattern.FindStringSubmatch(block)
		if len(parts) < 2 {
			return block
		}
		imgURL := strings.TrimSpace(parts[1])
		if imgURL != "" && !containsImage(images, imgURL) {
			host := imagehost.ExtractHost(imgURL)
			rawURL := normalizeRawImageURL(imgURL)
			images = append(images, Image{
				ImgURL: imgURL,
				RawURL: rawURL,
				WebURL: imgURL,
				Host:   host,
			})
		}
		return ""
	})

	_ = withoutURLBlocks
	return filterUnit3DImages(images)
}

func isPosterLikeTopBlock(images []Image) bool {
	if len(images) != 1 {
		return false
	}

	host := hostFromImage(images[0])
	if host == "" {
		return false
	}

	knownPosterHosts := map[string]struct{}{
		"image.tmdb.org":     {},
		"themoviedb.org":     {},
		"www.themoviedb.org": {},
	}

	_, found := knownPosterHosts[host]
	return found
}

func hostFromImage(image Image) string {
	selectedURL := strings.TrimSpace(image.RawURL)
	if selectedURL == "" {
		selectedURL = strings.TrimSpace(image.ImgURL)
	}
	if selectedURL == "" {
		return ""
	}
	parsed, err := url.Parse(selectedURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func stripSiteLinks(description string, site string) string {
	parsed, err := url.Parse(site)
	if err != nil {
		return description
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return description
	}
	pattern := siteLinkPattern(host)
	return pattern.ReplaceAllString(description, "$1")
}

func replaceSiteHost(description string, site string) string {
	parsed, err := url.Parse(site)
	if err != nil {
		return description
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return description
	}
	domain := siteLabelFromHost(host)
	if domain == "" {
		return description
	}
	return replaceOutsideURLs(description, siteHostAliases(host), domain)
}

func siteLabelFromHost(host string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	parts := strings.Split(trimmed, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func siteHostAliases(host string) []string {
	base := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	if base == "" {
		return nil
	}

	aliases := []string{"www." + base, base}
	if host != base {
		aliases[0], aliases[1] = host, base
	}
	return aliases
}

func replaceOutsideURLs(value string, aliases []string, newValue string) string {
	matches := unit3dURLToken.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return replaceAllFold(value, aliases, newValue)
	}

	var builder strings.Builder
	builder.Grow(len(value))
	last := 0
	for _, match := range matches {
		builder.WriteString(replaceAllFold(value[last:match[0]], aliases, newValue))
		builder.WriteString(value[match[0]:match[1]])
		last = match[1]
	}
	builder.WriteString(replaceAllFold(value[last:], aliases, newValue))
	return builder.String()
}

func replaceAllFold(value string, aliases []string, newValue string) string {
	pattern := hostAliasPattern(aliases)
	if pattern == nil {
		return value
	}

	matches := pattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}

	var builder strings.Builder
	builder.Grow(len(value))
	last := 0
	for _, match := range matches {
		builder.WriteString(value[last:match[0]])
		builder.WriteString(value[match[2]:match[3]])
		builder.WriteString(newValue)
		builder.WriteString(value[match[4]:match[5]])
		last = match[1]
	}
	builder.WriteString(value[last:])
	return builder.String()
}

func hostAliasPattern(aliases []string) *regexp.Regexp {
	normalized := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		trimmed := strings.ToLower(strings.TrimSpace(alias))
		if trimmed == "" {
			continue
		}
		if _, found := seen[trimmed]; found {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, regexp.QuoteMeta(trimmed))
	}
	if len(normalized) == 0 {
		return nil
	}

	key := strings.Join(normalized, "\x00")
	if cached, ok := unit3dHostAliasCache.Load(key); ok {
		if pattern, ok := cached.(*regexp.Regexp); ok {
			return pattern
		}
	}

	pattern := regexp.MustCompile(`(?i)(^|[^[:alnum:].-])(?:` + strings.Join(normalized, `|`) + `)($|[^[:alnum:].-])`)
	actual, _ := unit3dHostAliasCache.LoadOrStore(key, pattern)
	if pattern, ok := actual.(*regexp.Regexp); ok {
		return pattern
	}
	return pattern
}

func siteLinkPattern(host string) *regexp.Regexp {
	if cached, ok := unit3dSiteLinkCache.Load(host); ok {
		if pattern, ok := cached.(*regexp.Regexp); ok {
			return pattern
		}
	}
	pattern := regexp.MustCompile(`(?i)\[url=https?://(?:www\.)?` + regexp.QuoteMeta(host) + `(?:/[^\]]*)?\]([^\[]+)\[/url\]`)
	actual, _ := unit3dSiteLinkCache.LoadOrStore(host, pattern)
	if pattern, ok := actual.(*regexp.Regexp); ok {
		return pattern
	}
	return pattern
}

func containsImage(images []Image, targetURL string) bool {
	for _, image := range images {
		if image.ImgURL == targetURL {
			return true
		}
	}
	return false
}

func filterUnit3DImages(images []Image) []Image {
	banned := map[string]struct{}{
		"https://blutopia.xyz/favicon.ico":       {},
		"https://i.ibb.co/2NVWb0c/uploadrr.webp": {},
		"https://blutopia/favicon.ico":           {},
		"https://pixhost.cc/606tk4.png":          {},
		"https://pixhost.to/606tk4.png":          {},
	}

	filtered := make([]Image, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		rawURL := normalizeRawImageURL(image.RawURL)
		if rawURL == "" {
			rawURL = normalizeRawImageURL(image.ImgURL)
		}
		if rawURL != "" {
			image.RawURL = rawURL
		}
		selectedURL := strings.TrimSpace(image.RawURL)
		if selectedURL == "" {
			selectedURL = strings.TrimSpace(image.ImgURL)
		}
		if selectedURL == "" {
			continue
		}
		if _, found := banned[selectedURL]; found {
			continue
		}
		if strings.Contains(strings.ToLower(selectedURL), "thumbs") {
			continue
		}
		if _, found := seen[selectedURL]; found {
			continue
		}
		seen[selectedURL] = struct{}{}
		filtered = append(filtered, image)
	}
	return filtered
}

func normalizeNewlines(value string) string {
	decoded := html.UnescapeString(value)
	return strings.ReplaceAll(decoded, "\r\n", "\n")
}

func normalizeRawImageURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	pathValue := strings.TrimSpace(parsed.Path)

	if strings.Contains(host, "imgbox.com") && strings.Contains(host, "thumbs") {
		parsed.Host = strings.ReplaceAll(parsed.Host, "thumbs2.imgbox.com", "images2.imgbox.com")
		parsed.Path = strings.ReplaceAll(parsed.Path, "_t.png", "_o.png")
		parsed.Path = strings.ReplaceAll(parsed.Path, "_t.jpg", "_o.jpg")
		parsed.Path = strings.ReplaceAll(parsed.Path, "_t.jpeg", "_o.jpeg")
		return parsed.String()
	}

	if isPixhostHost(host) && strings.HasPrefix(pathValue, "/thumbs/") {
		replacePixhostThumbHost(parsed, host)
		parsed.Path = strings.Replace(pathValue, "/thumbs/", "/images/", 1)
		return parsed.String()
	}

	return trimmed
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

func normalizeLinkedRawImageURL(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if !isLikelyImageURL(trimmed) {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	pathValue := strings.ToLower(strings.TrimSpace(parsed.Path))
	if isPixhostHost(host) && strings.HasPrefix(pathValue, "/show/") {
		return "", false
	}

	return normalizeRawImageURL(trimmed), true
}

func isPixhostHost(host string) bool {
	return host == "pixhost.cc" ||
		host == "pixhost.to" ||
		strings.HasSuffix(host, ".pixhost.cc") ||
		strings.HasSuffix(host, ".pixhost.to")
}

func isLikelyImageURL(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	pathValue := strings.ToLower(strings.TrimSpace(parsed.Path))
	switch {
	case strings.HasSuffix(pathValue, ".jpg"):
		return true
	case strings.HasSuffix(pathValue, ".jpeg"):
		return true
	case strings.HasSuffix(pathValue, ".png"):
		return true
	case strings.HasSuffix(pathValue, ".webp"):
		return true
	default:
		return false
	}
}
