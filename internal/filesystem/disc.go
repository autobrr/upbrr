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

var errDiscFound = errors.New("disc type found")

// DetectDiscType scans the path for disc folder markers.
// It returns "BDMV", "DVD", "HDDVD", or "" when no disc type is found.
func DetectDiscType(ctx context.Context, root string) (string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "", fmt.Errorf("filesystem: empty path: %w", internalerrors.ErrInvalidInput)
	}

	info, err := os.Stat(trimmed)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("filesystem: path %q: %w", trimmed, internalerrors.ErrNotFound)
		}
		return "", fmt.Errorf("filesystem: path %q: %w", trimmed, err)
	}
	if !info.IsDir() {
		return "", nil
	}

	discType := ""
	err = filepath.WalkDir(trimmed, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.EqualFold(name, "BDMV") {
			discType = "BDMV"
			return errDiscFound
		}
		if name == "VIDEO_TS" {
			discType = "DVD"
			return errDiscFound
		}
		if name == "HVDVD_TS" {
			discType = "HDDVD"
			return errDiscFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errDiscFound) {
			return discType, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", fmt.Errorf("filesystem: scan disc: %w", err)
	}

	return discType, nil
}
