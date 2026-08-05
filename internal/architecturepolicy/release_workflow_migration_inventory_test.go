// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type migrationInventoryRow struct {
	marker         string
	classification string
	removalPhase   int
	allowedPaths   []string
}

// releaseWorkflowMigrationInventory records only deliberate post-migration
// compatibility. All migration rows must be removed when their seam closes.
var releaseWorkflowMigrationInventory = []migrationInventoryRow{
	{
marker: "RenderDescription",
 classification: "retain_stateless_utility",
 removalPhase: 5,
 allowedPaths: []string{"internal/core/", "internal/webserver/", "webui/src/api/"},
},
}

func TestReleaseWorkflowMigrationInventoryClassifiesProductionCompatibility(t *testing.T) {
	t.Parallel()

	validClassifications := []string{
		"migrate_to_private_publication",
		"migrate_to_workflow_command",
		"migrate_to_workflow_query",
		"migrate_to_workflow_snapshot",
		"retain_stateless_utility",
	}
	root := repositoryRoot(t)
	production := readProductionMigrationSources(t, root)
	seen := make(map[string]struct{}, len(releaseWorkflowMigrationInventory))
	for _, row := range releaseWorkflowMigrationInventory {
		if _, ok := seen[row.marker]; ok {
			t.Errorf("duplicate migration marker %q", row.marker)
			continue
		}
		seen[row.marker] = struct{}{}
		if !slices.Contains(validClassifications, row.classification) {
			t.Errorf("migration marker %q has unknown classification %q", row.marker, row.classification)
		}
		if row.removalPhase < 1 || row.removalPhase > 8 {
			t.Errorf("migration marker %q has invalid removal phase %d", row.marker, row.removalPhase)
		}
		var matches []string
		for relative, content := range production {
			if strings.Contains(content, row.marker) {
				matches = append(matches, relative)
			}
		}
		if len(matches) == 0 {
			t.Errorf("migration marker %q is no longer present; close or remove its ledger row", row.marker)
			continue
		}
		for _, match := range matches {
			if !hasAllowedMigrationPath(match, row.allowedPaths) {
				t.Errorf("migration marker %q has unclassified production caller %q", row.marker, match)
			}
		}
	}
}

func readProductionMigrationSources(t *testing.T, root string) map[string]string {
	t.Helper()

	sources := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		extension := filepath.Ext(path)
		if extension != ".go" && extension != ".ts" && extension != ".tsx" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve migration source path: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if strings.HasSuffix(relative, "_test.go") || strings.HasSuffix(relative, ".test.ts") || strings.HasSuffix(relative, ".test.tsx") ||
			strings.HasPrefix(relative, "internal/architecturepolicy/") || strings.HasPrefix(relative, "webui/src/test/") ||
			strings.Contains(relative, "/generated/") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration source %s: %w", relative, err)
		}
		sources[relative] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("read production migration sources: %v", err)
	}
	return sources
}

func hasAllowedMigrationPath(relative string, allowed []string) bool {
	for _, candidate := range allowed {
		if relative == strings.TrimSuffix(candidate, "/") || strings.HasPrefix(relative, candidate) {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}
