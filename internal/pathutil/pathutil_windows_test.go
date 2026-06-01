// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build windows

package pathutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestIsWithinRootRejectsWindowsDirectorySymlinkEscapes(t *testing.T) {
	t.Parallel()

	root, outside := setupEscapeDirs(t)
	link := filepath.Join(root, "dir-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("directory symlink unavailable on this host: %v", err)
	}

	assertEscapingLinkRejected(t, root, link)
}

func TestIsWithinRootRejectsWindowsFileSymlinkEscapes(t *testing.T) {
	t.Parallel()

	root, outside := setupEscapeDirs(t)
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	link := filepath.Join(root, "secret-link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("file symlink unavailable on this host: %v", err)
	}

	assertEscapingLinkRejected(t, root, link)
}

func TestIsWithinRootRejectsWindowsJunctionEscapes(t *testing.T) {
	t.Parallel()

	root, outside := setupEscapeDirs(t)
	link := filepath.Join(root, "junction")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "mklink", "/J", link, outside)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("junction unavailable on this host: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = os.Remove(link)
	})

	assertEscapingLinkRejected(t, root, link)
}

func TestIsWithinRootAllowsWindowsCaseVariants(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	child := filepath.Join(root, "ChildDir")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	rootVariant := swapASCIIPathCase(root)
	childVariant := swapASCIIPathCase(child)
	if !IsWithinRoot(rootVariant, childVariant) {
		t.Fatalf("expected case-variant child path to be within root")
	}
	if !SamePath(root, rootVariant) {
		t.Fatalf("expected case-variant root paths to match")
	}
}

func swapASCIIPathCase(value string) string {
	swapped := []byte(value)
	for idx, char := range swapped {
		switch {
		case char >= 'a' && char <= 'z':
			swapped[idx] = char - 'a' + 'A'
		case char >= 'A' && char <= 'Z':
			swapped[idx] = char - 'A' + 'a'
		}
	}
	return string(swapped)
}
