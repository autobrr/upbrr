// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"fmt"
	"io"
	"os"
)

// CopyFile copies src to dst, creating or truncating dst without creating its
// parent directory. It applies the source mode when available; copy failures
// may leave a partial destination.
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
		// Best-effort: try to preserve permissions, but ignore failures
		_ = os.Chmod(dst, sourceInfo.Mode())
	}

	return nil
}
