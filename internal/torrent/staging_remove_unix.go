//go:build !windows

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

func sameStagingDevice(parent, entry os.FileInfo) bool {
	parentStat, parentOK := parent.Sys().(*syscall.Stat_t)
	entryStat, entryOK := entry.Sys().(*syscall.Stat_t)
	return parentOK && entryOK && parentStat.Dev == entryStat.Dev
}

func removeStagedEntry(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("unlink staged entry: %w", err)
	}
	return nil
}

func removeStagedPayload(_ context.Context, path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove staged payload: %w", err)
	}
	return nil
}
