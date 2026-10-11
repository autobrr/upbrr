// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var (
	browseGetVolumePathName = windows.GetVolumePathName
	browseGetDriveType      = windows.GetDriveType
	browseGetVolumeName     = windows.GetVolumeNameForVolumeMountPoint
)

// browseVolumeKey probes a canonical root's logical filesystem for scheduling,
// not physical-disk identity or path authorization. Empty means unknown or remote;
// callers must retain serial traversal rather than reject an otherwise valid root.
// Cancellation is checked between native calls; a call in progress cannot be interrupted.
func browseVolumeKey(ctx context.Context, root string) string {
	if ctx.Err() != nil || !filepath.IsAbs(root) {
		return ""
	}
	if !strings.HasPrefix(root, `\\`) {
		//pathpolicy:allow Windows extended-length namespace prefix, not a path join.
		root = `\\?\` + root
	}
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	// Volume mount points can exceed MAX_PATH, just like the input root.
	var volumePath [32768]uint16
	if err := browseGetVolumePathName(path, &volumePath[0], uint32(len(volumePath))); err != nil || ctx.Err() != nil {
		return ""
	}
	switch browseGetDriveType(&volumePath[0]) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_CDROM, windows.DRIVE_RAMDISK:
	default:
		return ""
	}
	if ctx.Err() != nil {
		return ""
	}
	// The mount manager resolves drive-letter and mounted-folder aliases to
	// the same GUID. Drive letters and 32-bit volume serials are not identities.
	var name [50]uint16
	if err := browseGetVolumeName(&volumePath[0], &name[0], uint32(len(name))); err != nil || ctx.Err() != nil {
		return ""
	}
	return strings.ToLower(windows.UTF16ToString(name[:]))
}
