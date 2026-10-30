// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestGatherQueuePathsPreservesRootWhitespace(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	for _, name := range []string{" media", "media "} {
		t.Run(name, func(t *testing.T) {
			if name == "media " && runtime.GOOS == "windows" {
				t.Skip("ordinary Windows paths do not preserve trailing spaces")
			}
			for _, dir := range []string{name, "media"} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(name, "selected.mkv"), []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join("media", "wrong.mkv"), []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := GatherQueuePaths(t.Context(), name)
			want := []string{filepath.Join(base, name, "selected.mkv")}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("selected wrong root: paths=%v err=%v", got, err)
			}
		})
	}
}

func TestGatherQueuePathsExpandsFirstLevelCandidates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write movie: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write note: %v", err)
	}

	seasonDir := filepath.Join(root, "Season 1")
	if err := os.MkdirAll(seasonDir, 0o700); err != nil {
		t.Fatalf("mkdir season: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seasonDir, "episode1.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write episode: %v", err)
	}

	discDir := filepath.Join(root, "Disc")
	if err := os.MkdirAll(filepath.Join(discDir, "BDMV"), 0o700); err != nil {
		t.Fatalf("mkdir disc: %v", err)
	}

	emptyDir := filepath.Join(root, "Empty")
	if err := os.MkdirAll(emptyDir, 0o700); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}

	got, err := GatherQueuePaths(context.Background(), root)
	if err != nil {
		t.Fatalf("gather queue: %v", err)
	}

	want := []string{
		filepath.Join(root, "Disc"),
		filepath.Join(root, "Season 1"),
		filepath.Join(root, "movie.mkv"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gather queue = %#v, want %#v", got, want)
	}
}

func TestLimitQueuePaths(t *testing.T) {
	t.Parallel()

	input := []string{"a", "b", "c"}
	if got := LimitQueuePaths(input, 2); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("limited paths = %#v", got)
	}
	if got := LimitQueuePaths(input, 0); !reflect.DeepEqual(got, input) {
		t.Fatalf("unlimited paths = %#v", got)
	}
}

func TestShouldIncludeQueueDirectoryHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write movie: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	include, err := shouldIncludeQueueDirectory(ctx, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got include=%v err=%v", include, err)
	}
	if include {
		t.Fatal("expected canceled scan to not include directory")
	}
}
