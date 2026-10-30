//go:build !windows

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File, wait bool) error {
	flags := unix.LOCK_EX
	if !wait {
		flags |= unix.LOCK_NB
	}
	if err := unix.Flock(int(file.Fd()), flags); err != nil {
		return fmt.Errorf("lock process file: %w", err)
	}
	return nil
}
