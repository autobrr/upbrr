// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestE2EMetadataUsesNestedManifestVideos(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "Episodes")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{
		filepath.Join(nested, "Example.S00E01.mkv"),
		filepath.Join(nested, "Example.S01E01.mkv"),
	}
	entries := make([]api.SourceManifestEntry, 0, len(files)+3)
	entries = append(entries, api.SourceManifestEntry{Path: nested, Type: api.SourceEntryTypeDirectory})
	for _, name := range append(slices.Clone(files), filepath.Join(nested, "Example.S02E01.nfo"), filepath.Join(nested, "Example.S03E01.sample.mkv")) {
		if err := os.WriteFile(name, []byte("synthetic fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, api.SourceManifestEntry{Path: name, Type: api.SourceEntryTypeFile})
	}
	meta, err := (e2eMetadataService{}).CollectPreparationEvidence(context.Background(), preparationstate.Request{
		Input:    api.PrepareInput{SourcePath: root},
		Manifest: api.SourceManifest{SourcePath: root, Entries: entries},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meta.FileList, files) || meta.VideoPath != files[0] {
		t.Fatalf("file evidence = %v, video = %q; want %v", meta.FileList, meta.VideoPath, files)
	}
}

func TestE2EMetadataRetainsSingleFileAndExplicitSample(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Example.wav", "Example.!sample.mkv"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(file, []byte("synthetic fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			source := file
			if filepath.Ext(file) == ".mkv" {
				source = filepath.Dir(file)
			}
			meta, err := (e2eMetadataService{}).CollectPreparationEvidence(context.Background(), preparationstate.Request{
				Input:    api.PrepareInput{SourcePath: source},
				Manifest: api.SourceManifest{SourcePath: source, Entries: []api.SourceManifestEntry{{Path: file, Type: api.SourceEntryTypeFile}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(meta.FileList, []string{file}) || meta.VideoPath != file {
				t.Fatalf("file evidence = %v, video = %q; want %q", meta.FileList, meta.VideoPath, file)
			}
		})
	}
}
