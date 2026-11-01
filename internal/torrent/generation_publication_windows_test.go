// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestGeneratedPublicationPreservesLockedDestinationWindows(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "existing.torrent")
	stage := filepath.Join(dir, "staged.torrent")
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("generated"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	publication := &generatedTorrentPublication{stage: stage, destination: destination}
	if _, err := publication.Publish(); err == nil {
		t.Fatal("expected publication to fail while destination disallows deletion")
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "existing" {
		t.Fatalf("existing artifact changed: %q, %v", contents, err)
	}
	if err := publication.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("staged artifact was not removed: %v", err)
	}
}
