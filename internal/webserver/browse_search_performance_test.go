// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowseSameVolumeRootsBeforeDeepTraversal(t *testing.T) {
	t.Parallel()
	root := canonicalSearchRoot(t)
	first := filepath.Join(root, "Movies")
	roots := []string{first, filepath.Join(root, "Movies2"), filepath.Join(root, "temp"), filepath.Join(root, "TV")}
	for i := range 150 {
		makeBrowseSearchFile(t, filepath.Join(first, "library", fmt.Sprintf("Other.%03d.txt", i)))
	}
	for _, sibling := range roots[1:] {
		makeBrowseSearchFile(t, filepath.Join(sibling, "Beacon.Voyage.mkv"))
	}
	entries := browseConfiguredRoots("file", roots).Entries
	groups := groupBrowseRoots(t.Context(), entries, func(_ context.Context, _ string) string { return "same-volume" })
	result := searchBrowseGroups(t.Context(), groups, "file", "Beacon", 200, 100)
	if !result.Incomplete || len(result.Entries) != 3 {
		t.Fatalf("earlier root exhausted budget before sibling roots: %#v", result)
	}
}

func TestBrowseDefaultBudgetCoversLargerLibraries(t *testing.T) {
	t.Parallel()
	root := canonicalSearchRoot(t)
	// Excluded sidecar files still count as inspected entries. The former
	// 20,000-entry cap stopped even a fast local scan before reaching the end.
	for i := range 20001 {
		path := filepath.Join(root, fmt.Sprintf("Sidecar.%05d.txt", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	makeBrowseSearchFile(t, filepath.Join(root, "nested", "Beacon.Voyage.mkv"))
	result := searchBrowseRoots(t.Context(), []string{root}, "file", "Beacon", browseSearchResultLimit, browseSearchEntryLimit)
	if result.Incomplete || len(result.Entries) != 1 {
		t.Fatalf("default work budget truncated larger library: %#v", result)
	}
}

func BenchmarkBrowseSearchLibraries(b *testing.B) {
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	roots := make([]string, 3)
	for i := range roots {
		roots[i] = filepath.Join(root, fmt.Sprintf("Library%d", i))
		for j := range 128 {
			directory := filepath.Join(roots[i], fmt.Sprintf("Example.Release.%03d", j))
			if err := os.MkdirAll(directory, 0o755); err != nil {
				b.Fatal(err)
			}
			for k := range 16 {
				if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("Sidecar.%02d.txt", k)), nil, 0o600); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		result := searchBrowseRoots(b.Context(), roots, "file", "Beacon", 200, 100000)
		if result.Incomplete || len(result.Entries) != 0 {
			b.Fatalf("unexpected search result: %#v", result)
		}
	}
}
