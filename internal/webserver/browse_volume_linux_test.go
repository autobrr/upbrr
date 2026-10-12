// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"testing"

	"golang.org/x/sys/unix"
)

func localBrowseFilesystem() unix.Statfs_t {
	return unix.Statfs_t{Type: unix.EXT4_SUPER_MAGIC}
}

func TestBrowseLocalFilesystemLinux(t *testing.T) {
	for _, test := range []struct {
		name       string
		filesystem unix.Statfs_t
		want       bool
	}{
		{"ext4", unix.Statfs_t{Type: unix.EXT4_SUPER_MAGIC}, true},
		{"xfs", unix.Statfs_t{Type: unix.XFS_SUPER_MAGIC}, true},
		{"btrfs signed magic", unix.Statfs_t{Type: unix.BTRFS_SUPER_MAGIC - 1<<32}, true},
		{"zfs", unix.Statfs_t{Type: browseZFSMagic}, true},
		{"ntfs3", unix.Statfs_t{Type: browseNTFS3Magic}, true},
		{"exfat", unix.Statfs_t{Type: unix.EXFAT_SUPER_MAGIC}, true},
		{"nfs", unix.Statfs_t{Type: unix.NFS_SUPER_MAGIC}, false},
		{"cifs signed magic", unix.Statfs_t{Type: unix.CIFS_SUPER_MAGIC - 1<<32}, false},
		{"fuse", unix.Statfs_t{Type: unix.FUSE_SUPER_MAGIC}, false},
		{"overlay", unix.Statfs_t{Type: unix.OVERLAYFS_SUPER_MAGIC}, false},
		{"unknown", unix.Statfs_t{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := browseLocalFilesystem(&test.filesystem); got != test.want {
				t.Fatalf("local filesystem = %v, want %v", got, test.want)
			}
		})
	}
}
