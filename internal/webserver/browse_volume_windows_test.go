// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestBrowseVolumeKeyWindowsMockedVolumes(t *testing.T) {
	getPath, getType, getName := browseGetVolumePathName, browseGetDriveType, browseGetVolumeName
	t.Cleanup(func() { browseGetVolumePathName, browseGetDriveType, browseGetVolumeName = getPath, getType, getName })
	drive := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	mount := filepath.Join(drive, "mounted-volume") + string(filepath.Separator)
	longMount := filepath.Join(drive, strings.Repeat("long-component", 15), strings.Repeat("another-component", 15)) + string(filepath.Separator)
	const (
		firstGUID = `\\?\Volume{11111111-1111-1111-1111-111111111111}\`
		otherGUID = `\\?\Volume{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}\`
	)
	for _, test := range []struct {
		name      string
		root      string
		mount     string
		driveType uint32
		guid      string
		pathError error
		nameError error
		want      string
	}{
		{
			name:      "local",
			root:      filepath.Join(drive, "media"),
			mount:     drive,
			driveType: windows.DRIVE_FIXED,
			guid:      firstGUID,
			want:      strings.ToLower(firstGUID),
		},
		{
			name:      "same volume",
			root:      filepath.Join(drive, "other"),
			mount:     drive,
			driveType: windows.DRIVE_FIXED,
			guid:      firstGUID,
			want:      strings.ToLower(firstGUID),
		},
		{
			name:      "mounted folder distinct volume",
			root:      filepath.Join(mount, "media"),
			mount:     mount,
			driveType: windows.DRIVE_FIXED,
			guid:      otherGUID,
			want:      strings.ToLower(otherGUID),
		},
		{
			name:      "drive alias same volume",
			root:      filepath.Join(drive, "alias"),
			mount:     drive,
			driveType: windows.DRIVE_FIXED,
			guid:      otherGUID,
			want:      strings.ToLower(otherGUID),
		},
		{
			name:      "long mounted folder",
			root:      filepath.Join(longMount, "media"),
			mount:     longMount,
			driveType: windows.DRIVE_FIXED,
			guid:      otherGUID,
			want:      strings.ToLower(otherGUID),
		},
		{
			name:      "mapped network drive",
			root:      filepath.Join(drive, "network"),
			mount:     drive,
			driveType: windows.DRIVE_REMOTE,
		},
		{
			name:      "UNC share",
			root:      `\\server\share\media`,
			mount:     `\\server\share\`,
			driveType: windows.DRIVE_REMOTE,
		},
		{
			name:      "extended UNC share",
			root:      `\\?\UNC\server\share\media`,
			mount:     `\\?\UNC\server\share\`,
			driveType: windows.DRIVE_REMOTE,
		},
		{
			name:      "unknown drive",
			root:      drive,
			mount:     drive,
			driveType: windows.DRIVE_UNKNOWN,
		},
		{
			name:      "invalid drive",
			root:      drive,
			mount:     drive,
			driveType: windows.DRIVE_NO_ROOT_DIR,
		},
		{
			name:      "root lookup failure",
			root:      drive,
			pathError: windows.ERROR_PATH_NOT_FOUND,
		},
		{
			name:      "GUID lookup failure",
			root:      drive,
			mount:     drive,
			driveType: windows.DRIVE_FIXED,
			nameError: windows.ERROR_NOT_SUPPORTED,
		},
		{
			name:      "empty GUID",
			root:      drive,
			mount:     drive,
			driveType: windows.DRIVE_FIXED,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			browseGetVolumePathName = func(path, buffer *uint16, size uint32) error {
				input := windows.UTF16PtrToString(path)
				wantInput := test.root
				if !strings.HasPrefix(wantInput, `\\`) {
					wantInput = `\\?\` + wantInput
				}
				if input != wantInput {
					t.Fatalf("volume lookup path = %q, want %q", input, wantInput)
				}
				if size < 32768 {
					t.Fatalf("volume mount buffer = %d, cannot hold an extended-length path", size)
				}
				writeBrowseVolumeUTF16(t, buffer, size, test.mount)
				return test.pathError
			}
			browseGetDriveType = func(path *uint16) uint32 {
				if got := windows.UTF16PtrToString(path); got != test.mount {
					t.Fatalf("drive type path = %q, want resolved mount %q", got, test.mount)
				}
				return test.driveType
			}
			nameCalled := false
			browseGetVolumeName = func(path, buffer *uint16, size uint32) error {
				nameCalled = true
				if got := windows.UTF16PtrToString(path); got != test.mount {
					t.Fatalf("GUID lookup path = %q, want resolved mount %q", got, test.mount)
				}
				writeBrowseVolumeUTF16(t, buffer, size, test.guid)
				return test.nameError
			}
			if got := browseVolumeKey(t.Context(), test.root); got != test.want {
				t.Fatalf("volume key = %q, want %q", got, test.want)
			}
			if test.driveType == windows.DRIVE_REMOTE && nameCalled {
				t.Fatal("queried a local volume GUID for a remote drive")
			}
		})
	}
}

func TestBrowseVolumeKeyWindowsRejectsInvalidInput(t *testing.T) {
	for _, root := range []string{"", "relative", filepath.Join(t.TempDir(), "invalid\x00root")} {
		if got := browseVolumeKey(t.Context(), root); got != "" {
			t.Fatalf("invalid root volume key = %q, want empty", got)
		}
	}
}

func TestBrowseVolumeKeyWindowsCancellation(t *testing.T) {
	getPath, getType, getName := browseGetVolumePathName, browseGetDriveType, browseGetVolumeName
	t.Cleanup(func() { browseGetVolumePathName, browseGetDriveType, browseGetVolumeName = getPath, getType, getName })
	for _, cancelAfter := range []int{0, 1, 2, 3} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		if cancelAfter == 0 {
			cancel()
		}
		called := func() {
			calls++
			if calls == cancelAfter {
				cancel()
			}
		}
		browseGetVolumePathName = func(_, _ *uint16, _ uint32) error {
			called()
			return nil
		}
		browseGetDriveType = func(_ *uint16) uint32 {
			called()
			return windows.DRIVE_FIXED
		}
		browseGetVolumeName = func(_ *uint16, buffer *uint16, size uint32) error {
			called()
			writeBrowseVolumeUTF16(t, buffer, size, `\\?\Volume{11111111-1111-1111-1111-111111111111}\`)
			return nil
		}
		key := browseVolumeKey(ctx, t.TempDir())
		cancel()
		if key != "" || calls != cancelAfter {
			t.Fatalf("cancel after %d calls: key = %q, calls = %d", cancelAfter, key, calls)
		}
	}
}

func writeBrowseVolumeUTF16(t *testing.T, buffer *uint16, size uint32, value string) {
	t.Helper()
	encoded, err := windows.UTF16FromString(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > int(size) {
		t.Fatalf("test volume buffer too small: %d < %d", size, len(encoded))
	}
	copy(unsafe.Slice(buffer, size), encoded)
}
