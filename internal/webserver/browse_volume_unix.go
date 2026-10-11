// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux || darwin

package webserver

import (
	"context"
	"fmt"

	"golang.org/x/sys/unix"
)

var (
	browseVolumeStat   = unix.Stat
	browseVolumeStatfs = unix.Statfs
)

// browseVolumeKey probes a canonical root's logical filesystem for scheduling,
// not physical-disk identity or path authorization. Empty means unknown or remote;
// callers must retain serial traversal rather than reject an otherwise valid root.
// Cancellation is checked between native calls; a call in progress cannot be interrupted.
func browseVolumeKey(ctx context.Context, root string) string {
	if ctx.Err() != nil {
		return ""
	}
	var stat unix.Stat_t
	if err := browseVolumeStat(root, &stat); err != nil || stat.Dev == 0 || ctx.Err() != nil {
		return ""
	}
	var filesystem unix.Statfs_t
	if err := browseVolumeStatfs(root, &filesystem); err != nil || ctx.Err() != nil || !browseLocalFilesystem(&filesystem) {
		return ""
	}
	return fmt.Sprintf("dev:%x", stat.Dev)
}
