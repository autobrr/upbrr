// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux || darwin

package pathing

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenRegularFileRejectsFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.mkv")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		file, _, err := OpenRegularFile(path)
		if file != nil {
			_ = file.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("open FIFO: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("open waited for a FIFO writer")
	}
}
