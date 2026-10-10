// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockHDRCapture(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock HDR capture owner: %w", err)
	}
	return true, nil
}
