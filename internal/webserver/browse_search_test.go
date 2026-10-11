// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBrowseNameMatching(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, query string
		want        bool
	}{
		{"Beacon.Voyage", "BEACON voyage", true},
		{"Beacon_Voyage", "voyage beacon", true},
		{"Beacon-[Voyage]_(Journey){2026}", " journey   beacon ", true},
		{"Beacon Voyage", "bcvyge", true},
		{"a123456789b", "ab", true},
		{"a1234567890b", "ab", false},
		{"123456789ab", "ab", true}, // Exact substring wins over fuzzy rank.
		{"Beacon Voyage", "vb", false},
		{"Beacon Voyage", "Beeken Voyage", false},
		{"Café Voyage", "cafe voyage", true},
		{"Cafe", "café", true}, // qui's pre-fold byte-length fallback.
		{"東京_航海", "航海 東京", true},
		{"Beacon Voyage", " ", true},
		{"Beacon Voyage", "[Voyage]", true}, // Brackets normalize; no glob interpretation.
		{"Beacon Voyage", "*Voyage", false},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.query, func(t *testing.T) {
			if got := newBrowseNameMatcher(tc.query).matches(tc.name); got != tc.want {
				t.Fatalf("match=%v, want %v", got, tc.want)
			}
		})
	}
}

func makeBrowseSearchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseRootsSearchScopesAndSelection(t *testing.T) {
	t.Parallel()
	first, second := t.TempDir(), t.TempDir()
	files := []string{filepath.Join(first, "nested", "Beacon.Voyage.mkv"), filepath.Join(second, "deeper", "Beacon.Voyage.mkv")}
	for _, path := range files {
		makeBrowseSearchFile(t, path)
	}
	makeBrowseSearchFile(t, filepath.Join(first, "nested", "Beacon.Voyage.txt"))
	for _, roots := range [][]string{{first}, {first, second}} {
		listing, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{Scope: "roots", Mode: "file"}, "", roots)
		if err != nil {
			t.Fatal(err)
		}
		if listing.Scope != "roots" || listing.CurrentPath != "" || len(listing.Entries) != len(roots) {
			t.Fatalf("roots listing: %#v", listing)
		}
		result, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{
			Scope: "roots",
			Mode:  "file",
			Query: "voyage beacon",
		}, "", roots)
		if err != nil {
			t.Fatal(err)
		}
		if result.Incomplete || len(result.Entries) != len(roots) {
			t.Fatalf("search: %#v", result)
		}
		for _, entry := range result.Entries {
			if entry.IsDir || filepath.Ext(entry.Path) != ".mkv" {
				t.Fatalf("wrong kind: %#v", entry)
			}
			if err := ValidateBrowseSelection(entry.Path, false); err != nil {
				t.Fatalf("unusable selection: %v", err)
			}
		}
	}
	fallback, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{Scope: "directory", Mode: "file"}, first, nil)
	if err != nil {
		t.Fatalf("unrestricted fallback: %#v, %v", fallback, err)
	}
	if runtime.GOOS == "windows" {
		if fallback.Scope != "roots" || fallback.CurrentPath != "" || len(fallback.Entries) == 0 {
			t.Fatalf("unrestricted drive roots: %#v", fallback)
		}
	} else if fallback.Scope != "directory" || fallback.CurrentPath != first {
		t.Fatalf("unrestricted fallback directory: %#v", fallback)
	}
	legacy, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{Mode: "file"}, "", []string{first})
	if err != nil || legacy.Scope != "directory" || legacy.CurrentPath == "" {
		t.Fatalf("legacy single root: %#v, %v", legacy, err)
	}
	local, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{
		Scope: "directory",
		Path:  filepath.Join(first, "nested"),
		Mode:  "file",
	}, "", []string{first})
	if err != nil || len(local.Entries) != 1 || local.Scope != "directory" {
		t.Fatalf("local listing: %#v, %v", local, err)
	}
}

func TestBrowseSearchOverlappingRootsAndSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	file := filepath.Join(nested, "Shared.Voyage.mkv")
	makeBrowseSearchFile(t, file)
	outside := t.TempDir()
	makeBrowseSearchFile(t, filepath.Join(outside, "Forbidden.Voyage.mkv"))
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(nested, "cycle")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root, "Alias.Voyage.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "Forbidden.Voyage.mkv"), filepath.Join(root, "Forbidden.Voyage.mkv")); err != nil {
		t.Fatal(err)
	}
	result, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{
		Scope: "roots",
		Mode:  "file",
		Query: "voyage",
	}, "", []string{root, nested})
	if err != nil {
		t.Fatal(err)
	}
	if result.Incomplete || len(result.Entries) != 1 {
		t.Fatalf("deduplication or containment failed: %#v", result)
	}
	if strings.Contains(result.Entries[0].Name, "Forbidden") {
		t.Fatal("escaped root")
	}
}

func TestBrowseSearchBoundsAndCancellation(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Voyage.One.mkv", "Voyage.Two.mkv", "Voyage.Three.mkv"} {
		makeBrowseSearchFile(t, filepath.Join(root, name))
	}
	limited := searchBrowseRoots(t.Context(), []string{root}, "file", "Voyage", 1, 100)
	if !limited.Incomplete || len(limited.Entries) != 1 {
		t.Fatalf("result limit: %#v", limited)
	}
	visited := searchBrowseRoots(t.Context(), []string{root}, "file", "absent", 100, 1)
	if !visited.Incomplete || len(visited.Entries) != 0 {
		t.Fatalf("entry limit: %#v", visited)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = browsePathsWithinRoots(ctx, api.BrowseDirectoryRequest{Scope: "roots", Query: "Voyage"}, "", []string{root})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	timed := searchBrowseRoots(expired, []string{root}, "file", "Voyage", 100, 100)
	if !timed.Incomplete || len(timed.Entries) != 0 {
		t.Fatalf("time bound: %#v", timed)
	}
	missing := searchBrowseRoots(t.Context(), []string{filepath.Join(root, "missing"), root}, "file", "Voyage", 100, 100)
	if !missing.Incomplete || len(missing.Entries) != 3 {
		t.Fatalf("unreadable root: %#v", missing)
	}
}

func TestBrowseSearchRejectsAmbiguousRequests(t *testing.T) {
	t.Parallel()
	for _, req := range []api.BrowseDirectoryRequest{
		{Scope: "unknown"}, {Scope: "directory", Query: "voyage"}, {Scope: "roots", Path: "unexpected"}, {Scope: "roots", Query: strings.Repeat("x", 1025)},
	} {
		if _, err := browsePathsWithinRoots(t.Context(), req, "", nil); err == nil {
			t.Fatalf("accepted %#v", req)
		}
	}
}

func TestBrowseSearchFileSymlinkCannotSuppressDirectoryTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	folder := filepath.Join(root, "Voyage")
	makeBrowseSearchFile(t, filepath.Join(folder, "Nested.Voyage.mkv"))
	if err := os.Symlink(folder, filepath.Join(root, "Alias.Voyage.mkv")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{
		Scope: "roots",
		Mode:  "file",
		Query: "voyage",
	}, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range result.Entries {
		if entry.Name == "Nested.Voyage.mkv" {
			return
		}
	}
	t.Fatalf("directory target suppressed by file-mode symlink: %#v", result)
}

func TestBrowseSearchRetainsMatchingRootSelection(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "Voyage")
	nested := filepath.Join(root, "Voyage.Nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"file", "folder"} {
		result, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{
			Scope: "roots",
			Mode:  mode,
			Query: "voyage",
		}, "", []string{root, nested})
		if err != nil {
			t.Fatal(err)
		}
		if result.Incomplete || len(result.Entries) != 2 {
			t.Fatalf("matching roots missing/duplicated in %s mode: %#v", mode, result)
		}
		for _, entry := range result.Entries {
			if !entry.IsDir {
				t.Fatalf("root not navigable: %#v", entry)
			}
		}
	}
}

func TestBrowseSeparatorOnlyQueryRestoresRootsWithoutSearching(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	makeBrowseSearchFile(t, filepath.Join(root, "nested", "Beacon.Voyage.mkv"))
	for _, query := range []string{"", "  ", "._-[](){}", " ._ [ ] ( ) { } - "} {
		result, err := browsePathsWithinRoots(t.Context(), api.BrowseDirectoryRequest{
			Scope: "roots",
			Mode:  "file",
			Query: query,
		}, "", []string{root})
		if err != nil {
			t.Fatal(err)
		}
		if result.Incomplete || len(result.Entries) != 1 || !result.Entries[0].IsDir || result.Entries[0].Path != root {
			t.Fatalf("separator-only query %q traversed instead of listing roots: %#v", query, result)
		}
	}
}
