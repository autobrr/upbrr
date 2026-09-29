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
