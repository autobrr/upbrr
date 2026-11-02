// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// BenchmarkRuntimeRetainedHistory exercises real startup and settings activation
// against populated synthetic databases. Setup is excluded from the measurements.
func BenchmarkRuntimeRetainedHistory(b *testing.B) {
	for _, retained := range []int{20, 200} {
		for _, operation := range []string{"startup", "settings"} {
			b.Run(fmt.Sprintf("%s/workflows=%d/payload=1MiB", operation, retained), func(b *testing.B) {
				cfg := runtimeHistoryBenchmarkConfig(b, retained)
				var backend *Backend
				if operation == "settings" {
					var err error
					backend, err = NewBackendWithContext(b.Context(), cfg, newEventHub())
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() { _ = backend.Close() })
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if operation == "startup" {
						started, err := NewBackendWithContext(b.Context(), cfg, newEventHub())
						if err != nil {
							b.Fatal(err)
						}
						if err := started.Close(); err != nil {
							b.Fatal(err)
						}
					} else {
						cfg.Metadata.KeepImages = !cfg.Metadata.KeepImages
						payload, err := config.ExportToJSON(&cfg)
						if err != nil {
							b.Fatal(err)
						}
						if err := backend.SaveConfig(payload); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

func runtimeHistoryBenchmarkConfig(b *testing.B, retained int) config.Config {
	b.Helper()
	repoPath := filepath.Join(b.TempDir(), "runtime.sqlite")
	repo, err := db.Open(repoPath)
	if err != nil {
		b.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(b.Context()); err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	opaque := strings.Repeat("x", 1<<20)
	for index := range retained {
		source := filepath.Join(filepath.Dir(repoPath), fmt.Sprintf("Example.Release.%04d.mkv", index))
		workflowID := api.WorkflowID(fmt.Sprintf("workflow-%d", index))
		payload, err := json.Marshal(map[string]any{
			"SourcePath": source,
			"Releases": map[string]any{
				"release": map[string]any{
					"id":       "release",
					"revision": 1,
					"release":  map[string]any{"source": map[string]string{"sourcePath": source}},
					"opaque":   opaque,
				},
			},
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := repo.Save(b.Context(), db.FileMetadata{
Path: source,
 Title: "Example Release",
 UpdatedAt: now,
}); err != nil {
			b.Fatal(err)
		}
		if _, _, err := repo.CreateReleaseWorkflowState(b.Context(), api.ReleaseWorkflowStateRecord{
			OwnerID:    "benchmark",
			WorkflowID: workflowID,
			Revision:   1,
			Status:     api.WorkflowStatusCompleted,
			Payload:    payload,
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			b.Fatal(err)
		}
	}
	return backendConfigTestConfig(repoPath)
}
