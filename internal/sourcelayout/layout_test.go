// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sourcelayout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

func TestResolveSourceKinds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filePath := filepath.Join(root, "Example.Release.2026.mkv")
	if err := os.WriteFile(filePath, []byte("example"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	ordinary := filepath.Join(root, "ordinary")
	nestedBDMV := filepath.Join(ordinary, "nested", "BDMV")
	if err := os.MkdirAll(nestedBDMV, 0o700); err != nil {
		t.Fatalf("create ordinary source: %v", err)
	}
	discParent := filepath.Join(root, "disc-parent")
	bdmvRoot := filepath.Join(discParent, "BDMV")
	if err := os.MkdirAll(bdmvRoot, 0o700); err != nil {
		t.Fatalf("create BDMV source: %v", err)
	}
	dvdParent := filepath.Join(root, "dvd-parent")
	dvdRoot := filepath.Join(dvdParent, "VIDEO_TS")
	if err := os.MkdirAll(dvdRoot, 0o700); err != nil {
		t.Fatalf("create DVD source: %v", err)
	}

	tests := []struct {
		name        string
		path        string
		kind        Kind
		discType    string
		contentRoot string
		bdmvRoot    string
		dvdRoot     string
	}{
		{
name: "file",
 path: filePath,
 kind: KindFile,
 contentRoot: filePath,
},
		{
name: "ordinary directory",
 path: ordinary,
 kind: KindDirectory,
 contentRoot: ordinary,
},
		{
name: "disc parent",
 path: discParent + string(filepath.Separator),
 kind: KindDiscParent,
 discType: "BDMV",
 contentRoot: bdmvRoot,
 bdmvRoot: bdmvRoot,
},
		{
name: "direct BDMV root",
 path: bdmvRoot,
 kind: KindDiscRoot,
 discType: "BDMV",
 contentRoot: bdmvRoot,
 bdmvRoot: bdmvRoot,
},
		{
name: "DVD parent",
 path: dvdParent,
 kind: KindDiscParent,
 discType: "DVD",
 contentRoot: dvdRoot,
 dvdRoot: dvdRoot,
},
		{
name: "direct DVD root",
 path: dvdRoot,
 kind: KindDiscRoot,
 discType: "DVD",
 contentRoot: dvdRoot,
 dvdRoot: dvdRoot,
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
			assertSamePath(t, layout.ContentRoot, test.contentRoot)
			assertSamePath(t, layout.BDMVRoot, test.bdmvRoot)
			assertSamePath(t, layout.DVDRoot, test.dvdRoot)
			assertSamePath(t, layout.SourcePath, filepath.Clean(test.path))
		})
	}
}

func TestResolveWindowsPathCasing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("host filesystem is case-sensitive")
	}
	root := t.TempDir()
	bdmvRoot := filepath.Join(root, "BDMV")
	if err := os.MkdirAll(bdmvRoot, 0o700); err != nil {
		t.Fatalf("create BDMV source: %v", err)
	}
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

func assertSamePath(t *testing.T, got string, want string) {
	t.Helper()
	if got == "" || want == "" {
		if got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		return
	}
	if !pathutil.SamePath(got, want) {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
