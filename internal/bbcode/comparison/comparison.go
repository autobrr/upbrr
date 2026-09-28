// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparison

import (
	"regexp"
	"strings"
)

var comparisonTagPattern = regexp.MustCompile(`(?i)\[(?:spoiler(?:\s*=\s*[^\]]*)?|/spoiler|comparison(?:=[^\]]*)?|/comparison)\]`)

// BlockRanges returns complete comparison BBCode blocks, including
// nested spoiler and comparison tags, as byte ranges in source order.
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

func isComparisonSpoilerTag(tag string) bool {
	_, label, found := strings.Cut(tag, "=")
	if !found {
		return false
	}
	label = strings.TrimSpace(strings.TrimSuffix(label, "]"))
	return label == "comparison" || label == "comparisons"
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
