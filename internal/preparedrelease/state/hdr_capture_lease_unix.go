// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !windows

package preparationstate

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockHDRCapture(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock HDR capture owner: %w", err)
	}
	return true, nil
}
