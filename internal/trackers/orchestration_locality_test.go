// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGenericOrchestrationHasNoTrackerNameDispatch(t *testing.T) {
	t.Parallel()

	trackerName := regexp.MustCompile(`"(?:NBL|BTN|ANT|RTF)"`)
	for _, dir := range []string{".", filepath.Join("..", "core")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read orchestration directory %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "e2e_enabled.go" {
				continue
			}
			path := filepath.Join(dir, name)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read orchestration source %s: %v", path, err)
			}
			if match := trackerName.Find(content); match != nil {
				t.Errorf("generic orchestration %s contains tracker-name dispatch %s; move behavior into the tracker profile or adapter", path, match)
			}
		}
	}
}
