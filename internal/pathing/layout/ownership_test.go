// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package layout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/filelock"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseDirectoryWaitsForClaim(t *testing.T) {
	root := t.TempDir()
	lock, err := filelock.Acquire(filepath.Join(root, ".release-layout.lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, _, err := ReleaseTempDirFor(root, filepath.Join(root, "Example.mkv"), api.ReleaseInfo{})
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("directory claim returned before the existing owner released it: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReleaseDirectoriesIsolateSameBasenameOwners(t *testing.T) {
	root := t.TempDir()
	name := strings.Repeat("a", 180) + ".mkv"
	first := filepath.Join(t.TempDir(), name)
	second := filepath.Join(t.TempDir(), name)
	a, _, err := ReleaseTempDirFor(root, first, api.ReleaseInfo{})
	if err != nil {
		t.Fatal(err)
	}
	b, filenameBase, err := ReleaseTempDirFor(root, second, api.ReleaseInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different source owners share an artifact directory")
	}
	if err := os.WriteFile(filepath.Join(b, filenameBase+".torrent"), []byte("torrent"), 0o600); err != nil {
		t.Fatalf("collision directory expanded an otherwise valid artifact filename: %v", err)
	}
	for source, want := range map[string]string{first: a, second: b} {
		dir, _, err := ReleaseTempDirFor(root, source, api.ReleaseInfo{})
		if err != nil || dir != want {
			t.Fatalf("owner lost across reopen: %s %v", dir, err)
		}
	}
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := ReleaseTempDirFor(root, second, api.ReleaseInfo{})
	if err != nil || reopened != b {
		t.Fatalf("deleting the first owner changed the second owner's root: %s %v", reopened, err)
	}
}

func TestReleaseDirectoryOwnerSurvivesAliasedSourceChanges(t *testing.T) {
	physical := t.TempDir()
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	source := filepath.Join(alias, "Example.Release.mkv")
	root := t.TempDir()
	before, _, err := ReleaseTempDirFor(root, source, api.ReleaseInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, _, err := ReleaseTempDirFor(root, source, api.ReleaseInfo{})
	if err != nil || after != before {
		t.Fatalf("creating source through alias changed owner: before=%s after=%s err=%v", before, after, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	deleted, _, err := ReleaseTempPathFor(root, source, api.ReleaseInfo{})
	if err != nil || deleted != before {
		t.Fatalf("missing source changed cleanup owner: before=%s after=%s err=%v", before, deleted, err)
	}
	target := filepath.Join(physical, "target.mkv")
	if err := os.WriteFile(target, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, source); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	dangling, _, err := ReleaseTempPathFor(root, source, api.ReleaseInfo{})
	if err != nil || dangling != before {
		t.Fatalf("dangling source changed cleanup owner: before=%s after=%s err=%v", before, dangling, err)
	}
}
