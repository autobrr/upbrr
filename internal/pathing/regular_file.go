// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pathing

import (
	"errors"
	"fmt"
	"os"
)

// ErrNotRegular reports that an opened path is not a regular file.
var ErrNotRegular = errors.New("not a regular file")

// OpenRegularFile opens a file for reading and returns metadata from that same
// handle. On Unix it rejects FIFOs without waiting for a writer.
func OpenRegularFile(path string) (*os.File, os.FileInfo, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("stat opened file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, ErrNotRegular
	}
	return file, info, nil
}

// RegularFileInfo returns handle-backed metadata for the regular file at path.
func RegularFileInfo(path string) (os.FileInfo, error) {
	file, info, err := OpenRegularFile(path)
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	return info, nil
}
