// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"fmt"
	"io"
	"os"
)

// CopyFile copies a file from src to dst. If dst does not exist, it will be created.
// If dst exists, it will be overwritten.
func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("filesystem: open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("filesystem: create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("filesystem: copy content: %w", err)
	}

	sourceInfo, err := os.Stat(src)
	if err == nil {
		if err := os.Chmod(dst, sourceInfo.Mode()); err != nil {
			// Non-critical error, just log it if we had a logger here
		}
	}

	return nil
}
