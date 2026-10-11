// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "golang.org/x/sys/unix"

const (
	// These filesystem identifiers are not provided by x/sys/unix.
	browseNTFS3Magic = 0x7366746e
	browseZFSMagic   = 0x2fc12fc1
)

func browseLocalFilesystem(filesystem *unix.Statfs_t) bool {
	// Unknown, network, FUSE and overlay filesystems remain serial: a device
	// number alone cannot establish that their backing storage is local.
	//nolint:gosec // Linux filesystem magic is a 32-bit bit pattern, including on signed 32-bit statfs ABIs.
	switch uint32(filesystem.Type) {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC,
		unix.F2FS_SUPER_MAGIC, unix.BCACHEFS_SUPER_MAGIC, unix.REISERFS_SUPER_MAGIC,
		unix.MSDOS_SUPER_MAGIC, unix.EXFAT_SUPER_MAGIC, browseNTFS3Magic, browseZFSMagic,
		unix.ISOFS_SUPER_MAGIC, unix.UDF_SUPER_MAGIC, unix.TMPFS_MAGIC:
		return true
	default:
		return false
	}
}
