// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pathing

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestIsWithinRootAllowsRootAndChildren(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	child := filepath.Join(root, "child", "file.txt")

	if !IsWithinRoot(root, root) {
		t.Fatalf("expected root to be within itself")
	}
	if !IsWithinRoot(root, child) {
		t.Fatalf("expected child path to be within root")
	}
}

func TestIsWithinRootRejectsSiblingAndParentEscapes(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	if IsWithinRoot(root, filepath.Join(parent, "root-sibling")) {
		t.Fatalf("expected sibling prefix path to be outside root")
	}
	if IsWithinRoot(root, filepath.Join(root, "..", "outside")) {
		t.Fatalf("expected parent escape path to be outside root")
	}
}

func TestIsWithinRootRejectsSymlinkEscapes(t *testing.T) {
	t.Parallel()

	root, outside := setupEscapeDirs(t)
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this host: %v", err)
	}

	assertEscapingLinkRejected(t, root, link)
}

func TestIsWithinRootRejectsDanglingLinks(t *testing.T) {
	t.Parallel()
	for _, directory := range []bool{false, true} {
		t.Run(strconv.FormatBool(directory), func(t *testing.T) {
			root, outside := setupEscapeDirs(t)
			link := filepath.Join(root, "dangling")
			if err := os.Symlink(filepath.Join(outside, "missing"), link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			target := link
			if directory {
				target = filepath.Join(link, "child.nfo")
			}
			if IsWithinRoot(root, target) {
				t.Fatal("dangling outward link accepted")
			}
		})
	}
}

func setupEscapeDirs(t *testing.T) (string, string) {
	t.Helper()

	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	return root, outside
}

func assertEscapingLinkRejected(t *testing.T, root string, link string) {
	t.Helper()

	if IsWithinRoot(root, link) {
		t.Fatalf("expected escaping link to be outside root")
	}
	if IsWithinRoot(root, filepath.Join(link, "missing.txt")) {
		t.Fatalf("expected missing child under escaping symlink to be outside root")
	}
}

func TestSamePathUsesHostSemantics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left := filepath.Join(root, "file.txt")
	if !SamePath(left, filepath.Clean(left)) {
		t.Fatalf("expected clean equivalent paths to match")
	}
	if SamePath(left, filepath.Join(root, "other.txt")) {
		t.Fatalf("expected different paths not to match")
	}
}

func TestSamePathResolvesSymlinkAliases(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	alias := filepath.Join(parent, "root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable on this host: %v", err)
	}

	if !SamePath(root, alias) {
		t.Fatalf("expected symlink alias to match root")
	}
}
