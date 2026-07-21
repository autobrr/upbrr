// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/frustra/bbcode"
)

func renderBBCode(value string) string {
	compiler := bbcode.NewCompiler(true, true)
	compiler.SetTag("img", compileImg)
	compiler.SetTag("spoiler", compileSpoiler)
	compiler.SetTag("quote", compileQuote)
	compiler.SetTag("list", compileList)
	compiler.SetTag("*", compileListItem)
	compiler.SetTag("li", compileListItem)
	compiler.SetTag("left", compileAlign)
	compiler.SetTag("right", compileAlign)
	compiler.SetTag("center", compileAlign)
	compiler.SetTag("align", compileAlign)
	compiler.SetTag("comparison", compileComparison)
	normalized, codeBlocks := extractCodeBlocks(normalizeBBCode(value))
	return replaceCodeBlockPlaceholders(compiler.Compile(normalized), codeBlocks)
}

func normalizeBBCode(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = normalizeImgTags(value)
	return strings.TrimSpace(value)
}

func compileImg(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	url := ""
	width := ""
	if opening := node.GetOpeningTag(); opening != nil {
		if arg, ok := opening.Args["url"]; ok {
			url = strings.TrimSpace(arg)
		}
		if arg, ok := opening.Args["width"]; ok {
			width = strings.TrimSpace(arg)
		}
	}
	if url == "" {
		url = strings.TrimSpace(bbcode.CompileText(node))
	}
	if url == "" {
		return bbcode.NewHTMLTag(""), true
	}
	img := bbcode.NewHTMLTag("")
	img.Name = "img"
	img.Attrs["src"] = url
	if size := normalizeImageWidth(node.GetOpeningTag().Value, width); size != "" {
		img.Attrs["width"] = size
	}
	return img, true
}

func renderCodeBBCode(value string) string {
	escaped := html.EscapeString(value)
	escaped = renderCodeColorTags(escaped)
	escaped = renderCodeSimplePairTag(escaped, "b", "b")
	escaped = renderCodeSimplePairTag(escaped, "i", "i")
	escaped = renderCodeSimplePairTag(escaped, "u", "u")
	escaped = renderCodeSimplePairTag(escaped, "s", "s")
	return escaped
}

var codeBlockPattern = regexp.MustCompile(`(?is)\[code\]([\s\S]*?)\[/code\]`)

type codeBlockPlaceholder struct {
	token string
	value string
}

func extractCodeBlocks(value string) (string, []codeBlockPlaceholder) {
	placeholderPrefix := nextCodeBlockPlaceholderPrefix(value)
	blocks := make([]codeBlockPlaceholder, 0)
	index := 0
	normalized := codeBlockPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := codeBlockPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		token := placeholderPrefix + strconv.Itoa(index) + "_END"
		index++
		blocks = append(blocks, codeBlockPlaceholder{
			token: token,
			value: parts[1],
		})
		return token
	})
	return normalized, blocks
}

func nextCodeBlockPlaceholderPrefix(value string) string {
	for attempt := 0; ; attempt++ {
		prefix := "UPBRR_CODE_BLOCK_PLACEHOLDER_" + strconv.Itoa(attempt) + "_"
		if !strings.Contains(value, prefix) {
			return prefix
		}
	}
}

func replaceCodeBlockPlaceholders(value string, blocks []codeBlockPlaceholder) string {
	if len(blocks) == 0 {
		return value
	}
	replacements := make([]string, 0, len(blocks)*2)
	for _, block := range blocks {
		replacements = append(replacements, block.token, renderCodeBlockHTML(block.value))
	}
	return strings.NewReplacer(replacements...).Replace(value)
}

func renderCodeBlockHTML(value string) string {
	return "<pre><code>" + renderCodeBBCode(value) + "</code></pre>"
}

var codeColorTagPattern = regexp.MustCompile(`(?is)\[color=([#a-z0-9(),.%\s]+)\]([\s\S]*?)\[/color\]`)

func renderCodeColorTags(value string) string {
	return codeColorTagPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := codeColorTagPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		color := sanitizeColor(parts[1])
		if color == "" {
			return parts[2]
		}
		return `<span style="color: ` + color + `">` + parts[2] + `</span>`
	})
}

func renderCodeSimplePairTag(value string, bbcodeTag string, htmlTag string) string {
	pattern := regexp.MustCompile(`(?is)\[` + regexp.QuoteMeta(bbcodeTag) + `\]([\s\S]*?)\[/` + regexp.QuoteMeta(bbcodeTag) + `\]`)
	return pattern.ReplaceAllString(value, "<"+htmlTag+">$1</"+htmlTag+">")
}

var imgTagPattern = regexp.MustCompile(`(?is)\[img([^\]]*)\]([\s\S]*?)\[/img\]`)
var imgWidthPattern = regexp.MustCompile(`(?i)\bwidth\s*=\s*(\d+)`)

func normalizeImgTags(value string) string {
	return imgTagPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := imgTagPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		url := strings.TrimSpace(parts[2])
		if url == "" {
			return match
		}
		size := parseImageWidth(parts[1])
		if size != "" {
			return "[img=" + size + " url=" + url + "][/img]"
		}
		return "[img url=" + url + "][/img]"
	})
}

func parseImageWidth(attrs string) string {
	trimmed := strings.TrimSpace(attrs)
	if trimmed == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(trimmed, "="); ok {
		return strings.TrimSpace(after)
	}
	match := imgWidthPattern.FindStringSubmatch(trimmed)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func normalizeImageWidth(value string, widthArg string) string {
	for _, candidate := range []string{strings.TrimSpace(value), strings.TrimSpace(widthArg)} {
		if candidate == "" {
			continue
		}
		if size, err := strconv.Atoi(candidate); err == nil && size > 0 {
			return strconv.Itoa(size)
		}
	}
	return ""
}

func compileSpoiler(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	out := bbcode.NewHTMLTag("")
	out.Name = "details"
	summary := bbcode.NewHTMLTag("")
	summary.Name = "summary"
	label := node.GetOpeningTag().Value
	if label == "" {
		label = "Spoiler"
	}
	summary.AppendChild(bbcode.NewHTMLTag(label))
	out.AppendChild(summary)
	return out, true
}

func compileList(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	out := bbcode.NewHTMLTag("")
	listType := strings.TrimSpace(strings.ToLower(node.GetOpeningTag().Value))
	if listType == "1" || listType == "a" || listType == "i" {
		out.Name = "ol"
	} else {
		out.Name = "ul"
	}
	return out, true
}

func compileListItem(_ *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	out := bbcode.NewHTMLTag("")
	out.Name = "li"
	return out, true
}

func compileAlign(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	out := bbcode.NewHTMLTag("")
	out.Name = "div"
	align := resolveAlignment(node)
	if align == "" {
		align = "left"
	}
	out.Attrs["style"] = "text-align: " + align + ";"
	return out, true
}

func resolveAlignment(node *bbcode.BBCodeNode) string {
	opening := node.GetOpeningTag()
	if opening == nil {
		return ""
	}
	for _, candidate := range []string{
		strings.ToLower(strings.TrimSpace(opening.Name)),
		strings.ToLower(strings.TrimSpace(opening.Value)),
		strings.ToLower(strings.TrimSpace(opening.Args["align"])),
	} {
		switch candidate {
		case "left", "right", "center":
			return candidate
		}
	}
	return ""
}

func compileQuote(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	out := bbcode.NewHTMLTag("")
	out.Name = "blockquote"
	label := "Quote"
	if opening := node.GetOpeningTag(); opening != nil {
		if name, ok := opening.Args["name"]; ok && name != "" {
			label = name + " said:"
		} else if opening.Value != "" {
			label = opening.Value + " said:"
		}
	}
	cite := bbcode.NewHTMLTag("")
	cite.Name = "cite"
	cite.AppendChild(bbcode.NewHTMLTag(label))
	out.AppendChild(cite)
	out.AppendChild(bbcode.NewlineTag())
	for _, child := range node.Children {
		out.AppendChild(node.Compiler.CompileTree(child))
	}
	return out, false
}

var comparisonURLPattern = regexp.MustCompile(`(?i)https?://[^\s\]]+\.(?:png|jpe?g|gif|webp)`)

func compileComparison(node *bbcode.BBCodeNode) (*bbcode.HTMLTag, bool) {
	out := bbcode.NewHTMLTag("")
	out.Name = "div"
	out.Attrs["class"] = "comparison"

	sources := parseComparisonSources(node.GetOpeningTag().Value)
	images := parseComparisonImages(bbcode.CompileText(node))

	text := bbcode.NewHTMLTag("")
	text.Name = "div"
	text.Attrs["class"] = "comparison__text"
	if len(sources) == 0 {
		text.AppendChild(bbcode.NewHTMLTag("Comparison:"))
	} else {
		for idx, source := range sources {
			text.AppendChild(bbcode.NewHTMLTag(source))
			if idx < len(sources)-1 {
				text.AppendChild(bbcode.NewHTMLTag(" "))
				divider := bbcode.NewHTMLTag("")
				divider.Name = "span"
				divider.Attrs["class"] = "comparison__divider"
				divider.AppendChild(bbcode.NewHTMLTag("vs"))
				text.AppendChild(divider)
				text.AppendChild(bbcode.NewHTMLTag(" "))
			} else {
				text.AppendChild(bbcode.NewHTMLTag(":"))
			}
		}
	}
	out.AppendChild(text)

	details := bbcode.NewHTMLTag("")
	details.Name = "details"
	details.Attrs["class"] = "comparison__details"

	summary := bbcode.NewHTMLTag("")
	summary.Name = "summary"
	summary.Attrs["class"] = "comparison__button"
	summary.AppendChild(bbcode.NewHTMLTag("Show"))
	details.AppendChild(summary)

	screenshots := bbcode.NewHTMLTag("")
	screenshots.Name = "ul"
	screenshots.Attrs["class"] = "comparison__screenshots"

	columns := len(sources)
	if columns == 0 {
		columns = 1
	}
	for start := 0; start < len(images); start += columns {
		end := min(start+columns, len(images))
		rowItem := bbcode.NewHTMLTag("")
		rowItem.Name = "li"

		row := bbcode.NewHTMLTag("")
		row.Name = "ul"
		row.Attrs["class"] = "comparison__row"

		for i := start; i < end; i++ {
			colIndex := i - start
			container := bbcode.NewHTMLTag("")
			container.Name = "li"
			container.Attrs["class"] = "comparison__image-container"

			figure := bbcode.NewHTMLTag("")
			figure.Name = "figure"
			figure.Attrs["class"] = "comparison__figure"

			if start == 0 && colIndex < len(sources) {
				caption := bbcode.NewHTMLTag("")
				caption.Name = "figcaption"
				caption.Attrs["class"] = "comparison__figcaption"
				caption.AppendChild(bbcode.NewHTMLTag(sources[colIndex]))
				figure.AppendChild(caption)
			}

			img := bbcode.NewHTMLTag("")
			img.Name = "img"
			img.Attrs["class"] = "comparison__image"
			img.Attrs["src"] = images[i]
			figure.AppendChild(img)

			container.AppendChild(figure)
			row.AppendChild(container)
		}

		rowItem.AppendChild(row)
		screenshots.AppendChild(rowItem)
	}

	details.AppendChild(screenshots)
	out.AppendChild(details)
	return out, false
}

func parseComparisonSources(value string) []string {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return nil
	}
	parts := strings.Split(cleaned, ",")
	sources := make([]string, 0, len(parts))
	for _, part := range parts {
		source := strings.TrimSpace(part)
		if source == "" {
			continue
		}
		sources = append(sources, source)
	}
	return sources
}

func parseComparisonImages(value string) []string {
	matches := comparisonURLPattern.FindAllString(value, -1)
	if len(matches) == 0 {
		return nil
	}
	images := make([]string, 0, len(matches))
	for _, match := range matches {
		trimmed := strings.TrimSpace(match)
		if trimmed == "" {
			continue
		}
		images = append(images, trimmed)
	}
	return images
}
