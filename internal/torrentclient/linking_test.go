// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourcePathForQbitSavePathIgnoresRelativeFileListEntry(t *testing.T) {
	// No t.Parallel: t.Chdir forbids it. The chdir into an empty directory
	// proves relative FileList entries can no longer anchor the save path to
	// the process working directory.
	t.Chdir(t.TempDir())

	root := t.TempDir()
	sourcePath := filepath.Join(root, "Movie.2024.1080p-GRP.mkv")

	got, err := sourcePathForQbitSavePath(api.ClientSubject{
		SourcePath: sourcePath,
		FileList:   []string{"Movie.2024.1080p-GRP.mkv"},
	})
	if err != nil {
		t.Fatalf("sourcePathForQbitSavePath: %v", err)
	}
	if got != sourcePath {
		t.Fatalf("expected relative FileList entry to fall back to source path %q, got %q", sourcePath, got)
	}
}

func TestSourcePathForQbitSavePathPrefersAbsoluteSingleFileEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourceDir := filepath.Join(root, "Fixture.Title.2024")
	filePath := filepath.Join(sourceDir, "Fixture.Title.2024.mkv")

	// Nothing is created on disk: resolution must stay lexical because the
	// content may only exist on the remote client host.
	got, err := sourcePathForQbitSavePath(api.ClientSubject{
		SourcePath: sourceDir,
		FileList:   []string{filePath},
	})
	if err != nil {
		t.Fatalf("sourcePathForQbitSavePath: %v", err)
	}
	if got != filePath {
		t.Fatalf("expected absolute single-file entry %q, got %q", filePath, got)
	}
}

func TestSourcePathForLinkingIgnoresRelativeFileListEntryMatchingCWDFile(t *testing.T) {
	// No t.Parallel: t.Chdir forbids it. A same-named file exists in the
	// working directory so the os.Stat probe would succeed if relative
	// entries were still honored.
	trapDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(trapDir, "Movie.mkv"), []byte("wrong file"), 0o600); err != nil {
		t.Fatalf("write trap file: %v", err)
	}
	t.Chdir(trapDir)

	root := t.TempDir()
	sourcePath := filepath.Join(root, "Movie.mkv")
	if err := os.WriteFile(sourcePath, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got, err := sourcePathForLinking(api.ClientSubject{
		SourcePath: sourcePath,
		FileList:   []string{"Movie.mkv"},
	})
	if err != nil {
		t.Fatalf("sourcePathForLinking: %v", err)
	}
	if got != sourcePath {
		t.Fatalf("expected relative FileList entry to fall back to source path %q, got %q", sourcePath, got)
	}
}
