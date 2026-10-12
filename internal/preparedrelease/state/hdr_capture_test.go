// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHDRProvisionalRecoveryPreservesLiveOwnersAndUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "release", "disc", "hdr-provisional")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	makeCapture := func() HDRCaptureResource {
		t.Helper()
		//nolint:usetesting // Recovery requires captures nested in the feature-owned staging tree.
		directory, err := os.MkdirTemp(parent, "capture-")
		if err != nil {
			t.Fatal(err)
		}
		lease, err := NewHDRCaptureLease(directory)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(lease.release)
		return HDRCaptureResource{Directory: directory, Lease: lease}
	}
	live, abandoned := makeCapture(), makeCapture()
	abandoned.Lease.release() // The OS also closes this descriptor after process exit.
	unrelated := filepath.Join(parent, "notes")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	unowned := filepath.Join(parent, "capture-unowned")
	if err := os.Mkdir(unowned, 0o700); err != nil {
		t.Fatal(err)
	}
	count, err := CleanupHDRCaptures(t.Context(), root)
	if err != nil || count != 1 {
		t.Fatalf("provisional recovery count=%d err=%v", count, err)
	}
	for _, path := range []string{live.Directory, unrelated, unowned} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live or unrelated resource deleted: %s %v", path, err)
		}
	}
	if _, err := os.Stat(abandoned.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned capture retained: %v", err)
	}
	// A replacement Core's second sweep must still leave the original live owner.
	if count, err := CleanupHDRCaptures(t.Context(), root); err != nil || count != 0 {
		t.Fatalf("replacement cleanup count=%d err=%v", count, err)
	}
	ReleaseHDRCaptures(map[string]HDRCaptureResource{"live": live})
	if _, err := os.Stat(live.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner retirement did not release capture: %v", err)
	}
}
