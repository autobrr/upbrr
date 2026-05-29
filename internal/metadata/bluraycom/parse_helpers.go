// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/pkg/api"
)

var whitespacePattern = regexp.MustCompile(`\s+`)

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, className string) bool {
	classes := strings.Fields(attr(n, "class"))
	for _, class := range classes {
		if class == className {
			return true
		}
	}
	return false
}

func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if pred(n) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, pred); found != nil {
			return found
		}
	}
	return nil
}

func findAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var results []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if pred(node) {
			results = append(results, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return results
}

func nextNode(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	if n.FirstChild != nil {
		return n.FirstChild
	}
	for n != nil {
		if n.NextSibling != nil {
			return n.NextSibling
		}
		n = n.Parent
	}
	return nil
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			b.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return cleanText(b.String())
}

func nodeTextWithBreaks(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		if node.Type == html.ElementNode && node.Data == "br" {
			b.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode && (node.Data == "div" || node.Data == "p") {
			b.WriteByte('\n')
		}
	}
	walk(n)
	return b.String()
}

func cleanText(value string) string {
	return strings.TrimSpace(whitespacePattern.ReplaceAllString(value, " "))
}

func absolutize(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "//") {
		return "https:" + trimmed
	}
	if strings.HasPrefix(trimmed, "/") {
		return baseURL + trimmed
	}
	return baseURL + "/" + trimmed
}

func releaseSectionMatches(title string, input LookupInput) bool {
	title = strings.TrimSpace(title)
	discType := strings.ToUpper(strings.TrimSpace(input.DiscType))
	resolution := strings.ToLower(strings.TrimSpace(input.Resolution))
	is3D := strings.EqualFold(strings.TrimSpace(input.Is3D), "yes") || strings.EqualFold(strings.TrimSpace(input.Is3D), "true")
	is4K := strings.Contains(resolution, "2160") || strings.Contains(resolution, "4k")
	switch {
	case discType == "DVD":
		return strings.Contains(title, "DVD Editions")
	case is3D:
		return strings.Contains(title, "3D Blu-ray Editions")
	case is4K:
		return strings.Contains(title, "4K Blu-ray Editions")
	default:
		return strings.Contains(title, "Blu-ray Editions") &&
			!strings.Contains(title, "3D Blu-ray Editions") &&
			!strings.Contains(title, "4K Blu-ray Editions")
	}
}

func previousFlagTitle(root *html.Node, target *html.Node) string {
	current := ""
	for n := root; n != nil; n = nextNode(n) {
		if n == target {
			break
		}
		if n.Type == html.ElementNode && n.Data == "img" && attr(n, "width") == "18" && attr(n, "height") == "12" {
			if title := cleanText(attr(n, "title")); title != "" {
				current = title
			}
		}
	}
	if current == "" {
		return "Unknown"
	}
	return current
}

func nextSmallWithStyle(start *html.Node, styleNeedle string) *html.Node {
	needle := strings.ToLower(styleNeedle)
	for n := nextNode(start); n != nil; n = nextNode(n) {
		if n.Type == html.ElementNode && n.Data == "h3" {
			return nil
		}
		if n.Type == html.ElementNode && n.Data == "a" && attr(n, "href") != "" {
			return nil
		}
		if n.Type == html.ElementNode && n.Data == "small" && strings.Contains(strings.ToLower(attr(n, "style")), needle) {
			return n
		}
	}
	return nil
}

func extractSection(specs *html.Node, sectionTitle string) string {
	heading := findFirst(specs, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "span" && hasClass(n, "subheading") && strings.EqualFold(textContent(n), sectionTitle)
	})
	if heading == nil {
		return ""
	}
	var parts []string
	for n := heading.NextSibling; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && n.Data == "span" && hasClass(n, "subheading") {
			break
		}
		parts = append(parts, textContent(n))
	}
	return cleanText(strings.Join(parts, " "))
}

func parseAudioLines(value string) []string {
	rawLines := strings.Split(value, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = cleanText(strings.ReplaceAll(line, "(less)", ""))
		if line == "" || strings.EqualFold(line, "less") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}

	out := make([]string, 0, len(lines))
	for idx := 0; idx < len(lines); idx++ {
		current := lines[idx]
		if idx+1 < len(lines) && strings.Contains(strings.ToLower(current), "atmos") {
			next := lines[idx+1]
			currentLang := trackLanguagePrefix(current)
			nextLang := trackLanguagePrefix(next)
			if currentLang != "" && strings.EqualFold(currentLang, nextLang) &&
				strings.Contains(current, "Dolby Atmos") &&
				(strings.Contains(next, "Dolby Digital") || strings.Contains(next, "Dolby TrueHD")) {
				channels := ""
				if strings.Contains(next, "7.1") {
					channels = "7.1"
				} else if strings.Contains(next, "5.1") {
					channels = "5.1"
				}
				if strings.Contains(next, "TrueHD") {
					out = append(out, cleanText(currentLang+": Dolby TrueHD Atmos "+channels))
				} else {
					out = append(out, cleanText(currentLang+": Dolby Atmos "+channels))
				}
				idx++
				continue
			}
		}
		if strings.HasPrefix(current, "Note:") && len(out) > 0 {
			out[len(out)-1] = out[len(out)-1] + " - " + current
			continue
		}
		out = append(out, current)
	}
	return out
}

func parseSubtitles(value string) []string {
	value = strings.ReplaceAll(value, "(less)", "")
	fields := regexp.MustCompile(`[,\n]`).Split(value, -1)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if cleaned := cleanText(field); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func trackLanguagePrefix(value string) string {
	before, _, ok := strings.Cut(value, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(before)
}

func wordNumber(value string) int {
	value = strings.TrimSpace(value)
	if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
		return parsed
	}
	switch strings.ToLower(value) {
	case "one":
		return 1
	case "two":
		return 2
	case "three":
		return 3
	case "four":
		return 4
	case "five":
		return 5
	default:
		return 1
	}
}

func extractBDFormat(value string) string {
	if match := bdFormatPattern.FindStringSubmatch(value); len(match) == 2 {
		return cleanText(match[1])
	}
	return ""
}

func dedupeReleases(input []api.BlurayReleaseCandidate) []api.BlurayReleaseCandidate {
	seen := make(map[string]struct{}, len(input))
	out := make([]api.BlurayReleaseCandidate, 0, len(input))
	for _, release := range input {
		key := strings.TrimSpace(release.ReleaseID)
		if key == "" {
			key = strings.TrimSpace(release.URL)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, release)
	}
	return out
}

func extractCoverImages(htmlText string, root *html.Node) []api.BlurayImage {
	byKind := make(map[string]string)
	for _, match := range appendImagePattern.FindAllStringSubmatch(htmlText, -1) {
		if len(match) < 2 {
			continue
		}
		fragment, err := html.Parse(strings.NewReader(match[1]))
		if err != nil {
			continue
		}
		img := findFirst(fragment, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img" && attr(n, "src") != ""
		})
		if img == nil {
			continue
		}
		addCoverImage(byKind, attr(img, "id"), attr(img, "src"))
	}
	if len(byKind) == 0 {
		overlays := findAll(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "simple_overlay")
		})
		for _, overlay := range overlays {
			img := findFirst(overlay, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "img" && attr(n, "src") != ""
			})
			if img != nil {
				addCoverImage(byKind, attr(img, "id"), attr(img, "src"))
			}
		}
	}
	order := []string{"front", "back", "slip"}
	out := make([]api.BlurayImage, 0, len(byKind))
	for _, kind := range order {
		if imageURL := byKind[kind]; imageURL != "" {
			out = append(out, api.BlurayImage{Kind: kind, URL: imageURL})
			delete(byKind, kind)
		}
	}
	for kind, imageURL := range byKind {
		out = append(out, api.BlurayImage{Kind: kind, URL: imageURL})
	}
	return out
}

func addCoverImage(out map[string]string, rawID string, rawURL string) {
	imageURL := cleanImageURL(absolutize(rawURL))
	if imageURL == "" {
		return
	}
	id := strings.ToLower(strings.TrimSpace(rawID))
	kind := strings.TrimSpace(rawID)
	switch {
	case strings.Contains(id, "front"):
		kind = "front"
	case strings.Contains(id, "back"):
		kind = "back"
	case strings.Contains(id, "slip"):
		kind = "slip"
	case kind == "":
		kind = "image"
	}
	out[kind] = imageURL
}

func cleanImageURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	extensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	lower := strings.ToLower(trimmed)
	for _, ext := range extensions {
		if pos := strings.Index(lower, ext); pos >= 0 {
			return trimmed[:pos+len(ext)]
		}
	}
	return trimmed
}
