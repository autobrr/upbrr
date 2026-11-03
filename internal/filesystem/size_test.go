// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceSizeNonDisc(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "a.mkv")
	file2 := filepath.Join(tempDir, "b.mkv")
	if err := os.WriteFile(file1, []byte("abc"), 0o600); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("abcd"), 0o600); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	size, err := SourceSize(context.Background(), tempDir, "", []string{file1, file2}, "")
	if err != nil {
		t.Fatalf("SourceSize error: %v", err)
	}
	if size != 7 {
		t.Fatalf("SourceSize = %d, want 7", size)
	}
}

func TestSourceSizeDisc(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "BDMV", "file1.m2ts")
	file2 := filepath.Join(tempDir, "BDMV", "sub", "file2.m2ts")
	if err := os.MkdirAll(filepath.Dir(file1), 0o700); err != nil {
		t.Fatalf("mkdir file1: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(file2), 0o700); err != nil {
		t.Fatalf("mkdir file2: %v", err)
	}
	if err := os.WriteFile(file1, []byte("abc"), 0o600); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("abcd"), 0o600); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	size, err := SourceSize(context.Background(), tempDir, "BDMV", nil, "")
	if err != nil {
		t.Fatalf("SourceSize error: %v", err)
	}
	if size != 7 {
		t.Fatalf("SourceSize = %d, want 7", size)
	}
}

func TestSourceSizeDiscImagePreservesPhysicalPayload(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "Example.3D.iso")
	payload := []byte("synthetic disc image payload")
	if err := os.WriteFile(image, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	// Virtual stream names and generated artifacts must not replace the image.
	size, err := SourceSize(t.Context(), image, "BDMV", []string{filepath.Join(root, "00001.m2ts")}, filepath.Join(root, "summary.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("physical ISO size = %d, want %d", size, len(payload))
	}
}
