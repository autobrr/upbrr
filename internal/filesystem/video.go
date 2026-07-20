// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

var videoExts = map[string]struct{}{
	".mkv": {},
	".mp4": {},
	".ts":  {},
}

// SupportedVideoExtensions returns a sorted copy of recognized lowercase extensions.
func SupportedVideoExtensions() []string {
	exts := make([]string, 0, len(videoExts))
	for ext := range videoExts {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// IsVideoFile reports whether name has a recognized extension, ignoring case.
func IsVideoFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(strings.ToLower(name)))
	_, ok := videoExts[ext]
	return ok
}

// CollectVideoFiles returns a selected video and candidate list for a non-disc
// source. A file source is returned regardless of extension. A directory scan
// is non-recursive, includes recognized video extensions, excludes sample-like
// names except !sample, and sorts candidates by path. Selection uses the first
// candidate unless preferLargest is set.
func CollectVideoFiles(ctx context.Context, source string, preferLargest bool) (string, []string, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return "", nil, fmt.Errorf("filesystem: empty path: %w", internalerrors.ErrInvalidInput)
	}

	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", nil, fmt.Errorf("filesystem: resolve path: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("filesystem: path %q: %w", abs, internalerrors.ErrNotFound)
		}
		return "", nil, fmt.Errorf("filesystem: path %q: %w", abs, err)
	}

	if !info.IsDir() {
		return abs, []string{abs}, nil
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", nil, fmt.Errorf("filesystem: read dir %q: %w", abs, err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return "", nil, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if !IsVideoFile(lower) {
			continue
		}
		if strings.Contains(lower, "sample") && !strings.Contains(lower, "!sample") {
			continue
		}
		files = append(files, filepath.Join(abs, name))
	}

	if len(files) == 0 {
		return "", nil, fmt.Errorf("filesystem: no video files in %q: %w", abs, internalerrors.ErrNotFound)
	}

	sort.Strings(files)
	selected := files[0]
	if preferLargest {
		selected = largestFile(files)
	}

	return selected, files, nil
}

func largestFile(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	largest := paths[0]
	largestSize := int64(-1)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() > largestSize {
			largestSize = info.Size()
			largest = path
		}
	}
	return largest
}
