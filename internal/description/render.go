// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var bbcodeOpeningPattern = regexp.MustCompile(
	`(?i)\[(b|i|u|s|color|size|font|url|img|spoiler|hide|quote|list|li|left|right|center|align|comparison|mediainfo|code|pre)` +
		`(?:=[^\]]*|[ \t]+[^\]]*)?\]`,
)

// Render converts BBCode, MediaInfo blocks, or existing HTML into HTML that has
// passed the package's element, attribute, class, style, and URL allowlists.
// Blank input returns an empty string.
func Render(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if rendered, ok := renderBBCodeWithMediaInfo(trimmed); ok {
		return sanitizeHTML(rendered)
	}
	text, hasHTML := textOutsideHTMLTags(trimmed)
	if hasHTML && !looksLikeBBCode(text) {
		return sanitizeHTML(trimmed)
	}
	return sanitizeHTML(renderBBCode(trimmed))
}

// RenderWithImagePreviews substitutes known hosted thumbnails only in rendered
// image sources. The raw description and full-size link targets remain intact.
func RenderWithImagePreviews(raw string, previews map[string]string) string {
	rendered := Render(raw)
	if rendered == "" || len(previews) == 0 {
		return rendered
	}
	fragment, err := xhtml.ParseFragment(strings.NewReader(rendered), fragmentContext())
	if err != nil {
		return rendered
	}
	changed := false
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "img" {
			for idx := range node.Attr {
				attr := &node.Attr[idx]
				if attr.Key != "src" {
					continue
				}
				if preview, ok := previews[attr.Val]; ok {
					if safe, allowed := sanitizeURL(preview, true); allowed && safe != attr.Val {
						attr.Val = safe
						changed = true
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	for _, node := range fragment {
		visit(node)
	}
	if !changed {
		return rendered
	}
	var output strings.Builder
	for _, node := range fragment {
		sanitizeNode(&output, node)
	}
	return output.String()
}

func textOutsideHTMLTags(value string) (string, bool) {
	var text strings.Builder
	tokenizer := xhtml.NewTokenizer(strings.NewReader(value))
	hasHTML := false
	literalTags := make([]string, 0)
	for kind := tokenizer.Next(); kind != xhtml.ErrorToken; kind = tokenizer.Next() {
		switch kind {
		case xhtml.StartTagToken:
			hasHTML = true
			name, _ := tokenizer.TagName()
			if tag := strings.ToLower(string(name)); tag == "script" || tag == "style" || tag == "pre" || tag == "code" {
				literalTags = append(literalTags, tag)
			}
		case xhtml.EndTagToken:
			hasHTML = true
			name, _ := tokenizer.TagName()
			if len(literalTags) > 0 && strings.EqualFold(string(name), literalTags[len(literalTags)-1]) {
				literalTags = literalTags[:len(literalTags)-1]
			}
		case xhtml.SelfClosingTagToken:
			hasHTML = true
		case xhtml.TextToken:
			if len(literalTags) == 0 {
				text.Write(tokenizer.Raw())
			}
		case xhtml.CommentToken, xhtml.DoctypeToken, xhtml.ErrorToken:
		}
	}
	return text.String(), hasHTML
}

func looksLikeBBCode(value string) bool {
	lower := strings.ToLower(value)
	for _, opening := range bbcodeOpeningPattern.FindAllStringSubmatch(lower, -1) {
		if strings.Contains(lower, "[/"+opening[1]+"]") {
			return true
		}
	}
	return false
}
