// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sourcelayout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

func TestResolveSourceKinds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filePath := filepath.Join(root, "Example.Release.2026.mkv")
	writeTestFile(t, filePath)
	ordinary := filepath.Join(root, "ordinary")
	mkdirTest(t, filepath.Join(ordinary, "nested"))
	discParent := filepath.Join(root, "disc-parent")
	bdmvRoot := filepath.Join(discParent, "BDMV")
	mkdirTest(t, bdmvRoot)
	dvdParent := filepath.Join(root, "dvd-parent")
	dvdRoot := filepath.Join(dvdParent, "VIDEO_TS")
	mkdirTest(t, dvdRoot)

	tests := []struct {
		name     string
		path     string
		kind     Kind
		discType string
		discRoot string
	}{
		{
name: "file",
 path: filePath,
 kind: KindFile,
},
		{
name: "ordinary directory",
 path: ordinary,
 kind: KindDirectory,
},
		{
name: "disc parent",
 path: discParent + string(filepath.Separator),
 kind: KindDiscParent,
 discType: "BDMV",
 discRoot: bdmvRoot,
},
		{
name: "direct BDMV root",
 path: bdmvRoot,
 kind: KindDiscRoot,
 discType: "BDMV",
 discRoot: bdmvRoot,
},
		{
name: "DVD parent",
 path: dvdParent,
 kind: KindDiscParent,
 discType: "DVD",
 discRoot: dvdRoot,
},
		{
name: "direct DVD root",
 path: dvdRoot,
 kind: KindDiscRoot,
 discType: "DVD",
 discRoot: dvdRoot,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			layout, err := Resolve(context.Background(), test.path)
			if err != nil {
				t.Fatalf("resolve source: %v", err)
			}
			if layout.Kind != test.kind || layout.DiscType != test.discType {
				t.Fatalf("layout kind/disc = %s/%s, want %s/%s", layout.Kind, layout.DiscType, test.kind, test.discType)
			}
			assertSamePath(t, layout.SourcePath, filepath.Clean(test.path))
			if test.discRoot == "" {
				if len(layout.Discs) != 0 {
					t.Fatalf("discs = %#v, want none", layout.Discs)
				}
				return
			}
			if len(layout.Discs) != 1 {
				t.Fatalf("disc count = %d, want 1", len(layout.Discs))
			}
			assertSamePath(t, layout.Discs[0].Root, test.discRoot)
			if layout.Discs[0].ID == "" || layout.Discs[0].Name != "Disc 1" || layout.Discs[0].Type != test.discType {
				t.Fatalf("disc = %#v", layout.Discs[0])
			}
		})
	}
}

func TestResolveNestedHomogeneousCollections(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	second := filepath.Join(root, "Feature", "Disc 2", "VIDEO_TS")
	first := filepath.Join(root, "Disc 1", "VIDEO_TS")
	mkdirTest(t, second)
	mkdirTest(t, first)
	writeTestFile(t, filepath.Join(first, "VIDEO_TS.IFO"))
	writeTestFile(t, filepath.Join(second, "VIDEO_TS.IFO"))

	layout, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("resolve collection: %v", err)
	}
	if layout.Kind != KindDiscParent || layout.DiscType != "DVD" || len(layout.Discs) != 2 {
		t.Fatalf("layout = %#v", layout)
	}
	if got := []string{layout.Discs[0].Name, layout.Discs[1].Name}; !slices.Equal(got, []string{"Disc 1", "Feature/Disc 2"}) {
		t.Fatalf("disc names = %#v", got)
	}
	assertSamePath(t, layout.Discs[0].Root, first)
	assertSamePath(t, layout.Discs[1].Root, second)
	if layout.Discs[0].ID == layout.Discs[1].ID {
		t.Fatal("disc IDs collided")
	}
}

func TestResolveDiscIDsIgnoreDiscoveryOrderAndSiblingInsertion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	discTwo := filepath.Join(root, "Disc 2", "BDMV")
	discThree := filepath.Join(root, "Disc 3", "BDMV")
	mkdirTest(t, discThree)
	mkdirTest(t, discTwo)
	before, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("resolve before insertion: %v", err)
	}
	beforeIDs := discIDsByName(before.Discs)

	mkdirTest(t, filepath.Join(root, "Disc 1", "BDMV"))
	after, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("resolve after insertion: %v", err)
	}
	afterIDs := discIDsByName(after.Discs)
	for _, name := range []string{"Disc 2", "Disc 3"} {
		if beforeIDs[name] != afterIDs[name] {
			t.Fatalf("%s ID changed: %q != %q", name, beforeIDs[name], afterIDs[name])
		}
	}

	if err := os.Rename(filepath.Join(root, "Disc 2"), filepath.Join(root, "Disc 4")); err != nil {
		t.Fatalf("rename disc: %v", err)
	}
	renamed, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("resolve after rename: %v", err)
	}
	if discIDsByName(renamed.Discs)["Disc 4"] == beforeIDs["Disc 2"] {
		t.Fatal("renamed disc retained its old ID")
	}
}

func TestResolveRejectsMixedAndUnsupportedDiscCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		markers []string
		want    error
	}{
		{
name: "mixed BDMV and DVD",
 markers: []string{"Disc 1/BDMV", "Disc 2/VIDEO_TS"},
 want: ErrMixedDiscCollection,
},
		{
name: "nested HD DVD",
 markers: []string{"Disc 1/HVDVD_TS"},
 want: ErrUnsupportedHDDVDCollection,
},
		{
name: "multiple HD DVD",
 markers: []string{"HVDVD_TS", "Disc 2/HVDVD_TS"},
 want: ErrUnsupportedHDDVDCollection,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, marker := range test.markers {
				mkdirTest(t, filepath.Join(root, filepath.FromSlash(marker)))
			}
			_, err := Resolve(context.Background(), root)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), root) {
				t.Fatalf("error exposes source path: %v", err)
			}
		})
	}

	directParent := t.TempDir()
	directRoot := filepath.Join(directParent, "HVDVD_TS")
	mkdirTest(t, directRoot)
	for _, source := range []string{directRoot, directParent} {
		layout, err := Resolve(context.Background(), source)
		if err != nil {
			t.Fatalf("resolve legacy HD DVD %q: %v", source, err)
		}
		if layout.DiscType != "HDDVD" || len(layout.Discs) != 1 {
			t.Fatalf("legacy HD DVD layout = %#v", layout)
		}
	}
}

func TestResolveRejectsDirectorySymlinksWithoutTraversal(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	mkdirTest(t, filepath.Join(target, "BDMV"))
	root := t.TempDir()
	selectedLink := filepath.Join(root, "selected")
	if err := os.Symlink(target, selectedLink); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if _, err := Resolve(context.Background(), selectedLink); !errors.Is(err, ErrDirectorySymlink) {
		t.Fatalf("selected symlink error = %v", err)
	}

	collection := filepath.Join(root, "collection")
	mkdirTest(t, collection)
	if err := os.Symlink(target, filepath.Join(collection, "Disc 1")); err != nil {
		t.Fatalf("create nested symlink: %v", err)
	}
	if _, err := Resolve(context.Background(), collection); !errors.Is(err, ErrDirectorySymlink) {
		t.Fatalf("nested symlink error = %v", err)
	}
}

func TestResolveRejectsCanonicalIdentityCollisions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("case-insensitive filesystem cannot create colliding marker paths")
	}
	root := t.TempDir()
	mkdirTest(t, filepath.Join(root, "Disc", "BDMV"))
	mkdirTest(t, filepath.Join(root, "disc", "bdmv"))
	if _, err := Resolve(context.Background(), root); !errors.Is(err, ErrConflictingDiscLayout) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestResolveWindowsPathCasing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("host filesystem is case-sensitive")
	}
	root := t.TempDir()
	bdmvRoot := filepath.Join(root, "BDMV")
	mkdirTest(t, bdmvRoot)
	layout, err := Resolve(context.Background(), filepath.Join(root, "bdmv"))
	if err != nil {
		t.Fatalf("resolve differently cased source: %v", err)
	}
	if layout.Kind != KindDiscRoot || layout.DiscType != "BDMV" {
		t.Fatalf("layout = %#v", layout)
	}
}

func TestResolveRejectsMissingAndCanceledSources(t *testing.T) {
	t.Parallel()

	if _, err := Resolve(context.Background(), filepath.Join(t.TempDir(), "missing")); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("missing source error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Resolve(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled source error = %v", err)
	}
}

func discIDsByName(discs []DiscResource) map[string]string {
	result := make(map[string]string, len(discs))
	for _, disc := range discs {
		result[disc.Name] = disc.ID
	}
	return result
}

func mkdirTest(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	mkdirTest(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte("example"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func assertSamePath(t *testing.T, got string, want string) {
	t.Helper()
	if got == "" || want == "" {
		if got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		return
	}
	gotInfo, gotErr := os.Stat(got)
	wantInfo, wantErr := os.Stat(want)
	if gotErr == nil && wantErr == nil && os.SameFile(gotInfo, wantInfo) {
		return
	}
	if !pathutil.SamePath(got, want) {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
