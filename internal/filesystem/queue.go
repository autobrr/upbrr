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

var queueVideoExts = map[string]struct{}{
	".mkv": {},
	".mp4": {},
	".ts":  {},
}

// GatherQueuePaths returns sorted absolute host paths for first-level upload
// candidates. A file root is returned directly; directory children are kept
// only when they are video files or directories containing video or disc data.
func GatherQueuePaths(ctx context.Context, root string) ([]string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return nil, fmt.Errorf("filesystem: empty queue path: %w", internalerrors.ErrInvalidInput)
	}

	absRoot, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("filesystem: resolve queue path: %w", err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("filesystem: queue path %q: %w", absRoot, internalerrors.ErrNotFound)
		}
		return nil, fmt.Errorf("filesystem: queue path %q: %w", absRoot, err)
	}

	if !info.IsDir() {
		return []string{absRoot}, nil
	}

	entries, err := os.ReadDir(absRoot)
	if err != nil {
		return nil, fmt.Errorf("filesystem: read queue path %q: %w", absRoot, err)
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		entryPath := filepath.Join(absRoot, entry.Name())
		if entry.IsDir() {
			include, includeErr := shouldIncludeQueueDirectory(ctx, entryPath)
			if includeErr != nil {
				return nil, includeErr
			}
			if include {
				paths = append(paths, entryPath)
			}
			continue
		}

		if isQueueVideoFile(entry.Name()) {
			paths = append(paths, entryPath)
		}
	}

	sort.Strings(paths)
	return paths, nil
}

// LimitQueuePaths returns a defensive copy capped at limit. A non-positive
// limit returns all paths.
func LimitQueuePaths(paths []string, limit int) []string {
	if limit <= 0 || len(paths) <= limit {
		return append([]string{}, paths...)
	}
	return append([]string{}, paths[:limit]...)
}

func shouldIncludeQueueDirectory(ctx context.Context, dirPath string) (bool, error) {
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false, fmt.Errorf("filesystem: read queue dir %q: %w", dirPath, err)
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		if entry.IsDir() {
			name := strings.ToUpper(strings.TrimSpace(entry.Name()))
			if name == "BDMV" || name == "VIDEO_TS" || name == "HVDVD_TS" {
				return true, nil
			}
			continue
		}
		if isQueueVideoFile(entry.Name()) {
			return true, nil
		}
	}

	return false, nil
}

func isQueueVideoFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	_, ok := queueVideoExts[ext]
	return ok
}
