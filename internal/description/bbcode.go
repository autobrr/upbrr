// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/frustra/bbcode"
	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/bbcode/comparison"
)

func renderBBCode(value string) string {
	compiler := bbcode.NewCompiler(true, true)
	compiler.SetTag("img", compileImg)
	compiler.SetTag("spoiler", compileSpoiler)
	compiler.SetTag("hide", compileSpoiler)
	compiler.SetTag("quote", compileQuote)
	compiler.SetTag("list", compileList)
	compiler.SetTag("*", compileListItem)
	compiler.SetTag("li", compileListItem)
	compiler.SetTag("left", compileAlign)
	compiler.SetTag("right", compileAlign)
	compiler.SetTag("center", compileAlign)
	compiler.SetTag("align", compileAlign)
	compiler.SetTag("comparison", compileComparison)
	normalized, blocks := extractLiteralBlocks(strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n")))
	normalized, htmlLiteralBlocks := extractHTMLLiteralBlocks(normalized)
	for i := range htmlLiteralBlocks {
		for _, block := range blocks {
			htmlLiteralBlocks[i].value = strings.ReplaceAll(htmlLiteralBlocks[i].value, block.token, block.raw)
		}
	}
	normalized = normalizeImgTags(normalized)
	normalized, htmlTags := extractHTMLTags(normalized)
	normalized, htmlEntities := extractHTMLEntities(normalized)
	rendered := compiler.Compile(normalized)
	rendered = replaceHTMLTagPlaceholders(rendered, htmlTags)
	rendered = replaceHTMLTagPlaceholders(rendered, htmlEntities)
	rendered = replaceLiteralBlockPlaceholders(rendered, blocks)
	return replaceHTMLTagPlaceholders(rendered, htmlLiteralBlocks)
}

// SplitTrailingSourceAudioSpoiler separates generated audio markup so
// description builders can place it before their screenshot sections.
func SplitTrailingSourceAudioSpoiler(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	const opening = "[spoiler=source_audio]"
	start := strings.LastIndex(trimmed, "\n"+opening)
	switch {
	case start >= 0:
		start++
	case strings.HasPrefix(trimmed, opening):
		start = 0
	default:
		return trimmed, ""
	}
	for _, block := range comparison.BlockRanges(trimmed) {
		if start >= block[0] && start < block[1] {
			return trimmed, ""
		}
	}
	block := strings.TrimSpace(trimmed[start:])
	if !strings.HasSuffix(block, "[/spoiler]") {
		return trimmed, ""
	}
	return strings.TrimSpace(trimmed[:start]), block
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
var preBlockPattern = regexp.MustCompile(`(?is)\[pre\]([\s\S]*?)\[/pre\]`)

type codeBlockPlaceholder struct {
	token string
	value string
	raw   string
	pre   bool
}

func extractLiteralBlocks(value string) (string, []codeBlockPlaceholder) {
	placeholderPrefix := nextCodeBlockPlaceholderPrefix(value)
	htmlTags := htmlTagRanges(value)
	blocks := make([]codeBlockPlaceholder, 0)
	var normalized strings.Builder
	for offset := 0; offset < len(value); {
		code := codeBlockPattern.FindStringSubmatchIndex(value[offset:])
		pre := preBlockPattern.FindStringSubmatchIndex(value[offset:])
		if len(code) == 0 && len(pre) == 0 {
			normalized.WriteString(value[offset:])
			break
		}
		selected := code
		isPre := false
		if len(pre) != 0 && (len(code) == 0 || pre[0] < code[0]) {
			selected = pre
			isPre = true
		}
		if end := htmlTagEndAt(htmlTags, offset+selected[0]); end > 0 {
			normalized.WriteString(value[offset:end])
			offset = end
			continue
		}
		normalized.WriteString(value[offset : offset+selected[0]])
		token := placeholderPrefix + strconv.Itoa(len(blocks)) + "_END"
		blocks = append(blocks, codeBlockPlaceholder{
			token: token,
			value: value[offset+selected[2] : offset+selected[3]],
			raw:   value[offset+selected[0] : offset+selected[1]],
			pre:   isPre,
		})
		normalized.WriteString(token)
		offset += selected[1]
	}
	return normalized.String(), blocks
}

type htmlTagRange struct{ start, end int }

func htmlTagRanges(value string) []htmlTagRange {
	tokenizer := xhtml.NewTokenizer(strings.NewReader(value))
	ranges := make([]htmlTagRange, 0)
	offset := 0
	for kind := tokenizer.Next(); kind != xhtml.ErrorToken; kind = tokenizer.Next() {
		raw := tokenizer.Raw()
		if kind == xhtml.StartTagToken || kind == xhtml.EndTagToken || kind == xhtml.SelfClosingTagToken ||
			kind == xhtml.CommentToken || kind == xhtml.DoctypeToken {
			ranges = append(ranges, htmlTagRange{start: offset, end: offset + len(raw)})
		}
		offset += len(raw)
	}
	return ranges
}

func htmlTagEndAt(ranges []htmlTagRange, offset int) int {
	for _, tag := range ranges {
		if offset < tag.start {
			break
		}
		if offset < tag.end {
			return tag.end
		}
	}
	return 0
}

func nextCodeBlockPlaceholderPrefix(value string) string {
	for attempt := 0; ; attempt++ {
		prefix := "UPBRR_CODE_BLOCK_PLACEHOLDER_" + strconv.Itoa(attempt) + "_"
		if !strings.Contains(value, prefix) {
			return prefix
		}
	}
}

func replaceLiteralBlockPlaceholders(value string, blocks []codeBlockPlaceholder) string {
	if len(blocks) == 0 {
		return value
	}
	replacements := make([]string, 0, len(blocks)*2)
	for _, block := range blocks {
		rendered := renderCodeBlockHTML(block.value)
		if block.pre {
			rendered = "<pre>" + html.EscapeString(block.value) + "</pre>"
		}
		replacements = append(replacements, block.token, rendered)
	}
	return strings.NewReplacer(replacements...).Replace(value)
}

func renderCodeBlockHTML(value string) string {
	return "<pre><code>" + renderCodeBBCode(value) + "</code></pre>"
}

func extractHTMLTags(value string) (string, []codeBlockPlaceholder) {
	placeholderPrefix := "UPBRR_HTML_TAG_PLACEHOLDER_"
	for strings.Contains(value, placeholderPrefix) {
		placeholderPrefix += "_"
	}
	tags := make([]codeBlockPlaceholder, 0)
	var normalized strings.Builder
	tokenizer := xhtml.NewTokenizer(strings.NewReader(value))
	offset := 0
	for kind := tokenizer.Next(); kind != xhtml.ErrorToken; kind = tokenizer.Next() {
		raw := string(tokenizer.Raw())
		offset += len(raw)
		switch kind {
		case xhtml.StartTagToken, xhtml.EndTagToken, xhtml.SelfClosingTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
			token := placeholderPrefix + strconv.Itoa(len(tags)) + "_END"
			tags = append(tags, codeBlockPlaceholder{token: token, value: raw})
			normalized.WriteString(token)
		case xhtml.TextToken:
			normalized.WriteString(raw)
		case xhtml.ErrorToken:
		}
	}
	normalized.WriteString(value[offset:])
	return normalized.String(), tags
}

func extractHTMLLiteralBlocks(value string) (string, []codeBlockPlaceholder) {
	placeholderPrefix := "UPBRR_HTML_LITERAL_PLACEHOLDER_"
	for strings.Contains(value, placeholderPrefix) {
		placeholderPrefix += "_"
	}
	blocks := make([]codeBlockPlaceholder, 0)
	var normalized strings.Builder
	tokenizer := xhtml.NewTokenizer(strings.NewReader(value))
	offset, copied, start, depth := 0, 0, 0, 0
	active := ""
	for kind := tokenizer.Next(); kind != xhtml.ErrorToken; kind = tokenizer.Next() {
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)
		if kind != xhtml.StartTagToken && kind != xhtml.EndTagToken {
			continue
		}
		name, _ := tokenizer.TagName()
		tag := strings.ToLower(string(name))
		if active == "" {
			if kind == xhtml.StartTagToken && (tag == "pre" || tag == "code") {
				active, start, depth = tag, tokenStart, 1
			}
			continue
		}
		if tag != active {
			continue
		}
		if kind == xhtml.StartTagToken {
			depth++
		} else if depth--; depth == 0 {
			normalized.WriteString(value[copied:start])
			token := placeholderPrefix + strconv.Itoa(len(blocks)) + "_END"
			blocks = append(blocks, codeBlockPlaceholder{token: token, value: value[start:offset]})
			normalized.WriteString(token)
			copied, active = offset, ""
		}
	}
	if active != "" {
		normalized.WriteString(value[copied:start])
		token := placeholderPrefix + strconv.Itoa(len(blocks)) + "_END"
		blocks = append(blocks, codeBlockPlaceholder{token: token, value: value[start:]})
		normalized.WriteString(token)
		return normalized.String(), blocks
	}
	normalized.WriteString(value[copied:])
	return normalized.String(), blocks
}

func replaceHTMLTagPlaceholders(value string, tags []codeBlockPlaceholder) string {
	for _, tag := range tags {
		value = strings.ReplaceAll(value, tag.token, tag.value)
	}
	return value
}

var htmlEntityPattern = regexp.MustCompile(`&(?:#[0-9]+|#x[0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]+);`)

func extractHTMLEntities(value string) (string, []codeBlockPlaceholder) {
	placeholderPrefix := "UPBRR_HTML_ENTITY_PLACEHOLDER_"
	for strings.Contains(value, placeholderPrefix) {
		placeholderPrefix += "_"
	}
	entities := make([]codeBlockPlaceholder, 0)
	normalized := htmlEntityPattern.ReplaceAllStringFunc(value, func(match string) string {
		token := placeholderPrefix + strconv.Itoa(len(entities)) + "_END"
		entities = append(entities, codeBlockPlaceholder{token: token, value: match})
		return token
	})
	return normalized, entities
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
