// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build windows

package pathing

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const finalPathBufferSize uint32 = windows.MAX_LONG_PATH

func resolveExistingPath(value string) (string, bool) {
	pathPtr, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return "", false
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", false
	}
	defer func() {
		_ = windows.CloseHandle(handle)
	}()

	buffer := make([]uint16, finalPathBufferSize)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], finalPathBufferSize, 0)
	if err != nil || n == 0 {
		return "", false
	}
	if n >= finalPathBufferSize {
		return "", false
	}

	resolved := windows.UTF16ToString(buffer[:n])
	return filepath.Clean(trimWindowsFinalPathPrefix(resolved)), true
}

func trimWindowsFinalPathPrefix(value string) string {
	trimmed := strings.TrimPrefix(value, `\\?\`)
	if after, ok := strings.CutPrefix(trimmed, `UNC\`); ok {
		return `\\` + after
	}
	return trimmed
}
