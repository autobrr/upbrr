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

func TestKeptSingleVideoFolderUsesFolderSavePathAndLinksOnlyVideo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	folder := filepath.Join(root, "Movie.2026")
	video := filepath.Join(folder, "Movie.2026.mkv")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Movie.2026.mkv", "sample.mkv", "release.nfo", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	subject := api.ClientSubject{
		SourcePath: folder,
		FileList:   []string{video},
		KeepFolder: true,
	}
	source, err := sourcePathForLinking(subject)
	if err != nil || source != folder {
		t.Fatalf("link source = %q, %v", source, err)
	}
	save, mapped, err := qbitSavePathForSource(subject, []string{root}, []string{"/downloads"})
	if err != nil || !mapped || save != "/downloads/" {
		t.Fatalf("save path = %q mapped=%t err=%v", save, mapped, err)
	}
	torrentPath := filepath.Join(root, "kept.torrent")
	writeQbitTestTorrent(t, torrentPath, filepath.Base(folder), map[string]string{filepath.Base(video): video}, true)
	plan, err := buildTorrentLinkPlan(t.Context(), torrentPath, subject)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Base(folder), filepath.Base(video))
	if len(plan.files) != 1 || plan.files[0].destRel != want {
		t.Fatalf("link plan = %+v", plan)
	}
	staging := filepath.Join(root, "linked")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := createTorrentLinkPlan(t.Context(), staging, plan, "hardlink"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(staging, filepath.Base(folder)))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(video) {
		t.Fatalf("staged files = %v, %v", entries, err)
	}
	original, err := os.Stat(video)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := os.Stat(filepath.Join(staging, want))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, linked) {
		t.Fatal("selected video was not hardlinked")
	}
	subject.KeepFolder = false
	save, mapped, err = qbitSavePathForSource(subject, []string{root}, []string{"/downloads"})
	if err != nil || !mapped || save != "/downloads/Movie.2026/" {
		t.Fatalf("ordinary save path = %q mapped=%t err=%v", save, mapped, err)
	}
}
