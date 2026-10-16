// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "golang.org/x/sys/unix"

func browseLocalFilesystem(filesystem *unix.Statfs_t) bool {
	if filesystem.Flags&unix.MNT_LOCAL == 0 {
		return false
	}
	switch unix.ByteSliceToString(filesystem.Fstypename[:]) {
	case "apfs", "hfs", "msdos", "exfat", "ufs", "udf", "cd9660":
		return true
	default:
		return false
	}
}
