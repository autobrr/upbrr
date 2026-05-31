// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

// SourceSize returns the total size of the content in bytes.
// For disc sources, it walks the entire tree; otherwise it sums the file list or video path.
func SourceSize(ctx context.Context, sourcePath, discType string, fileList []string, videoPath string) (int64, error) {
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return 0, fmt.Errorf("filesystem: empty path: %w", internalerrors.ErrInvalidInput)
	}

	if strings.TrimSpace(discType) != "" {
		return discTreeSize(ctx, trimmed)
	}

	paths := make([]string, 0, len(fileList)+1)
	for _, value := range fileList {
		trimmedFile := strings.TrimSpace(value)
		if trimmedFile == "" {
			continue
		}
		paths = append(paths, trimmedFile)
	}
	if len(paths) == 0 {
		if trimmedVideo := strings.TrimSpace(videoPath); trimmedVideo != "" {
			paths = append(paths, trimmedVideo)
		}
	}

	var total int64
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
	}

	return total, nil
}

func discTreeSize(ctx context.Context, root string) (int64, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("filesystem: path %q: %w", root, internalerrors.ErrNotFound)
		}
		return 0, fmt.Errorf("filesystem: path %q: %w", root, err)
	}
	if !info.IsDir() {
		return 0, nil
	}

	var total int64
	err = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("filesystem: size walk interrupted: %w", err)
		}
		return 0, fmt.Errorf("filesystem: size walk: %w", err)
	}

	return total, nil
}
