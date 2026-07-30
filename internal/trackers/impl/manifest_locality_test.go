// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestTrackerImplementationsUseFamilyRoots(t *testing.T) {
	t.Parallel()

	allowed := map[string]struct{}{
		"azfamily":   {},
		"commonhttp": {},
		"standalone": {},
		"unit3d":     {},
	}
	root := filepath.Join(trackerManifestRepoRoot(t), "internal", "trackers", "impl")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read tracker implementation root: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := allowed[entry.Name()]; !ok {
			t.Errorf("tracker implementation directory must be under a family root: %s", entry.Name())
		}
	}
}

func TestStandaloneRegistryMatchesPackageDirectories(t *testing.T) {
	t.Parallel()

	registry := MustNewRegistry()
	want := make(map[string]struct{})
	for _, name := range registry.NamesByFamily(trackers.FamilyStandalone) {
		want[strings.ToLower(name)] = struct{}{}
	}

	root := filepath.Join(trackerManifestRepoRoot(t), "internal", "trackers", "impl", "standalone")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read standalone implementation root: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == "internal" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), "profile.go")); err != nil {
			t.Errorf("standalone tracker package must own profile.go: tracker=%s error=%v", entry.Name(), err)
		}
		if _, ok := want[entry.Name()]; !ok {
			t.Errorf("unregistered standalone tracker directory: %s", entry.Name())
			continue
		}
		delete(want, entry.Name())
	}
	for name := range want {
		t.Errorf("standalone registry entry has no package directory: %s", name)
	}
}

func TestGenericTrackerPolicyFilesContainNoSupportedTrackerDispatch(t *testing.T) {
	t.Parallel()

	registry := MustNewRegistry()
	supported := make(map[string]struct{}, len(registry.Names()))
	for _, name := range registry.Names() {
		supported[strings.ToUpper(name)] = struct{}{}
	}
	repoRoot := trackerManifestRepoRoot(t)
	paths := []string{
		"internal/metadata/media_details.go",
		"internal/metadata/source_lookup.go",
		"internal/metadata/tracker_data.go",
		"internal/torrentclient/search.go",
	}
	for _, directory := range []string{"internal/trackers/auth", "internal/imagehosting/policy"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, filepath.FromSlash(directory)), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return fmt.Errorf("walk generic tracker policy path: %w", walkErr)
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
				relative, err := filepath.Rel(repoRoot, path)
				if err != nil {
					return fmt.Errorf("resolve generic tracker policy path: %w", err)
				}
				paths = append(paths, filepath.ToSlash(relative))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", directory, err)
		}
	}
	for _, relative := range paths {
		ignored := map[string]struct{}{}
		if strings.HasPrefix(relative, "internal/imagehosting/policy/") {
			for _, host := range imagehostpolicy.KnownUploadHosts() {
				ignored[strings.ToUpper(host)] = struct{}{}
			}
		}
		assertNoSupportedTrackerStringLiterals(t, repoRoot, relative, supported, ignored)
	}
	for _, relative := range []string{
		"webui/src/hooks/useSettingsState.tsx",
		"webui/src/pages/settings/index.tsx",
	} {
		assertNoSupportedTrackerTypeScriptLiterals(t, repoRoot, relative, supported)
	}
}

func TestUnit3DBaseURLsRemainInSiteProfiles(t *testing.T) {
	t.Parallel()

	repoRoot := trackerManifestRepoRoot(t)
	sitesRoot := filepath.Join(repoRoot, "internal", "trackers", "impl", "unit3d", "sites")
	err := filepath.WalkDir(sitesRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk Unit3D site path: %w", walkErr)
		}
		if entry.IsDir() || entry.Name() == "profile.go" || strings.HasSuffix(entry.Name(), "_test.go") || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return fmt.Errorf("resolve Unit3D site path: %w", err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse Unit3D site source: %w", err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && (strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")) {
				t.Errorf("Unit3D endpoint literal outside profile: file=%s value=%s", filepath.ToSlash(relative), value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan Unit3D site profiles: %v", err)
	}
}

func trackerManifestRepoRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve manifest-locality test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
}

func assertNoSupportedTrackerStringLiterals(
	t *testing.T,
	repoRoot string,
	relative string,
	supported map[string]struct{},
	ignored map[string]struct{},
) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(relative))
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relative, err)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			normalized := strings.ToUpper(strings.TrimSpace(value))
			_, allowed := ignored[normalized]
			if _, found := supported[normalized]; found && !allowed {
				t.Errorf("generic tracker dispatch literal: file=%s value=%q", relative, value)
			}
		}
		return true
	})
}

func assertNoSupportedTrackerTypeScriptLiterals(t *testing.T, repoRoot string, relative string, supported map[string]struct{}) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	quoted := regexp.MustCompile(`["']([A-Za-z0-9]+)["']`)
	for _, match := range quoted.FindAllSubmatch(payload, -1) {
		if _, found := supported[strings.ToUpper(string(match[1]))]; found {
			t.Errorf("generic frontend tracker dispatch literal: file=%s value=%q", relative, match[1])
		}
	}
}
