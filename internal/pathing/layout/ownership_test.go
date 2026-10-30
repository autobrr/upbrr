// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package layout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

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
	for _, source := range []string{first, second} {
		dir, _, err := ReleaseTempDirFor(root, source, api.ReleaseInfo{})
		if err != nil || (dir != a && dir != b) {
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
