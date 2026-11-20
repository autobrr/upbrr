// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build unix

package pathing

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openRegularFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) // #nosec G304 -- Callers supply an explicitly selected local path.
	if err != nil {
		return nil, fmt.Errorf("open regular file: %w", err)
	}
	return file, nil
}
