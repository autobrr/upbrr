// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pathing

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if !SamePath(filepath.Join(root, "missing.txt"), filepath.Join(alias, "missing.txt")) {
		t.Fatal("missing paths under equivalent existing prefixes did not match")
	}
}

func TestLocalPathWhitespacePreservesIdentityAndContainment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows paths do not preserve trailing spaces")
	}
	t.Parallel()
	base := t.TempDir()
	plain, spaced := filepath.Join(base, "root"), filepath.Join(base, "root ")
	for _, dir := range []string{plain, spaced} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if SamePath(plain, spaced) || !SamePath(spaced, spaced) {
		t.Fatal("literal trailing whitespace changed path identity")
	}
	if !IsWithinRoot(spaced, filepath.Join(spaced, "child")) || IsWithinRoot(spaced, filepath.Join(plain, "child")) {
		t.Fatal("literal root whitespace changed containment")
	}
}

func TestPathsOverlapDetectsBidirectionalAndCanonicalOverlap(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	left := filepath.Join(parent, "left")
	leftChild := filepath.Join(left, "nested", "file.mkv")
	right := filepath.Join(parent, "left-sibling")
	if err := os.MkdirAll(filepath.Dir(leftChild), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftChild, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}

	if PathsOverlap(left, right) {
		t.Fatal("expected disjoint sibling directories not to overlap")
	}
	if !PathsOverlap(left, left) || !PathsOverlap(left, leftChild) || !PathsOverlap(leftChild, left) {
		t.Fatal("expected same and parent/child paths to overlap in both directions")
	}
	if !PathsOverlap("", left) || !PathsOverlap(left, "") {
		t.Fatal("expected empty path to fail closed as overlapping")
	}

	alias := filepath.Join(right, "alias-to-left")
	createTestDirLinkOrSkip(t, left, alias)
	if !PathsOverlap(left, filepath.Join(alias, "nested", "file.mkv")) || !PathsOverlap(filepath.Join(alias, "nested", "file.mkv"), left) {
		t.Fatal("expected symlink-aliased descendant to overlap target root")
	}

	dangling := filepath.Join(right, "dangling")
	createTestDirLinkOrSkip(t, filepath.Join(parent, "missing-target"), dangling)
	if !PathsOverlap(left, dangling) {
		t.Fatal("expected dangling symlink to fail closed as overlapping")
	}
}

func TestPathsOverlapFilesystemAliases(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "Pack")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(root, "video.mkv")
	if err := os.WriteFile(media, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("hardlinked file", func(t *testing.T) {
		alias := filepath.Join(base, "alias.mkv")
		if err := os.Link(media, alias); err != nil {
			t.Skipf("hardlinks unavailable on test volume: %v", err)
		}
		if !PathsOverlap(media, alias) {
			t.Fatal("expected both names for the same file to overlap")
		}
	})
	t.Run("case-insensitive directory and descendants", func(t *testing.T) {
		alias := filepath.Join(base, "PACK")
		if _, err := os.Stat(alias); os.IsNotExist(err) {
			t.Skip("test volume is case-sensitive")
		} else if err != nil {
			t.Fatal(err)
		}
		for _, child := range []string{filepath.Join(alias, "video.mkv"), filepath.Join(alias, "missing", "video.mkv")} {
			if !PathsOverlap(root, child) || !PathsOverlap(child, root) {
				t.Fatalf("expected both path spellings to protect descendant %s", child)
			}
		}
		if PathsOverlap(media, filepath.Join(alias, "other.mkv")) {
			t.Fatal("sibling names under a shared directory identity must stay disjoint")
		}
	})
}

func createTestDirLinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("symlink unavailable on this host: %v", err)
	}
	output, err := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("junction unavailable on this host: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}
