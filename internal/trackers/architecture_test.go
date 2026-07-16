// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const trackerImplImportPrefix = "github.com/autobrr/upbrr/internal/trackers/impl/"

func TestTrackerImplementationImportBoundaries(t *testing.T) {
	t.Parallel()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	allowedDebt := map[string]map[string]bool{}

	roots := []string{
		filepath.Join(repoRoot, "internal", "trackers"),
		filepath.Join(repoRoot, "internal", "metadata"),
		filepath.Join(repoRoot, "internal", "bbcode"),
		filepath.Join(repoRoot, "internal", "imagehosting"),
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			checkImplementationImports(t, repoRoot, path, allowedDebt)
			return nil
		})
		if err != nil {
			t.Fatalf("scan architecture root: %v", err)
		}
	}
	for file, imports := range allowedDebt {
		for importPath, seen := range imports {
			if !seen {
				t.Errorf("stale architecture debt allowlist: file=%s import=%s", file, importPath)
			}
		}
	}
}

func checkImplementationImports(t *testing.T, repoRoot string, filePath string, allowedDebt map[string]map[string]bool) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
	if err != nil {
		t.Errorf("parse imports: %v", err)
		return
	}
	relative, err := filepath.Rel(repoRoot, filePath)
	if err != nil {
		t.Errorf("resolve relative source path: %v", err)
		return
	}
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(importPath, trackerImplImportPrefix) {
			continue
		}
		if implementationImportAllowed(relative, importPath) {
			continue
		}
		if imports := allowedDebt[relative]; imports != nil {
			if _, ok := imports[importPath]; ok {
				imports[importPath] = true
				continue
			}
		}
		t.Errorf("generic or sibling package imports tracker implementation: file=%s import=%s", relative, importPath)
	}
}

func implementationImportAllowed(relative string, importPath string) bool {
	compositionRoot := filepath.Join("internal", "trackers", "impl", "registry.go")
	if relative == compositionRoot {
		return true
	}
	implRoot := filepath.Join("internal", "trackers", "impl") + string(filepath.Separator)
	if strings.HasPrefix(relative, implRoot) && importPath == trackerImplImportPrefix+"commonhttp" {
		return true
	}
	unit3DSitesRoot := filepath.Join("internal", "trackers", "impl", "unit3d", "sites") + string(filepath.Separator)
	return strings.HasPrefix(relative, unit3DSitesRoot) && strings.HasPrefix(importPath, trackerImplImportPrefix+"unit3d")
}
