// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBrowseRootGroupingIsConservative(t *testing.T) {
	t.Parallel()
	entries := []api.BrowseDirectoryEntry{{Path: "first"}, {Path: "second"}, {Path: "third"}}
	keys := map[string]string{
		"first":  "volume-a",
		"second": "volume-b",
		"third":  "volume-a",
	}
	calls := 0
	identify := func(_ context.Context, path string) string { calls++; return keys[path] }
	groups := groupBrowseRoots(t.Context(), entries, identify)
	want := [][]api.BrowseDirectoryEntry{{entries[0], entries[2]}, {entries[1]}}
	if !reflect.DeepEqual(groups, want) || calls != 3 {
		t.Fatalf("groups=%#v, identity probes=%d", groups, calls)
	}
	keys["second"] = ""
	calls = 0
	groups = groupBrowseRoots(t.Context(), entries, identify)
	if !reflect.DeepEqual(groups, [][]api.BrowseDirectoryEntry{entries}) || calls != 2 {
		t.Fatalf("unknown identity must preserve serial order: %#v, probes=%d", groups, calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls = 0
	if groups := groupBrowseRoots(ctx, entries, identify); len(groups) != 0 || calls != 0 {
		t.Fatalf("canceled classification performed work: %#v, probes=%d", groups, calls)
	}
}

func TestBrowseRootGroupingPreservesWindowsCaseAliases(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths compare case-insensitively")
	}
	root := canonicalSearchRoot(t)
	entries := []api.BrowseDirectoryEntry{{Path: root}, {Path: filepath.Join(strings.ToUpper(root), "nested")}}
	groups := groupBrowseRoots(t.Context(), entries, func(_ context.Context, path string) string { return path })
	if !reflect.DeepEqual(groups, [][]api.BrowseDirectoryEntry{entries}) {
		t.Fatalf("case-aliased overlapping roots must preserve serial order: %#v", groups)
	}
}

func TestBrowseGroupWorkersJoinBlockedCallsOnCancellation(t *testing.T) {
	t.Parallel()
	groups := [][]api.BrowseDirectoryEntry{{{Path: "first"}}, {{Path: "second"}}, {{Path: "third"}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan string, 3)
	release := make(chan struct{})
	defer close(release)
	done := make(chan struct{})
	go func() {
		walkBrowseGroups(ctx, groups, func(ctx context.Context, entries []api.BrowseDirectoryEntry) {
			if ctx.Err() != nil {
				return
			}
			started <- entries[0].Path
			<-release // Model a filesystem call that cannot be interrupted.
		})
		close(done)
	}()
	for range 2 {
		<-started
	}
	cancel()
	select {
	case <-done:
		t.Fatal("returned while filesystem workers were blocked")
	case extra := <-started:
		t.Fatalf("more than two workers started: %s", extra)
	case <-time.After(20 * time.Millisecond):
	}
	for range 2 {
		release <- struct{}{}
	}
	<-done
	select {
	case extra := <-started:
		t.Fatalf("started another group after cancellation: %s", extra)
	default:
	}
}

func TestBrowseParallelResultsAndOverlappingRoots(t *testing.T) {
	t.Parallel()
	root := canonicalSearchRoot(t)
	nested := filepath.Join(root, "nested")
	other := canonicalSearchRoot(t)
	for _, directory := range []string{nested, other} {
		for i := range 20 {
			makeBrowseSearchFile(t, filepath.Join(directory, fmt.Sprintf("Beacon.%02d.mkv", i)))
		}
	}
	entries := browseConfiguredRoots("file", []string{root, nested, other}).Entries
	serial := searchBrowseGroups(t.Context(), [][]api.BrowseDirectoryEntry{entries}, "file", "Beacon", 200, 20000)
	parallel := [][]api.BrowseDirectoryEntry{{entries[0]}, {entries[1]}, {entries[2]}}
	for range 10 {
		result := searchBrowseGroups(t.Context(), parallel, "file", "Beacon", 200, 20000)
		if !reflect.DeepEqual(result, serial) || len(result.Entries) != 40 || result.Incomplete {
			t.Fatalf("parallel overlap changed complete results: %#v", result)
		}
	}
}

func TestBrowseOverlappingVolumesPreserveRootScopeAndRepresentatives(t *testing.T) {
	t.Parallel()
	root := canonicalSearchRoot(t)
	nested := filepath.Join(root, "Beacon")
	makeBrowseSearchFile(t, filepath.Join(root, "Other.mkv"))
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	entries := browseConfiguredRoots("file", []string{root, nested}).Entries
	groups := groupBrowseRoots(t.Context(), entries, func(_ context.Context, path string) string { return path })
	if !reflect.DeepEqual(groups, [][]api.BrowseDirectoryEntry{entries}) {
		t.Fatalf("overlapping roots with different identities must retain serial order: %#v", groups)
	}
	alias := filepath.Join(nested, "Beacon.mkv")
	if err := os.Symlink(filepath.Join(root, "Other.mkv"), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	serial := searchBrowseGroups(t.Context(), [][]api.BrowseDirectoryEntry{entries}, "file", "Beacon", 200, 20000)
	for range 10 {
		result := searchBrowseGroups(t.Context(), groups, "file", "Beacon", 200, 20000)
		if !reflect.DeepEqual(result, serial) || result.Incomplete || len(result.Entries) != 2 {
			t.Fatalf("overlapping volume scopes changed directory metadata or alias selection: %#v", result)
		}
		if result.Entries[1].Path != alias {
			t.Fatalf("alias allowed by broader root was omitted: %#v", result)
		}
	}
}

func TestBrowseParallelBudgetsRemainGlobal(t *testing.T) {
	t.Parallel()
	roots := []string{canonicalSearchRoot(t), canonicalSearchRoot(t)}
	for _, root := range roots {
		for i := range 220 {
			makeBrowseSearchFile(t, filepath.Join(root, fmt.Sprintf("Beacon.%03d.mkv", i)))
		}
	}
	entries := browseConfiguredRoots("file", roots).Entries
	groups := [][]api.BrowseDirectoryEntry{{entries[0]}, {entries[1]}}
	for range 10 {
		result := searchBrowseGroups(t.Context(), groups, "file", "Beacon", 200, 20000)
		seen := make(map[string]bool)
		byRoot := [2]int{}
		for _, entry := range result.Entries {
			if seen[entry.Path] {
				t.Fatalf("duplicate result: %s", entry.Path)
			}
			seen[entry.Path] = true
			for i, root := range roots {
				if filepath.Dir(entry.Path) == root {
					byRoot[i]++
				}
			}
		}
		if len(result.Entries) != 200 || !result.Incomplete {
			t.Fatalf("per-request result cap changed: %#v", result)
		}
		t.Logf("capped result distribution across groups: %v", byRoot)
	}
	search := browseSearch{
		ctx:         t.Context(),
		entryLimit:  1000,
		resultLimit: 200,
	}
	var visits atomic.Int32
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for search.visit() {
				visits.Add(1)
			}
		})
	}
	workers.Wait()
	if visits.Load() != 1000 || search.visited != 1000 {
		t.Fatalf("per-request entry cap changed: calls=%d, visited=%d", visits.Load(), search.visited)
	}
}

func TestBrowseSearchIgnoresExcludedBrokenLinks(t *testing.T) {
	t.Parallel()
	root := canonicalSearchRoot(t)
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "Other.mkv")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result := searchBrowseRoots(t.Context(), []string{root}, "file", "Beacon", 200, 20000)
	if result.Incomplete || len(result.Entries) != 0 {
		t.Fatalf("excluded broken link affected query completeness: %#v", result)
	}
	result = searchBrowseRoots(t.Context(), []string{root}, "file", "Other", 200, 20000)
	if !result.Incomplete || len(result.Entries) != 0 {
		t.Fatalf("matching broken link must still report incomplete: %#v", result)
	}
}

func canonicalSearchRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
