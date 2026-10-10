// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/pathing"
)

// ReleaseHDRCaptures deletes only exclusively allocated provisional capture directories.
func ReleaseHDRCaptures(captures map[string]HDRCaptureResource) {
	for _, capture := range captures {
		capture.Lease.release()
		parent := filepath.Dir(capture.Directory)
		if filepath.Base(parent) != "hdr-provisional" || !strings.HasPrefix(filepath.Base(capture.Directory), "capture-") ||
			!pathing.IsWithinRoot(parent, capture.Directory) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(capture.Directory)
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if err == nil && parentErr == nil && pathing.IsWithinRoot(resolvedParent, resolved) {
			_ = os.RemoveAll(capture.Directory)
		}
	}
}

// CleanupHDRCaptures removes only unleased provisional captures under the private tmp root.
// It never follows directory links or retires captures belonging to a live preparation.
func CleanupHDRCaptures(ctx context.Context, root string) (int, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("resolve HDR capture cleanup root: %w", err)
	}
	privateRoot, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return 0, fmt.Errorf("open HDR capture cleanup root: %w", err)
	}
	defer privateRoot.Close()
	count := 0
	err = fs.WalkDir(privateRoot.FS(), ".", func(pathValue string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cancel HDR capture cleanup: %w", err)
		}
		relative := filepath.FromSlash(pathValue)
		if !entry.IsDir() || filepath.Base(filepath.Dir(relative)) != "hdr-provisional" || !strings.HasPrefix(entry.Name(), "capture-") {
			return nil
		}
		leasePath := filepath.Join(relative, "owner.lock")
		info, err := privateRoot.Lstat(leasePath)
		if errors.Is(err, os.ErrNotExist) {
			return filepath.SkipDir
		}
		if err != nil {
			return fmt.Errorf("inspect HDR capture owner: %w", err)
		}
		if !info.Mode().IsRegular() {
			return filepath.SkipDir
		}
		file, err := privateRoot.OpenFile(leasePath, os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("open HDR capture owner: %w", err)
		}
		locked, lockErr := lockHDRCapture(file)
		closeErr := file.Close()
		if err := errors.Join(lockErr, closeErr); err != nil {
			return err
		}
		if locked {
			if err := privateRoot.RemoveAll(relative); err != nil {
				return fmt.Errorf("remove abandoned HDR capture: %w", err)
			}
			count++
		}
		return filepath.SkipDir
	})
	if err != nil {
		return count, fmt.Errorf("clean abandoned HDR captures: %w", err)
	}
	return count, nil
}
