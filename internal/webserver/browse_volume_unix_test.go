// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux || darwin

package webserver

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBrowseVolumeKeyUnix(t *testing.T) {
	stat, statfs := browseVolumeStat, browseVolumeStatfs
	t.Cleanup(func() { browseVolumeStat, browseVolumeStatfs = stat, statfs })
	root := t.TempDir()
	browseVolumeStat = func(path string, info *unix.Stat_t) error {
		info.Dev = 42
		switch filepath.Base(path) {
		case "other-volume":
			info.Dev = 43
		case "zero-device":
			info.Dev = 0
		case "stat-failure":
			return unix.ENOENT
		}
		return nil
	}
	browseVolumeStatfs = func(path string, info *unix.Statfs_t) error {
		*info = localBrowseFilesystem()
		switch filepath.Base(path) {
		case "unknown-filesystem":
			*info = unix.Statfs_t{}
		case "statfs-failure":
			return unix.EIO
		}
		return nil
	}
	key := browseVolumeKey(t.Context(), root)
	if key == "" {
		t.Fatal("known local filesystem has no volume key")
	}
	if same := browseVolumeKey(t.Context(), filepath.Join(root, "same-volume")); same != key {
		t.Fatalf("same device keys differ: %q and %q", key, same)
	}
	if other := browseVolumeKey(t.Context(), filepath.Join(root, "other-volume")); other == "" || other == key {
		t.Fatalf("distinct device key = %q, first = %q", other, key)
	}
	for _, name := range []string{"zero-device", "stat-failure", "statfs-failure", "unknown-filesystem"} {
		t.Run(name, func(t *testing.T) {
			if key := browseVolumeKey(t.Context(), filepath.Join(root, name)); key != "" {
				t.Fatalf("unknown identity key = %q, want empty", key)
			}
		})
	}
}

func TestBrowseVolumeKeyUnixMissingRoot(t *testing.T) {
	if key := browseVolumeKey(t.Context(), filepath.Join(t.TempDir(), "missing")); key != "" {
		t.Fatalf("missing root key = %q, want empty", key)
	}
}

func TestBrowseVolumeKeyUnixCancellation(t *testing.T) {
	stat, statfs := browseVolumeStat, browseVolumeStatfs
	t.Cleanup(func() { browseVolumeStat, browseVolumeStatfs = stat, statfs })
	for _, cancelAfter := range []int{0, 1, 2} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		if cancelAfter == 0 {
			cancel()
		}
		browseVolumeStat = func(_ string, info *unix.Stat_t) error {
			calls++
			info.Dev = 42
			if calls == cancelAfter {
				cancel()
			}
			return nil
		}
		browseVolumeStatfs = func(_ string, info *unix.Statfs_t) error {
			calls++
			*info = localBrowseFilesystem()
			cancel()
			return nil
		}
		key := browseVolumeKey(ctx, t.TempDir())
		cancel()
		if key != "" || calls != cancelAfter {
			t.Fatalf("cancel after %d calls: key = %q, calls = %d", cancelAfter, key, calls)
		}
	}
}
