// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparison

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/frustra/bbcode"
)

var imageURLPattern = regexp.MustCompile(`(?i)https?://[^\s\[\]<>]+`)

// Block retains the original markup and its row-major source images.
type Block struct {
	BBCode  string
	Sources []string
	Images  []string
}

// Sources returns the ordered source columns from the raw comparison opening tag.
// The parser's Value stops at unquoted spaces, which source names may contain.
func Sources(openingTag string) []string {
	_, value, found := strings.Cut(strings.TrimSuffix(openingTag, "]"), "=")
	if !found {
		return nil
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		value = value[1 : len(value)-1]
	}
	var result []string
	for source := range strings.SplitSeq(value, ",") {
		if source = strings.TrimSpace(source); source != "" {
			result = append(result, source)
		}
	}
	return result
}

// Images returns ordered source references without merging adjacent image nodes.
// Image-file links take precedence over their nested thumbnail images.
func Images(node *bbcode.BBCodeNode) []string {
	var result []string
	var visit func(*bbcode.BBCodeNode)
	visit = func(node *bbcode.BBCodeNode) {
		if opening := node.GetOpeningTag(); opening != nil {
			switch opening.Name {
			case "img":
				raw := opening.Args["url"]
				if raw == "" {
					raw = bbcode.CompileText(node)
				}
				if source := imageReference(html.UnescapeString(raw), false); source != "" {
					result = append(result, source)
				}
				return
			case "url":
				if source := imageReference(html.UnescapeString(opening.Value), true); source != "" {
					result = append(result, source)
					return
				}
			}
		}
		if value, ok := node.Value.(string); ok {
			for _, raw := range imageURLPattern.FindAllString(value, -1) {
				for candidate := range strings.FieldsSeq(html.UnescapeString(raw)) {
					if source := imageReference(candidate, true); source != "" {
						result = append(result, source)
					}
				}
			}
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(node)
	return result
}

func imageReference(raw string, requireImageSuffix bool) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if requireImageSuffix {
		path := strings.ToLower(parsed.Path)
		if !strings.HasSuffix(path, ".png") && !strings.HasSuffix(path, ".jpg") && !strings.HasSuffix(path, ".jpeg") &&
			!strings.HasSuffix(path, ".gif") && !strings.HasSuffix(path, ".webp") {
			return ""
		}
	}
	return raw
}

// Parse returns complete comparison nodes using the same parser as the preview.
// Callers exclude literal examples before parsing or replacing the original markup.
func Parse(value string) []Block {
	var result []Block
	var visit func(*bbcode.BBCodeNode)
	visit = func(node *bbcode.BBCodeNode) {
		if opening := node.GetOpeningTag(); opening != nil && opening.Name == "comparison" && node.ClosingTag != nil {
			raw := html.UnescapeString(bbcode.CompileRaw(node).Compile(false))
			if strings.Contains(value, raw) {
				result = append(result, Block{
					BBCode:  raw,
					Sources: Sources(opening.Raw),
					Images:  Images(node),
				})
			}
			return
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(bbcode.Parse(bbcode.Lex(value)))
	return result
}

var comparisonTagPattern = regexp.MustCompile(`(?i)\[(?:spoiler(?:\s*=\s*[^\]]*)?|/spoiler|comparison(?:=[^\]]*)?|/comparison)\]`)

// BlockRanges returns complete comparison BBCode blocks, including
// nested spoiler and comparison tags, as half-open byte ranges in source order.
func BlockRanges(value string) [][2]int {
	var ranges [][2]int
	var stack []string
	start := -1
	for _, match := range comparisonTagPattern.FindAllStringIndex(value, -1) {
		tag := strings.ToLower(value[match[0]:match[1]])
		kind := "comparison"
		if strings.HasPrefix(tag, "[spoiler") || tag == "[/spoiler]" {
			kind = "spoiler"
		}
		closing := strings.HasPrefix(tag, "[/")
		if start < 0 {
			if closing || kind == "spoiler" && !isComparisonSpoilerTag(tag) {
				continue
			}
			start = match[0]
			stack = append(stack, kind)
			continue
		}
		if !closing {
			stack = append(stack, kind)
			continue
		}
		if len(stack) == 0 || stack[len(stack)-1] != kind {
			continue
		}
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			ranges = append(ranges, [2]int{start, match[1]})
			start = -1
		}
	}
	return ranges
}

// MapOutsideBlocks applies transform to the text around complete comparison
// blocks while retaining each block byte for byte. It also transforms an empty
// fragment before or after a block, as it does for input without any blocks.
func MapOutsideBlocks(value string, transform func(string) string) string {
	blocks := BlockRanges(value)
	if len(blocks) == 0 {
		return transform(value)
	}
	var result strings.Builder
	last := 0
	for _, block := range blocks {
		result.WriteString(transform(value[last:block[0]]))
		result.WriteString(value[block[0]:block[1]])
		last = block[1]
	}
	result.WriteString(transform(value[last:]))
	return result.String()
}

func isComparisonSpoilerTag(tag string) bool {
	_, label, found := strings.Cut(tag, "=")
	if !found {
		return false
	}
	label = strings.TrimSpace(strings.TrimSuffix(label, "]"))
	return label == "comparison" || label == "comparisons" || label == "comps"
}

// RemoveComparisonBlocks strips complete comparison blocks without changing
// the BBCode before or after them.
func RemoveComparisonBlocks(value string) string {
	ranges := BlockRanges(value)
	if len(ranges) == 0 {
		return value
	}
	var out strings.Builder
	last := 0
	for _, block := range ranges {
		out.WriteString(value[last:block[0]])
		last = block[1]
	}
	out.WriteString(value[last:])
	return out.String()
}
