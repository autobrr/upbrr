// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package paths

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseTempBaseUsesSourceBasename(t *testing.T) {
	release := api.ReleaseInfo{
		Title:  "Movie.Title",
		Year:   2024,
		Source: "BluRay",
		Type:   "Remux",
		Group:  "GRP",
	}
	base := ReleaseTempBase(api.PreparedMetadata{Release: release}, "C:/Media/Movie.Title.mkv")
	if base != "Movie.Title.mkv" {
		t.Fatalf("unexpected base name: %s", base)
	}
}

func TestReleaseTempBaseFallsBackToReleaseInfoWhenSourceMissing(t *testing.T) {
	release := api.ReleaseInfo{
		Title:  "Movie.Title",
		Year:   2024,
		Source: "BluRay",
		Type:   "Remux",
		Group:  "GRP",
	}
	base := ReleaseTempBase(api.PreparedMetadata{Release: release}, "")
	if base != "Movie.Title.2024.BluRay.Remux-GRP" {
		t.Fatalf("unexpected base name: %s", base)
	}
}

func TestReleaseTempBaseSkipsDuplicateTypeAndSource(t *testing.T) {
	release := api.ReleaseInfo{
		Title:  "Movie.Title",
		Year:   2024,
		Source: "WEB-DL",
		Type:   "WEB-DL",
		Group:  "GRP",
	}
	base := ReleaseTempBase(api.PreparedMetadata{Release: release}, "")
	if base != "Movie.Title.2024.WEB-DL-GRP" {
		t.Fatalf("unexpected base name: %s", base)
	}
}

func TestReleaseTempBaseUsesFolderNameForDirectorySource(t *testing.T) {
	base := ReleaseTempBase(api.PreparedMetadata{}, "C:/Media/Movie.Title.2024")
	if base != "Movie.Title.2024" {
		t.Fatalf("unexpected base name: %s", base)
	}
}

func TestReleaseTempBaseKeepsFileExtension(t *testing.T) {
	base := ReleaseTempBase(api.PreparedMetadata{}, "C:/Media/Movie.Title.mkv")
	if base != "Movie.Title.mkv" {
		t.Fatalf("unexpected base name: %s", base)
	}
}

func TestReleaseTempDirCreatesDirectory(t *testing.T) {
	tmpRoot := t.TempDir()
	dir, base, err := ReleaseTempDir(tmpRoot, api.PreparedMetadata{}, filepath.Join(tmpRoot, "Movie.Title.mkv"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base == "" {
		t.Fatalf("expected base name")
	}
	if dir == "" {
		t.Fatalf("expected directory path")
	}
}
