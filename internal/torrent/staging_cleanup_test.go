// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStagingMountsDisjoint(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	release := filepath.Join(root, "release with spaces")
	stale := filepath.Join(release, "wanted-files-stale")
	payload := filepath.Join(stale, "payload")
	source := filepath.Join(root, "media")
	escape := strings.NewReplacer(`\`, `\134`, " ", `\040`)
	mount := func(device, backing, target string) string {
		return fmt.Sprintf("12 1 %s %s %s rw - ext4 none rw\n", device, escape.Replace(backing), escape.Replace(target))
	}
	// The staging release itself is mounted at a different backing path.
	backing := filepath.Join(root, "staging-backing")
	base := mount("8:1", root, root) + mount("8:2", backing, release)
	for _, tc := range []struct {
		name  string
		extra string
		safe  bool
	}{
		{name: "ordinary staging", safe: true},
		{
			name:  "source mounted on root device",
			extra: mount("8:1", filepath.Join(root, "other-media"), source),
			safe:  true,
		},
		{
			name:  "bind alias is the source",
			extra: mount("8:2", filepath.Join(backing, filepath.Base(stale), filepath.Base(payload)), source),
		},
		{
			name:  "bind alias is nested under the source",
			extra: mount("8:2", filepath.Join(backing, filepath.Base(stale), filepath.Base(payload)), filepath.Join(source, "nested")),
		},
		{name: "staging owner exposed under source", extra: mount("8:2", backing, filepath.Join(source, "nested"))},
		{name: "mounted payload", extra: mount("8:3", root, payload)},
		{
			name:  "same backing path on unrelated filesystem",
			extra: mount("8:3", filepath.Join(backing, filepath.Base(stale), filepath.Base(payload)), source),
			safe:  true,
		},
		{name: "unreadable mount record", extra: "bad record\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stagingMountsDisjoint(base+tc.extra, stale, source); got != tc.safe {
				t.Fatalf("disjoint = %t, want %t", got, tc.safe)
			}
		})
	}
}

func TestStagingMountsDisjointUsesEffectiveBackingPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	release := filepath.Join(cache, "release")
	stale := filepath.Join(release, "wanted-files-stale")
	library := filepath.Join(root, "library")
	source := filepath.Join(library, "release")
	escape := strings.NewReplacer(`\`, `\134`, " ", `\040`)
	mount := func(device, backing, target string) string {
		return fmt.Sprintf("12 1 %s %s %s rw - ext4 none rw\n", device, escape.Replace(backing), escape.Replace(target))
	}
	sourceContainsStaging := mount("8:1", filepath.Join(source, "cache"), release)
	stagingContainsSource := mount("8:1", library, cache) +
		mount("8:1", filepath.Join(library, "release"), filepath.Join(root, "selected"))
	other := filepath.Join(root, "other")
	hiddenSourceMount := mount("8:2", root, filepath.Join(library, "sub")) +
		mount("8:1", other, library) +
		mount("8:1", filepath.Join(other, "sub", "Show", "cache"), cache)
	for _, tc := range []struct {
		name   string
		mounts string
		source string
		safe   bool
	}{
		{
			name:   "source contains staging on backing device",
			mounts: sourceContainsStaging,
			source: source,
		},
		{
			name:   "staging contains source on backing device",
			mounts: stagingContainsSource,
			source: filepath.Join(root, "selected", filepath.Base(stale), "episode"),
		},
		{
			name:   "source backing on deeper mount",
			mounts: mount("8:1", other, release) + mount("8:2", root, library),
			source: source,
			safe:   true,
		},
		{
			name:   "staging backing on deeper mount",
			mounts: mount("8:1", other, cache) + mount("8:2", root, release),
			source: filepath.Join(source, filepath.Base(stale), "episode"),
			safe:   true,
		},
		{
			name:   "overmounted staging ancestor has overlapping backing path",
			mounts: mount("8:1", library, cache) + mount("8:2", root, release),
			source: filepath.Join(source, filepath.Base(stale), "episode"),
		},
		{
			name:   "source mount hidden by ancestor overmount",
			mounts: hiddenSourceMount,
			source: filepath.Join(library, "sub", "Show"),
		},
		{
			name:   "missing source backing",
			mounts: mount("8:2", root, release),
			source: filepath.Dir(root),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stagingMountsDisjoint(mount("8:1", root, root)+tc.mounts, stale, tc.source); got != tc.safe {
				t.Fatalf("disjoint = %t, want %t", got, tc.safe)
			}
		})
	}
}

func TestStaleStagingTreeProtectsNestedSourceDirectoryIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stale := filepath.Join(root, "wanted-files-stale")
	source := filepath.Join(root, "source")
	nested := filepath.Join(stale, "payload", "nested")
	writeTestFile(t, filepath.Join(source, "ep1.mkv"), "source bytes")
	writeTestFile(t, filepath.Join(nested, "ep1.mkv"), "staged bytes")
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if !staleStagingTreeDisjoint(t.Context(), stale, source, []os.FileInfo{sourceInfo}) {
		t.Fatal("expected staging tree to be disjoint without nested source identity")
	}
	// Use the identity observed through an alias to the nested directory.
	aliasInfo, err := os.Stat(nested)
	if err != nil {
		t.Fatal(err)
	}
	if staleStagingTreeDisjoint(t.Context(), stale, source, []os.FileInfo{sourceInfo, aliasInfo}) {
		t.Fatal("stale staging tree includes an aliased source directory")
	}
}
