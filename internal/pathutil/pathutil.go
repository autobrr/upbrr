// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pathutil

import (
	"path" //nolint:depguard // Normalizes slash-style metadata paths, not local filesystem paths.
	"strings"
)

// Clean normalizes path-like strings for cross-platform parsing by treating
// both slash styles as separators. Use this for metadata/source-path parsing,
// not filesystem operations.
func Clean(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
}

// Base returns the last path element while treating both slash styles as
// separators. Use this for parsing stored path strings that may originate from
// another OS.
func Base(value string) string {
	cleaned := Clean(value)
	if cleaned == "" {
		return ""
	}
	return path.Base(cleaned)
}
