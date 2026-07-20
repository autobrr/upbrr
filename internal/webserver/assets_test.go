// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestCandidateAssetPaths(t *testing.T) {
	t.Parallel()

	paths := candidateAssetPaths()

	if !slices.Contains(paths, filepath.Join("webui", "dist")) {
		t.Fatalf("missing repo-root asset path: %v", paths)
	}
	if len(paths) != 1 {
		t.Fatalf("unexpected asset paths: %v", paths)
	}
}
