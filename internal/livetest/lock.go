// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package livetest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/autobrr/upbrr/internal/filelock"
)

// Lock serializes live processes and cleanup. The OS releases it on process exit.
// The empty lock file remains, so a crash never requires guessing stale ownership.
func Lock() (*os.File, error) {
	root, err := PrivateRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("live-test create private root: %w", err)
	}
	file, err := filelock.Acquire(filepath.Join(root, "process.lock"))
	if err != nil {
		return nil, fmt.Errorf("live-test open process lock: %w", err)
	}
	return file, nil
}
