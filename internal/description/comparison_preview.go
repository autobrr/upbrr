// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/frustra/bbcode"
)

type comparisonPreviewImage struct {
	src  string
	full string
}

// Without whitespace a comma belongs to the URL, including embedded query URLs.
var comparisonURLSeparator = regexp.MustCompile(`(?i),(\s+)(https?://)`)

// Native comparison tags provide source order explicitly. Keep incomplete or
// invalid blocks in the ordinary renderer instead of shifting their columns.
func compileComparison(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	sources := comparisonLabels(descriptionTagLabel(node.GetOpeningTag()), ",")
	if len(sources) >= 2 && node.ClosingTag != nil {
		if images := nativeComparisonImages(node.Children, len(sources)); len(images) > 0 {
			return renderComparison(sources, images), false
		}
	}
	return bbcode.DefaultTagCompiler(node)
}

func nativeComparisonImages(nodes []*bbcode.BBCodeNode, columns int) []comparisonPreviewImage {
	// Some native descriptions put a single alignment wrapper around their images.
	content := slices.DeleteFunc(slices.Clone(nodes), isBlankComparisonText)
	if len(content) == 1 && isCenteredComparisonWrapper(content[0]) {
		content = content[0].Children
		var raw string
		if len(content) == 1 && content[0].ID == bbcode.TEXT {
			raw, _ = content[0].Value.(string)
		}
		// A flat native URL list is valid on one line. Multiline or tagged
		// content must use complete rows or one image per line consistently.
		if raw == "" || strings.Contains(strings.TrimSpace(raw), "\n") {
			if rows := comparisonRows(content, columns, true); len(rows) > 0 {
				return rows
			}
			if images := comparisonRows(content, 1, true); len(images)%columns == 0 {
				return images
			}
			return nil
		}
	}

	var images []comparisonPreviewImage
	for _, child := range content {
		switch {
		case child.ID == bbcode.TEXT:
			value, ok := child.Value.(string)
			if !ok {
				return nil
			}
			images, ok = appendNativeComparisonURLs(images, comparisonURLSeparator.ReplaceAllString(value, "${1}${2}"))
			if !ok {
				return nil
			}
		case isCenteredComparisonWrapper(child):
			row := comparisonRows(child.Children, columns, true)
			if len(images)%columns != 0 || len(row) != columns {
				return nil
			}
			images = append(images, row...)
		default:
			img, ok := comparisonImage(child)
			if !ok {
				return nil
			}
			images = append(images, img)
		}
	}
	if len(images)%columns != 0 {
		return nil
	}
	return images
}

func appendNativeComparisonURLs(images []comparisonPreviewImage, text string) ([]comparisonPreviewImage, bool) {
	for value := range strings.FieldsSeq(text) {
		if !absoluteComparisonURL(value) {
			return nil, false
		}
		images = append(images, comparisonPreviewImage{src: value, full: value})
	}
	return images, true
}

func isCenteredComparisonWrapper(node *bbcode.BBCodeNode) bool {
	opening := node.GetOpeningTag()
	return node.ClosingTag != nil && opening != nil && (opening.Name == "center" || opening.Name == "align") &&
		resolveAlignment(node) == "center"
}

// The tracker conversions label sources and delimit each complete row with a
// newline. Mixed content or incomplete rows are not evidence of a comparison.
func compileCenteredComparison(node *bbcode.BBCodeNode) *bbcode.HTMLTag {
	if !isCenteredComparisonWrapper(node) || len(node.Children) == 0 {
		return nil
	}
	first := node.Children[0]
	raw, ok := first.Value.(string)
	if !ok || first.ID != bbcode.TEXT {
		return nil
	}
	header, body, ok := strings.Cut(strings.TrimLeft(raw, " \t\n"), "\n")
	if !ok || strings.TrimSpace(body) != "" {
		return nil
	}
	sources := comparisonLabels(header, " | ")
	if len(sources) < 2 || conflictsWithParentComparison(node, sources) {
		return nil
	}
	if images := comparisonRows(node.Children[1:], len(sources), false); len(images) > 0 {
		return renderComparison(sources, images)
	}
	return nil
}

// A centered child cannot override recognized parent comparison labels.
func conflictsWithParentComparison(node *bbcode.BBCodeNode, sources []string) bool {
	if node.Parent == nil {
		return false
	}
	opening := node.Parent.GetOpeningTag()
	if opening == nil {
		return false
	}
	sep := ","
	switch opening.Name {
	case "comparison":
	case "spoiler", "hide":
		sep = " vs "
		// An unrelated spoiler can still contain an independent comparison.
		for _, child := range node.Parent.Children {
			if !isBlankComparisonText(child) {
				if child != node {
					return false
				}
				break
			}
		}
	default:
		return false
	}
	labels := comparisonLabels(descriptionTagLabel(opening), sep)
	return len(labels) >= 2 && !slices.Equal(labels, sources)
}

func compileCollapsedComparison(node *bbcode.BBCodeNode) *bbcode.HTMLTag {
	sources := comparisonLabels(descriptionTagLabel(node.GetOpeningTag()), " vs ")
	if node.ClosingTag == nil || len(sources) < 2 {
		return nil
	}
	children := node.Children
	for len(children) > 0 && isBlankComparisonText(children[0]) {
		children = children[1:]
	}
	if len(children) > 0 && isCenteredComparisonWrapper(children[0]) {
		header := children[0]
		if len(header.Children) != 1 || header.Children[0].ID != bbcode.TEXT ||
			!slices.Equal(sources, comparisonLabels(bbcode.CompileText(header), " | ")) {
			return nil
		}
		children = children[1:]
	}
	if images := comparisonRows(children, len(sources), false); len(images) > 0 {
		return renderComparison(sources, images)
	}
	return nil
}

func comparisonRows(nodes []*bbcode.BBCodeNode, columns int, nativeURLs bool) []comparisonPreviewImage {
	var images []comparisonPreviewImage
	rowStart := 0
	for _, node := range nodes {
		if node.ID == bbcode.TEXT {
			value, ok := node.Value.(string)
			if !ok {
				return nil
			}
			if nativeURLs {
				// Strip commas used as separators before splitting lines, but
				// keep the delimiter's whitespace and its row boundaries.
				value = comparisonURLSeparator.ReplaceAllString(value, "${1}${2}")
			}
			for {
				line, rest, newline := strings.Cut(value, "\n")
				if strings.TrimSpace(line) != "" {
					if !nativeURLs {
						return nil
					}
					images, ok = appendNativeComparisonURLs(images, line)
					if !ok || len(images)-rowStart > columns {
						return nil
					}
				}
				if !newline {
					break
				}
				if count := len(images) - rowStart; count != 0 && count != columns {
					return nil
				}
				rowStart = len(images)
				value = rest
			}
			continue
		}
		image, ok := comparisonImage(node)
		if !ok || len(images)-rowStart == columns {
			return nil
		}
		images = append(images, image)
	}
	if count := len(images) - rowStart; count != 0 && count != columns {
		return nil
	}
	return images
}

func comparisonImage(node *bbcode.BBCodeNode) (comparisonPreviewImage, bool) {
	opening := node.GetOpeningTag()
	if opening == nil || node.ClosingTag == nil {
		return comparisonPreviewImage{}, false
	}
	var full string
	if opening.Name == "url" {
		full = opening.Value
		if !absoluteComparisonURL(full) {
			return comparisonPreviewImage{}, false
		}
		var image *bbcode.BBCodeNode
		for _, child := range node.Children {
			if isBlankComparisonText(child) {
				continue
			}
			if image != nil {
				return comparisonPreviewImage{}, false
			}
			image = child
		}
		if image == nil {
			return comparisonPreviewImage{}, false
		}
		node = image
		opening = node.GetOpeningTag()
	}
	if opening == nil || node.ClosingTag == nil || opening.Name != "img" {
		return comparisonPreviewImage{}, false
	}
	tag, _ := compileImg(node)
	src := tag.Attrs["src"]
	if !absoluteComparisonURL(src) {
		return comparisonPreviewImage{}, false
	}
	if full == "" {
		full = src
	}
	return comparisonPreviewImage{src: src, full: full}, true
}

func absoluteComparisonURL(value string) bool {
	if strings.ContainsAny(value, "<>\\\"' \t\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func isBlankComparisonText(node *bbcode.BBCodeNode) bool {
	value, ok := node.Value.(string)
	return node.ID == bbcode.TEXT && ok && strings.TrimSpace(value) == ""
}

func comparisonLabels(value, sep string) []string {
	labels := strings.Split(value, sep)
	for index, label := range labels {
		labels[index] = strings.TrimSpace(label)
		if labels[index] == "" {
			return nil
		}
	}
	return labels
}

func descriptionTagLabel(opening *bbcode.BBOpeningTag) string {
	label := opening.Value
	// Unquoted labels may contain spaces; the lexer treats later words as arguments.
	name, value, assigned := strings.Cut(strings.TrimPrefix(opening.Raw, "["), "=")
	if assigned && strings.EqualFold(strings.TrimSpace(name), opening.Name) {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "\"") && !strings.HasPrefix(value, "'") {
			label = strings.TrimSpace(strings.TrimSuffix(value, "]"))
		}
	}
	return label
}

func comparisonElement(tag, class string) *bbcode.HTMLTag {
	out := bbcode.NewHTMLTag("")
	out.Name = tag
	out.Attrs["class"] = class
	return out
}

func renderComparison(sources []string, images []comparisonPreviewImage) *bbcode.HTMLTag {
	out := comparisonElement("div", "comparison")

	text := comparisonElement("div", "comparison__text")
	for idx, source := range sources {
		if idx > 0 {
			divider := comparisonElement("span", "comparison__divider")
			divider.AppendChild(bbcode.NewHTMLTag(" vs "))
			text.AppendChild(divider)
		}
		text.AppendChild(bbcode.NewHTMLTag(source))
	}
	text.AppendChild(bbcode.NewHTMLTag(":"))
	out.AppendChild(text)

	details := comparisonElement("details", "comparison__details")

	summary := comparisonElement("summary", "comparison__button")
	summary.AppendChild(bbcode.NewHTMLTag("Show"))
	details.AppendChild(summary)

	screenshots := comparisonElement("ul", "comparison__screenshots")

	columns := len(sources)
	for start := 0; start < len(images); start += columns {
		rowItem := bbcode.NewHTMLTag("")
		rowItem.Name = "li"

		row := comparisonElement("ul", "comparison__row")

		for i := start; i < start+columns; i++ {
			colIndex := i - start
			container := comparisonElement("li", "comparison__image-container")

			figure := comparisonElement("figure", "comparison__figure")

			if start == 0 {
				caption := comparisonElement("figcaption", "comparison__figcaption")
				caption.AppendChild(bbcode.NewHTMLTag(sources[colIndex]))
				figure.AppendChild(caption)
			}

			img := comparisonElement("img", "comparison__image")
			img.Attrs["src"] = images[i].src
			img.Attrs["alt"] = sources[colIndex]
			if images[i].full != images[i].src {
				img.Attrs["class"] += " comparison__image--linked"
			}
			link := bbcode.NewHTMLTag("")
			link.Name = "a"
			link.Attrs["href"] = images[i].full
			link.AppendChild(img)
			figure.AppendChild(link)

			container.AppendChild(figure)
			row.AppendChild(container)
		}

		rowItem.AppendChild(row)
		screenshots.AppendChild(rowItem)
	}

	details.AppendChild(screenshots)
	out.AppendChild(details)
	return out
}
