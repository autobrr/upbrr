// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package filelock provides nonblocking process locks released by the OS on exit.
package filelock

import (
	"fmt"
	"os"
)

// Acquire opens a lock file, creating it with mode 0600 if needed, and takes
// exclusive ownership until Close or process exit. The parent directory must
// exist. The file must not be removed while any process may hold or acquire it.
func Acquire(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open process lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another process owns the lock: %w", err)
	}
	return file, nil
}
