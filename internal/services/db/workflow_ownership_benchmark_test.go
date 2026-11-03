// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// BenchmarkWorkflowSourceLookup uses the same repository entrypoints on both
// sides of the storage migration. Every retained workflow carries a synthetic
// one-MiB prepared snapshot; timed reads must only discover source ownership.
func BenchmarkWorkflowSourceLookup(b *testing.B) {
	for _, count := range []int{16, 160} {
		b.Run(fmt.Sprintf("workflows=%d", count), func(b *testing.B) {
			root := b.TempDir()
			repo, err := Open(filepath.Join(root, "history.sqlite"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(b.Context()); err != nil {
				b.Fatal(err)
			}
			now := time.Now().UTC()
			var selected string
			for index := range count {
				source := filepath.Join(root, fmt.Sprintf("Example.Release.%03d.mkv", index))
				if index == 0 {
					selected = source
				}
				payload := fmt.Sprintf(`{"SourcePath":%q,"Releases":{"release":{"id":"release","revision":1,"release":{"Source":{"SourcePath":%q},"Episode":{"Overview":%q}}}}}`,
					source, source, strings.Repeat("synthetic overview ", (1<<20)/len("synthetic overview ")))
				record := api.ReleaseWorkflowStateRecord{
					OwnerID:    "benchmark",
					WorkflowID: api.WorkflowID(fmt.Sprintf("workflow-%03d", index)),
					Revision:   1,
					Status:     api.WorkflowStatusCompleted,
					Payload:    []byte(payload),
					CreatedAt:  now,
					UpdatedAt:  now,
				}
				if _, _, err := repo.CreateReleaseWorkflowState(b.Context(), record); err != nil {
					b.Fatal(err)
				}
			}
			reportWorkflowSourceLookupPlans(b, repo)
			b.Run("stored-sources", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					paths, err := repo.ListStoredReleasePaths(b.Context())
					if err != nil || len(paths) != count {
						b.Fatalf("source lookup: count=%d err=%v", len(paths), err)
					}
				}
			})
			b.Run("matching-workflow", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					matches, err := matchingStoredReleaseWorkflowStates(b.Context(), repo.RawDB(), selected)
					if err != nil || len(matches) != 1 {
						b.Fatalf("workflow match: count=%d err=%v", len(matches), err)
					}
				}
			})
		})
	}
}

func reportWorkflowSourceLookupPlans(b *testing.B, repo *SQLiteRepository) {
	b.Helper()
	var structured bool
	if err := repo.RawDB().QueryRowContext(b.Context(), `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE name = 'release_workflow_sources')`).Scan(&structured); err != nil {
		b.Fatal(err)
	}
	queries := []string{`SELECT state_json FROM release_workflow_states`, `SELECT owner_id, workflow_id, state_json FROM release_workflow_states`}
	if structured {
		queries = []string{
			`SELECT DISTINCT source_path FROM release_workflow_sources ORDER BY source_path`,
			`SELECT owner_id, workflow_id, source_path FROM release_workflow_sources ORDER BY owner_id, workflow_id, source_path`,
			`SELECT EXISTS (SELECT 1 FROM release_workflow_states AS state
				LEFT JOIN release_workflow_ownership AS ownership USING (owner_id, workflow_id)
				WHERE ownership.revision IS NULL OR ownership.revision <> state.revision OR ownership.ambiguous <> 0)`,
		}
	}
	for index, query := range queries {
		rows, err := repo.RawDB().QueryContext(b.Context(), "EXPLAIN QUERY PLAN "+query)
		if err != nil {
			b.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				b.Fatal(err)
			}
			b.Logf("ownership query %d: %s", index+1, detail)
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
