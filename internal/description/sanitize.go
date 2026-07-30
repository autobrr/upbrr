// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"fmt"
	stdhtml "html"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
	xatom "golang.org/x/net/html/atom"
)

type tagPolicy struct {
	selfClosing bool
}

var allowedTags = map[string]tagPolicy{
	"a":          {},
	"article":    {},
	"b":          {},
	"blockquote": {},
	"br":         {selfClosing: true},
	"cite":       {},
	"code":       {},
	"details":    {},
	"div":        {},
	"dd":         {},
	"dl":         {},
	"dt":         {},
	"em":         {},
	"figcaption": {},
	"figure":     {},
	"h1":         {},
	"h2":         {},
	"h3":         {},
	"h4":         {},
	"hr":         {selfClosing: true},
	"i":          {},
	"img":        {selfClosing: true},
	"li":         {},
	"ol":         {},
	"p":          {},
	"pre":        {},
	"section":    {},
	"span":       {},
	"strong":     {},
	"summary":    {},
	"table":      {},
	"tbody":      {},
	"td":         {},
	"th":         {},
	"thead":      {},
	"tr":         {},
	"u":          {},
	"ul":         {},
	"s":          {},
}

var allowedAttrs = map[string]map[string]bool{
	"a":          {"href": true, "title": true},
	"blockquote": {"style": true, "align": true},
	"details":    {"class": true},
	"div": {
		"style": true,
		"class": true,
		"align": true,
	},
	"figcaption": {"class": true},
	"figure":     {"class": true},
	"img": {
		"src":   true,
		"alt":   true,
		"title": true,
		"class": true,
		"width": true,
	},
	"p":       {"style": true, "align": true},
	"section": {"class": true},
	"span": {
		"style": true,
		"class": true,
		"align": true,
	},
	"summary": {"class": true},
	"ul":      {"class": true},
	"li":      {"class": true},
}

func sanitizeHTML(input string) string {
	fragment, err := xhtml.ParseFragment(strings.NewReader(input), fragmentContext())
	if err != nil && len(fragment) == 0 {
		if reparsed, ok := tryParseUnescapedHTML(input); ok {
			fragment = reparsed
		} else {
			return stdhtml.EscapeString(input)
		}
	}
	if !hasElementNodes(fragment) {
		if reparsed, ok := tryParseUnescapedHTML(input); ok {
			fragment = reparsed
		}
	}
	var builder strings.Builder
	for _, node := range fragment {
		sanitizeNode(&builder, node)
	}
	return builder.String()
}

func tryParseUnescapedHTML(input string) ([]*xhtml.Node, bool) {
	if !strings.Contains(input, "&lt;") && !strings.Contains(input, "&gt;") {
		return nil, false
	}
	unescaped := stdhtml.UnescapeString(input)
	fragment, err := xhtml.ParseFragment(strings.NewReader(unescaped), fragmentContext())
	if err != nil || len(fragment) == 0 {
		return nil, false
	}
	if !hasElementNodes(fragment) {
		return nil, false
	}
	return fragment, true
}

func fragmentContext() *xhtml.Node {
	return &xhtml.Node{
		Type:     xhtml.ElementNode,
		Data:     "div",
		DataAtom: xatom.Div,
	}
}

func hasElementNodes(nodes []*xhtml.Node) bool {
	for _, node := range nodes {
		if node.Type == xhtml.ElementNode {
			return true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.ElementNode {
				return true
			}
		}
	}
	return false
}

func sanitizeNode(builder *strings.Builder, node *xhtml.Node) {
	//nolint:exhaustive // Unsupported node types are sanitized by recursively visiting their children.
	switch node.Type {
	case xhtml.TextNode:
		builder.WriteString(stdhtml.EscapeString(node.Data))
	case xhtml.ElementNode:
		tag := strings.ToLower(node.Data)
		if tag == "script" || tag == "style" {
			return
		}
		policy, ok := allowedTags[tag]
		if !ok {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				sanitizeNode(builder, child)
			}
			return
		}
		builder.WriteByte('<')
		builder.WriteString(tag)
		for _, attr := range node.Attr {
			if value, ok := sanitizeAttr(tag, attr.Key, attr.Val); ok {
				fmt.Fprintf(builder, " %s=\"%s\"", attr.Key, stdhtml.EscapeString(value))
			}
		}
		if policy.selfClosing {
			builder.WriteString(" />")
			return
		}
		builder.WriteByte('>')
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			sanitizeNode(builder, child)
		}
		builder.WriteString("</")
		builder.WriteString(tag)
		builder.WriteByte('>')
	case xhtml.CommentNode:
		return
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			sanitizeNode(builder, child)
		}
	}
}

func sanitizeAttr(tag string, key string, value string) (string, bool) {
	attrs, ok := allowedAttrs[tag]
	if !ok {
		return "", false
	}
	key = strings.ToLower(key)
	if !attrs[key] {
		return "", false
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	if key == "href" {
		return sanitizeURL(trimmed, false)
	}
	if key == "src" {
		return sanitizeURL(trimmed, true)
	}
	if key == "style" {
		return sanitizeStyle(trimmed)
	}
	if key == "align" {
		return sanitizeAlign(trimmed)
	}
	if key == "class" {
		return sanitizeClass(trimmed)
	}
	return trimmed, true
}

func sanitizeAlign(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "left", "right", "center":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func sanitizeURL(value string, allowDataImage bool) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return "", false
	}
	if strings.HasPrefix(lower, "javascript:") {
		return "", false
	}
	if strings.HasPrefix(lower, "data:") {
		if allowDataImage && strings.HasPrefix(lower, "data:image/") {
			return value, true
		}
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	if parsed.Scheme == "" || parsed.Scheme == "http" || parsed.Scheme == "https" {
		return value, true
	}
	return "", false
}

func sanitizeStyle(value string) (string, bool) {
	parts := strings.Split(value, ";")
	var allowed []string
	for _, part := range parts {
		pair := strings.SplitN(part, ":", 2)
		if len(pair) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(pair[0]))
		val := strings.TrimSpace(pair[1])
		if val == "" {
			continue
		}
		switch key {
		case "text-align":
			lower := strings.ToLower(val)
			if lower == "left" || lower == "right" || lower == "center" {
				allowed = append(allowed, "text-align: "+lower)
			}
		case "color":
			if sanitized := sanitizeColor(val); sanitized != "" {
				allowed = append(allowed, "color: "+sanitized)
			}
		}
	}
	if len(allowed) == 0 {
		return "", false
	}
	return strings.Join(allowed, "; "), true
}

func sanitizeColor(value string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return ""
	}
	for _, r := range clean {
		switch {
		case r == '#' || r == ',' || r == '.' || r == '(' || r == ')' || r == '%':
			continue
		case r >= '0' && r <= '9':
			continue
		case r >= 'a' && r <= 'z':
			continue
		case r >= 'A' && r <= 'Z':
			continue
		default:
			return ""
		}
	}
	return clean
}

func sanitizeClass(value string) (string, bool) {
	parts := strings.Fields(value)
	var allowed []string
	for _, part := range parts {
		if strings.HasPrefix(part, "size") && len(part) == 5 {
			size := part[4]
			if size >= '1' && size <= '7' {
				allowed = append(allowed, part)
			}
			continue
		}
		if isAllowedComparisonClass(part) {
			allowed = append(allowed, part)
			continue
		}
		if isAllowedMediaInfoClass(part) {
			allowed = append(allowed, part)
		}
	}
	if len(allowed) == 0 {
		return "", false
	}
	return strings.Join(allowed, " "), true
}

func isAllowedMediaInfoClass(value string) bool {
	switch value {
	case "mediainfo-preview",
		"mediainfo",
		"mediainfo__filename",
		"mediainfo__general",
		"mediainfo__video",
		"mediainfo__audio",
		"mediainfo__subtitles",
		"mediainfo__raw":
		return true
	default:
		return false
	}
}

func isAllowedComparisonClass(value string) bool {
	switch value {
	case "comparison",
		"comparison__text",
		"comparison__divider",
		"comparison__button",
		"comparison__details",
		"comparison__screenshots",
		"comparison__row",
		"comparison__image-container",
		"comparison__image-container--hidden",
		"comparison__figure",
		"comparison__image",
		"comparison__image--hidden",
		"comparison__figcaption":
		return true
	default:
		return false
	}
}
