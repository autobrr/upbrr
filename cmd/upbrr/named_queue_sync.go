// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type cliQueueSync struct {
	hierarchy  func(string) error
	checkpoint func(string) error
}

func syncCLIQueueHierarchy(dir string) error {
	if runtime.GOOS == "windows" {
		// The post-rename writable-file flush persists directory structure on NTFS.
		// FlushFileBuffers requires write access; a read-only directory handle is insufficient.
		return nil
	}
	return syncCLIQueueAncestors(dir, func(path string) error { return syncCLIQueuePath(path, os.O_RDONLY) })
}

// syncCLIQueueAncestors also covers data directories created by configuration or
// logging before queue initialization. It does not ignore unsupported sync errors.
func syncCLIQueueAncestors(dir string, syncDirectory func(string) error) error {
	current, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve queue directory hierarchy: %w", err)
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return fmt.Errorf("resolve queue directory ancestors: %w", err)
	}
	for {
		if err := syncDirectory(current); err != nil {
			return fmt.Errorf("sync queue directory ancestor: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func syncCLIQueueCheckpoint(path string) error {
	if runtime.GOOS == "windows" {
		// FlushFileBuffers flushes file metadata after replacement. MS-FSA 2.1.5.7
		// and product note 80 document directory-structure persistence only for NTFS.
		return syncCLIQueuePath(path, os.O_WRONLY)
	}
	return syncCLIQueuePath(filepath.Dir(path), os.O_RDONLY)
}

func syncCLIQueuePath(path string, flags int) error {
	file, err := os.OpenFile(path, flags, 0) //nolint:gosec // Queue checkpoint or its configured directory ancestors; no creation or truncation.
	if err != nil {
		return fmt.Errorf("open queue sync target: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("flush queue sync target: %w", err)
	}
	return nil
}
