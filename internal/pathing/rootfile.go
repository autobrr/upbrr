// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pathing

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenFileWithinRoot opens target only within root, including inward symlink
// targets. Missing files may be created; unresolvable existing links are rejected.
// The final open is confined by os.Root even if a path changes during resolution.
func OpenFileWithinRoot(root, target string, flag int, perm os.FileMode) (*os.File, error) {
	rootReal, targetReal, ok := resolveWithinRoot(root, target)
	if !ok {
		return nil, &os.PathError{
			Op:   "open within root",
			Path: target,
			Err:  os.ErrPermission,
		}
	}
	directory, err := os.OpenRoot(rootReal)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	defer directory.Close()
	name, err := filepath.Rel(rootReal, targetReal)
	if err != nil {
		return nil, fmt.Errorf("relative storage path: %w", err)
	}
	file, err := directory.OpenFile(name, flag, perm)
	if err != nil {
		return nil, fmt.Errorf("open storage file: %w", err)
	}
	return file, nil
}
