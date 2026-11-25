// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func sameStagingDevice(os.FileInfo, os.FileInfo) bool {
	// Windows volume mount points are reparse points, which WalkDir skips.
	return true
}

func removeStagedPayload(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("remove staged payload: %w", err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat staged payload: %w", err)
	}
	// Directory links, including junctions, are removed as entries.
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("read staged payload directory: %w", err)
		}
		var removeErr error
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return errors.Join(removeErr, err)
			}
			if err := removeStagedPayload(ctx, filepath.Join(path, entry.Name())); err != nil {
				removeErr = errors.Join(removeErr, err)
			}
		}
		if removeErr != nil {
			return removeErr
		}
	}
	if err := removeStagedEntry(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeStagedEntry(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve staged path: %w", err)
	}
	if !strings.HasPrefix(absolute, `\\?\`) && !strings.HasPrefix(absolute, `\\.\`) {
		if unc, ok := strings.CutPrefix(absolute, `\\`); ok {
			absolute = `\\?\UNC\` + unc
		} else {
			absolute = `\\?\` + absolute
		}
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return fmt.Errorf("encode staged path: %w", err)
	}
	if err := unlinkStagedEntry(name); err != nil {
		return &os.PathError{
			Op:   "unlink staged entry",
			Path: path,
			Err:  err,
		}
	}
	return nil
}

func unlinkStagedEntry(name *uint16) error {
	err := windows.DeleteFile(name)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return fmt.Errorf("delete staged file: %w", err)
	}
	if err := windows.RemoveDirectory(name); err == nil {
		return nil
	}
	// Go's removal functions can clear the shared read-only attribute on a
	// hardlink. Unlink through a handle that targets this name, leaving the
	// marker in place if the filesystem cannot perform the safe deletion.
	handle, err := windows.CreateFile(name, windows.DELETE|windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("open staged entry for deletion: %w", err)
	}
	flags := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS | windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE)
	err = windows.SetFileInformationByHandle(handle, windows.FileDispositionInfoEx, (*byte)(unsafe.Pointer(&flags)), 4)
	return errors.Join(err, windows.CloseHandle(handle))
}
