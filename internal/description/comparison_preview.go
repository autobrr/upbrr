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

var comparisonURLSeparator = regexp.MustCompile(`(?i),\s*(https?://)`)

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
	if len(content) == 1 && content[0].ClosingTag != nil && resolveAlignment(content[0]) == "center" {
		content = content[0].Children
		// With tagged images, distinguish explicit complete rows from the
		// native one-image-per-line form. Do not flatten mixed row sizes.
		if len(content) != 1 || content[0].ID != bbcode.TEXT {
			if rows := comparisonRows(content, columns); len(rows) > 0 {
				return rows
			}
			if images := comparisonRows(content, 1); len(images)%columns == 0 {
				return images
			}
			return nil
		}
	}

	var images []comparisonPreviewImage
	for _, child := range content {
		switch {
		case child.ID == bbcode.TEXT:
			for value := range strings.FieldsSeq(comparisonURLSeparator.ReplaceAllString(child.Value.(string), " ${1}")) {
				if !absoluteComparisonURL(value) {
					return nil
				}
				images = append(images, comparisonPreviewImage{src: value, full: value})
			}
		case child.ClosingTag != nil && resolveAlignment(child) == "center":
			row := comparisonRows(child.Children, columns)
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

// The tracker conversions label sources and delimit each complete row with a
// newline. Mixed content or incomplete rows are not evidence of a comparison.
func compileCenteredComparison(node *bbcode.BBCodeNode) *bbcode.HTMLTag {
	if node.ClosingTag == nil || resolveAlignment(node) != "center" || len(node.Children) == 0 {
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
	if len(sources) < 2 {
		return nil
	}
	if images := comparisonRows(node.Children[1:], len(sources)); len(images) > 0 {
		return renderComparison(sources, images)
	}
	return nil
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
	if len(children) > 0 && resolveAlignment(children[0]) == "center" {
		header := children[0]
		if header.ClosingTag == nil || len(header.Children) != 1 || header.Children[0].ID != bbcode.TEXT ||
			!slices.Equal(sources, comparisonLabels(bbcode.CompileText(header), " | ")) {
			return nil
		}
		children = children[1:]
	}
	if images := comparisonRows(children, len(sources)); len(images) > 0 {
		return renderComparison(sources, images)
	}
	return nil
}

func comparisonRows(nodes []*bbcode.BBCodeNode, columns int) []comparisonPreviewImage {
	var images []comparisonPreviewImage
	count := 0
	for _, node := range nodes {
		if isBlankComparisonText(node) {
			if count > 0 && strings.Contains(bbcode.CompileText(node), "\n") {
				if count != columns {
					return nil
				}
				count = 0
			}
			continue
		}
		image, ok := comparisonImage(node)
		if !ok || count == columns {
			return nil
		}
		images = append(images, image)
		count++
	}
	if count > 0 && count != columns {
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
	if opening.Name == "url" && len(node.Children) == 1 {
		full = opening.Value
		if !absoluteComparisonURL(full) {
			return comparisonPreviewImage{}, false
		}
		node = node.Children[0]
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
	return node.ID == bbcode.TEXT && strings.TrimSpace(node.Value.(string)) == ""
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
	if _, value, ok := strings.Cut(opening.Raw, "="); ok {
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
