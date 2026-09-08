// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package livetest

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(file *os.File) error {
	if err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&windows.Overlapped{},
	); err != nil {
		return fmt.Errorf("lock process file: %w", err)
	}
	return nil
}
