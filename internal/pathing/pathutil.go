// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package pathing provides portable host-filesystem path operations.
package pathing

import (
	"path" //nolint:depguard // Normalizes slash-style metadata paths, not local filesystem paths.
	"path/filepath"
	"runtime"
	"strings"
)

// Clean normalizes path-like strings for cross-platform parsing by treating
// both slash styles as separators. Use this for metadata/source-path parsing,
// not filesystem operations.
// Clean normalizes a host-filesystem path while preserving an empty input.
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
// Base returns the final element of a normalized host-filesystem path.
func Base(value string) string {
	cleaned := Clean(value)
	if cleaned == "" {
		return ""
	}
	return path.Base(cleaned)
}

// IsWithinRoot reports whether target resolves to root or a descendant of root.
// It first checks lexical paths, then repeats the check with symlinks resolved
// through the nearest existing path prefix so missing child paths under a
// symlink cannot escape the root.
// IsWithinRoot reports whether target resolves to root or one of its descendants.
func IsWithinRoot(root string, target string) bool {
	rootAbs, ok := cleanAbs(root)
	if !ok {
		return false
	}
	targetAbs, ok := cleanAbs(target)
	if !ok {
		return false
	}
	if !isWithinCleanRoot(rootAbs, targetAbs) {
		return false
	}

	rootReal, rootOK := evalExistingPrefix(rootAbs)
	targetReal, targetOK := evalExistingPrefix(targetAbs)
	if !rootOK || !targetOK {
		return true
	}
	return isWithinCleanRoot(rootReal, targetReal)
}

// SamePath compares local filesystem paths with the host OS path semantics.
// SamePath reports whether two paths identify the same filesystem location.
func SamePath(left string, right string) bool {
	leftAbs, leftOK := cleanAbs(left)
	rightAbs, rightOK := cleanAbs(right)
	if !leftOK || !rightOK {
		return false
	}
	if sameCleanPath(leftAbs, rightAbs) {
		return true
	}
	leftReal, leftRealOK := evalExistingPrefix(leftAbs)
	rightReal, rightRealOK := evalExistingPrefix(rightAbs)
	return leftRealOK && rightRealOK && sameCleanPath(leftReal, rightReal)
}

func sameCleanPath(left string, right string) bool {
	left = normalizeCleanLocalPath(left)
	right = normalizeCleanLocalPath(right)
	if filepath.VolumeName(left) != filepath.VolumeName(right) {
		return false
	}
	return left == right
}

func normalizeCleanLocalPath(value string) string {
	cleaned := filepath.Clean(value)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func cleanAbs(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	abs, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

func isWithinCleanRoot(root string, target string) bool {
	root = normalizeCleanLocalPath(root)
	target = normalizeCleanLocalPath(target)
	if filepath.VolumeName(root) != filepath.VolumeName(target) {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func evalExistingPrefix(value string) (string, bool) {
	cleaned := filepath.Clean(value)
	current := cleaned
	missing := make([]string, 0)
	for {
		resolved, ok := resolveExistingPath(current)
		if ok {
			for idx := len(missing) - 1; idx >= 0; idx-- {
				resolved = filepath.Join(resolved, missing[idx])
			}
			return filepath.Clean(resolved), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
