// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"testing"

	"golang.org/x/sys/unix"
)

func localBrowseFilesystem() unix.Statfs_t {
	info := unix.Statfs_t{Flags: unix.MNT_LOCAL}
	copy(info.Fstypename[:], "apfs")
	return info
}

func TestBrowseLocalFilesystemDarwin(t *testing.T) {
	for _, test := range []struct {
		name  string
		local bool
		want  bool
	}{
		{"apfs", true, true},
		{"hfs", true, true},
		{"exfat", true, true},
		{"smbfs", false, false},
		{"nfs", false, false},
		{"unknown", true, false},
		{"apfs", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var info unix.Statfs_t
			copy(info.Fstypename[:], test.name)
			if test.local {
				info.Flags = unix.MNT_LOCAL
			}
			if got := browseLocalFilesystem(&info); got != test.want {
				t.Fatalf("local filesystem = %v, want %v", got, test.want)
			}
		})
	}
}
